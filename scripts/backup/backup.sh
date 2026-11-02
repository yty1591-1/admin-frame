#!/usr/bin/env bash
# 数据库全量备份（ENT-08 T2）：幂等、可重入、日志可审计。
#
# 用法:
#   DB_DRIVER=mysql DB_DSN='user:pass@tcp(host:3306)/db' bash scripts/backup/backup.sh
#   DB_DRIVER=postgres DB_DSN='postgres://...' bash scripts/backup/backup.sh
#
# 环境变量:
#   DB_DRIVER            mysql | postgres（必填）
#   DB_DSN               连接串（必填；建议由 Secret 注入，禁止写死）
#   BACKUP_DIR           输出目录（默认 /var/backups/admin-frame）
#   BACKUP_RETENTION_DAYS 本地保留天数（默认 7）
#   BACKUP_ENCRYPT_KEY   对称加密口令文件路径（可选，gpg AES256）
#   BACKUP_S3_URI        上传目标（可选，如 s3://bucket/admin-frame/）
#   BACKUP_REQUIRE_POSITION 1=MySQL 备份必须记录 binlog 位点，缺失即失败（默认 1）
#
# 说明:
#   - MySQL 使用 --single-transaction 获得一致性快照；PITR 依赖 binlog 保留期。
#   - MySQL 加 --source-data=2，把快照对应的 binlog 位点写进 dump 头部注释，并
#     另存 sidecar `<备份文件>.pos`。restore.sh 据此自动前滚，不必在恢复时人工
#     执行 SHOW MASTER STATUS（ENT-08 G2）。
#   - 输出 <db>-<UTC 时间戳>.sql.gz + .sha256，便于 verify.sh 校验。
#   - 同一时间只允许一个实例（目录锁），重复执行安全。
#
# 权限要求（MySQL）:
#   - SELECT / LOCK TABLES / SHOW VIEW / EVENT / TRIGGER：读取数据与结构；
#   - RELOAD：--source-data 需要 FLUSH TABLES 以获得与快照一致的位点；
#   - REPLICATION CLIENT：读取 SHOW MASTER STATUS 取得位点。
#   缺 RELOAD 或 REPLICATION CLIENT 时 mysqldump 直接以非 0 退出（实测 8.0.46），
#   不会静默产出「没有位点」的备份。
set -euo pipefail

