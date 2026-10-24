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
#
# 说明:
#   - MySQL 使用 --single-transaction 获得一致性快照；PITR 依赖 binlog 保留期。
#   - 输出 <db>-<UTC 时间戳>.sql.gz + .sha256，便于 verify.sh 校验。
#   - 同一时间只允许一个实例（目录锁），重复执行安全。
set -euo pipefail

log() { printf '%s [backup] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

DB_DRIVER="${DB_DRIVER:-}"
DB_DSN="${DB_DSN:-}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/admin-frame}"
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-7}"
BACKUP_ENCRYPT_KEY="${BACKUP_ENCRYPT_KEY:-}"
BACKUP_S3_URI="${BACKUP_S3_URI:-}"

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
    # 支持 mysql://user:pass@host:3306/db 形式的 DSN（口令走 MYSQL_PWD，不进命令行）。
    if [[ "${DB_DSN}" =~ ^mysql:// ]]; then
      rest="${DB_DSN#mysql://}"
      userinfo="${rest%%@*}"
      hostpart="${rest#*@}"
      DB_USER="${userinfo%%:*}"
      export MYSQL_PWD="${userinfo#*:}"
      hostport="${hostpart%%/*}"
      DB_NAME="${hostpart#*/}"
      DB_NAME="${DB_NAME%%\?*}"
      DB_HOST="${hostport%%:*}"
      DB_PORT="${hostport#*:}"
      [[ "${DB_PORT}" == "${DB_HOST}" ]] && DB_PORT=3306
    fi
    : "${DB_NAME:?set DB_NAME or use mysql:// URL}"
    : "${DB_HOST:?set DB_HOST or use mysql:// URL}"
    : "${DB_USER:?set DB_USER or use mysql:// URL}"
    log "dumping mysql (single-transaction) -> ${OUT}.gz"
    mysqldump --single-transaction --quick --routines --triggers --events \
      --set-gtid-purged=OFF --no-tablespaces --default-character-set=utf8mb4 \
      --host="${DB_HOST}" --port="${DB_PORT:-3306}" --user="${DB_USER}" \
      --result-file="${OUT}" "${DB_NAME}"
    ;;
  postgres)
    command -v pg_dump >/dev/null 2>&1 || die "pg_dump not found"
    log "dumping postgres (custom format) -> ${OUT}"
    pg_dump --format=custom --no-owner --no-privileges --file="${OUT}" "${DB_DSN}"
    ;;
esac

[[ -s "${OUT}" ]] || die "backup file is empty: ${OUT}"
gzip -c "${OUT}" > "${OUT}.gz"
rm -f "${OUT}"

if [[ -n "${BACKUP_ENCRYPT_KEY}" ]]; then
  [[ -f "${BACKUP_ENCRYPT_KEY}" ]] || die "BACKUP_ENCRYPT_KEY file not found"
  gpg --batch --yes --symmetric --cipher-algo AES256 \
    --passphrase-file "${BACKUP_ENCRYPT_KEY}" "${OUT}.gz"
  rm -f "${OUT}.gz"
  FINAL="${OUT}.gz.gpg"
else
  FINAL="${OUT}.gz"
fi

sha256sum "${FINAL}" > "${FINAL}.sha256"
SIZE="$(wc -c < "${FINAL}" | tr -d ' ')"
log "backup complete: ${FINAL} (${SIZE} bytes)"

if [[ -n "${BACKUP_S3_URI}" ]]; then
  command -v aws >/dev/null 2>&1 || die "aws cli not found for BACKUP_S3_URI upload"
  aws s3 cp "${FINAL}" "${BACKUP_S3_URI%/}/$(basename "${FINAL}")"
  aws s3 cp "${FINAL}.sha256" "${BACKUP_S3_URI%/}/$(basename "${FINAL}.sha256")"
  log "uploaded to ${BACKUP_S3_URI}"
fi

# 轮转：仅清理本脚本命名的文件，避免误删其它备份。
find "${BACKUP_DIR}" -maxdepth 1 -type f \
  \( -name 'mysql-*.dump.gz' -o -name 'mysql-*.dump.gz.gpg' -o -name 'postgres-*.dump.gz' -o -name 'postgres-*.dump.gz.gpg' \
     -o -name 'mysql-*.dump.gz.sha256' -o -name 'mysql-*.dump.gz.gpg.sha256' \
     -o -name 'postgres-*.dump.gz.sha256' -o -name 'postgres-*.dump.gz.gpg.sha256' \) \
  -mtime +"${BACKUP_RETENTION_DAYS}" -print -delete | while read -r old; do
    log "rotated out: ${old}"
  done

log "OK"