log() { printf '%s [backup] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=dsn.sh
source "${SCRIPT_DIR}/dsn.sh"

# read_mysql_dump_position <dump-file>
#   从 mysqldump --source-data=2 的头部注释解析快照对应的 binlog 位点。
#   成功时填充全局 POS_FILE / POS_OFFSET 并返回 0；未找到时清空并返回 1。
#
# 只读文件头部：位点注释固定出现在 dump 开头的元数据块（前 20 行左右），
# 无需扫描整个文件——全量 dump 可能有数 GB。
read_mysql_dump_position() {
  local file="$1" auth
  POS_FILE=""
  POS_OFFSET=""
  # MySQL 8.0 输出 CHANGE MASTER TO；8.4 起改称 CHANGE REPLICATION SOURCE TO。
  auth="$(head -c 200000 "${file}" | grep -m1 -E '^-- CHANGE (MASTER|REPLICATION SOURCE) TO ' || true)"
  [[ -n "${auth}" ]] || return 1
  POS_FILE="$(printf '%s' "${auth}" | grep -oE "(MASTER|SOURCE)_LOG_FILE='[^']+'" | sed -E "s/.*='([^']+)'/\1/")"
  POS_OFFSET="$(printf '%s' "${auth}" | grep -oE "(MASTER|SOURCE)_LOG_POS=[0-9]+" | sed -E 's/.*=([0-9]+)/\1/')"
  [[ -n "${POS_FILE}" && -n "${POS_OFFSET}" ]] || return 1
  return 0
}

# write_backup_position <path>
#   写位点 sidecar（key=value，restore.sh 可直接 source）。
#   不用 JSON/INI 是因为 shell 消费方零依赖解析，且不易出现「读了但读错字段」。
write_backup_position() {
  local target="$1"
  {
    printf '# PITR 起点（ENT-08 G2）。缺少本文件的备份只能恢复到最后一次全量，无法前滚。\n'
    printf 'DB_DRIVER=%s\n' "${DB_DRIVER}"
    printf 'DB_NAME=%s\n' "${DB_NAME}"
    printf 'BACKUP_STARTED_UTC=%s\n' "${STARTED_UTC}"
    printf 'BACKUP_FINISHED_UTC=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'BINLOG_FILE=%s\n' "${POS_FILE}"
    printf 'BINLOG_POS=%s\n' "${POS_OFFSET}"
  } >"${target}"
}

DB_DRIVER="${DB_DRIVER:-}"
DB_DSN="${DB_DSN:-}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/admin-frame}"
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-7}"
BACKUP_ENCRYPT_KEY="${BACKUP_ENCRYPT_KEY:-}"
BACKUP_S3_URI="${BACKUP_S3_URI:-}"
BACKUP_REQUIRE_POSITION="${BACKUP_REQUIRE_POSITION:-1}"

STARTED_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
POS_FILE=""
POS_OFFSET=""

[[ -n "${DB_DRIVER}" ]] || die "DB_DRIVER is required"
[[ -n "${DB_DSN}" ]] || die "DB_DSN is required"
[[ "${DB_DRIVER}" == "mysql" || "${DB_DRIVER}" == "postgres" ]] || die "DB_DRIVER must be mysql or postgres"

mkdir -p "${BACKUP_DIR}"
LOCK_DIR="${BACKUP_DIR}/.lock"
if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
  die "another backup is running (lock: ${LOCK_DIR}); remove stale lock manually after verification"
fi
trap 'rmdir "${LOCK_DIR}" 2>/dev/null || true' EXIT

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BASE="${BACKUP_DIR}/${DB_DRIVER}-${STAMP}"
OUT="${BASE}.dump"

case "${DB_DRIVER}" in
  mysql)
    command -v mysqldump >/dev/null 2>&1 || die "mysqldump not found"
    # 同时支持 mysql:// URL 与项目 Go 格式 DSN（user:pass@tcp(host:port)/db），
    # 后者是 ADMIN_FRAME_DB_DSN 的实际格式；口令经 MYSQL_PWD 传递，不进命令行。
    parse_mysql_dsn "${DB_DSN}" ||
      die "cannot parse mysql DSN; expect mysql://user:pass@host:port/db or user:pass@tcp(host:port)/db"
    log "dumping mysql (single-transaction, source-data=2) -> ${OUT}.gz"
    # --source-data=2：把一致性快照对应的 binlog 位点以注释形式写入 dump 头
    # （=1 会写成可执行语句，恢复时真的执行 CHANGE MASTER，不是我们要的）。
    # 该参数执行 FLUSH TABLES 取位点，瞬间阻塞写入（毫秒级），需 RELOAD；
    # 读取位点值需 REPLICATION CLIENT。
    mysqldump --single-transaction --quick --routines --triggers --events \
      --source-data=2 \
      --set-gtid-purged=OFF --no-tablespaces --default-character-set=utf8mb4 \
      --host="${DB_HOST}" --port="${DB_PORT}" --user="${DB_USER}" \
      --result-file="${OUT}" "${DB_NAME}"
    ;;
  postgres)
    command -v pg_dump >/dev/null 2>&1 || die "pg_dump not found"
    log "dumping postgres (custom format) -> ${OUT}"
    pg_dump --format=custom --no-owner --no-privileges --file="${OUT}" "${DB_DSN}"
    ;;
esac

[[ -s "${OUT}" ]] || die "backup file is empty: ${OUT}"

# ENT-08 G2：解析快照位点，供 PITR 前滚。
#
# 解析失败默认是硬失败，不是告警：没有位点的全备只能恢复到备份瞬间，事故时
# 备份之后的写入会全部丢失——而这种备份在 check 层面「存在且可解压」，会被
# 当成可用备份，比直接失败危险得多。
if [[ "${DB_DRIVER}" == "mysql" ]]; then
  if read_mysql_dump_position "${OUT}"; then
    log "binlog position recorded in dump: ${POS_FILE}:${POS_OFFSET}"
  elif [[ "${BACKUP_REQUIRE_POSITION}" == "1" ]]; then
    rm -f "${OUT}"
    die "mysqldump did not record a binlog position (needs RELOAD + REPLICATION CLIENT on the backup account, and log_bin=ON); a backup without a position cannot be rolled forward — set BACKUP_REQUIRE_POSITION=0 only for deployments that never use PITR"
  else
    log "WARN: no binlog position in dump; this backup cannot be rolled forward (PITR unavailable for it)"
  fi
fi

gzip -c "${OUT}" > "${OUT}.gz"
rm -f "${OUT}"

if [[ -n "${BACKUP_ENCRYPT_KEY}" ]]; then
  [[ -f "${BACKUP_ENCRYPT_KEY}" ]] || die "BACKUP_ENCRYPT_KEY file not found"
  # 加密失败必须删除明文：否则「加密备份」失败后会在备份目录留下完整未加密
  # 全量数据，一旦被归档或同步出去即为数据泄露（SEC-05 / AUD-07）。
  if ! gpg --batch --yes --symmetric --cipher-algo AES256 \
    --passphrase-file "${BACKUP_ENCRYPT_KEY}" "${OUT}.gz"; then
    # gpg 中断会留下不完整密文，与成功产物同名，必须一并删除，
    # 否则「备份文件存在」的监控会把它当成可用备份。
    rm -f "${OUT}.gz" "${OUT}.gz.gpg"
    die "gpg encryption failed; partial artifacts removed to avoid leaking plaintext or a broken ciphertext"
  fi
  rm -f "${OUT}.gz"
  FINAL="${OUT}.gz.gpg"
else
  FINAL="${OUT}.gz"
fi

sha256sum "${FINAL}" > "${FINAL}.sha256"
SIZE="$(wc -c < "${FINAL}" | tr -d ' ')"

# sidecar 跟随最终文件名（加密后是 .gpg），restore.sh 用 "${BACKUP_FILE}.pos" 查找，
# 不需要知道备份是否加密过。
if [[ -n "${POS_FILE}" && -n "${POS_OFFSET}" ]]; then
  write_backup_position "${FINAL}.pos"
  log "position sidecar: ${FINAL}.pos"
fi

log "backup complete: ${FINAL} (${SIZE} bytes)"

if [[ -n "${BACKUP_S3_URI}" ]]; then
  command -v aws >/dev/null 2>&1 || die "aws cli not found for BACKUP_S3_URI upload"
  aws s3 cp "${FINAL}" "${BACKUP_S3_URI%/}/$(basename "${FINAL}")"
  aws s3 cp "${FINAL}.sha256" "${BACKUP_S3_URI%/}/$(basename "${FINAL}.sha256")"
  # 位点文件必须与备份一起上传：只上传备份而漏掉位点，异地恢复时同样无法前滚。
  if [[ -f "${FINAL}.pos" ]]; then
    aws s3 cp "${FINAL}.pos" "${BACKUP_S3_URI%/}/$(basename "${FINAL}.pos")"
  fi
  log "uploaded to ${BACKUP_S3_URI}"
fi

# 轮转：仅清理本脚本命名的文件，避免误删其它备份。
find "${BACKUP_DIR}" -maxdepth 1 -type f \
  \( -name 'mysql-*.dump.gz' -o -name 'mysql-*.dump.gz.gpg' -o -name 'postgres-*.dump.gz' -o -name 'postgres-*.dump.gz.gpg' \
     -o -name 'mysql-*.dump.gz.sha256' -o -name 'mysql-*.dump.gz.gpg.sha256' \
     -o -name 'mysql-*.dump.gz.pos' -o -name 'mysql-*.dump.gz.gpg.pos' \
     -o -name 'postgres-*.dump.gz.sha256' -o -name 'postgres-*.dump.gz.gpg.sha256' \
     -o -name 'postgres-*.dump.gz.pos' -o -name 'postgres-*.dump.gz.gpg.pos' \) \
  -mtime +"${BACKUP_RETENTION_DAYS}" -print -delete | while read -r old; do
    log "rotated out: ${old}"
  done

log "OK"
