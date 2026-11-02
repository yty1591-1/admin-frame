#!/usr/bin/env bash
# 数据库恢复（ENT-08 T4/T5）：默认拒绝执行，必须显式确认目标环境。
#
# 用法:
#   RESTORE_CONFIRM=yes DB_DRIVER=mysql DB_DSN='...' \
#     bash scripts/backup/restore.sh /var/backups/admin-frame/mysql-20260101T030000Z.dump.gz
#
#   # 带 PITR：恢复全量后前滚到误操作前的位置
#   RESTORE_CONFIRM=yes PITR_CONFIRM=yes PITR_STOP_POS=87368 \
#   DB_DRIVER=mysql DB_DSN='...' \
#     bash scripts/backup/restore.sh /var/backups/admin-frame/mysql-20260101T030000Z.dump.gz
#
# 环境变量:
#   RESTORE_CONFIRM  必须为 yes（防止误操作生产）
#   DB_DRIVER        mysql | postgres
#   DB_DSN           目标连接串；PostgreSQL 必须是**新建的空库**（脚本不覆盖现有库）
#   BACKUP_ENCRYPT_KEY gpg 口令文件（加密备份必填）
#   RESTORE_SCRATCH  为 yes 时仅恢复到 DB_DSN 指定的临时库（演练用）
#
# PITR（仅 mysql，ENT-08）：恢复全量后按 binlog 前滚到指定时间点 / 位点。
#   PITR_STOP_POS       回放到的位点（在 PITR_STOP_FILE 内部）；精确到字节，优先使用
#   PITR_STOP_DATETIME  回放到的时刻（'YYYY-MM-DD HH:MM:SS'，UTC）；精度只到秒
#   PITR_STOP_FILE      目标 binlog 文件（默认：服务器上最后一个）
#   PITR_START_FILE     起始 binlog 文件（默认：从 `<备份文件>.pos` 读取）
#   PITR_START_POS      起始位点（默认：从 `<备份文件>.pos` 读取）
#   PITR_DRY_RUN        1=只恢复全量并打印回放范围，不实际回放（先用它核对范围）
#   PITR_CONFIRM        必须为 yes 才会真正回放
#
# 为什么 PITR 不复用 RESTORE_CONFIRM：两者后果不同。恢复全量只是回到已知状态；
# 前滚会把 binlog 里的 DELETE / DROP / TRUNCATE 一并重放，stop 位点算错等于
# 造成第二次数据破坏，因此需要独立确认。
#
# 恢复流程（详见 docs/runbooks/backup-restore.md）:
#   1) 停止写入（API 摘流 / worker 暂停）；2) 备份当前库；3) 恢复；
#   4) 跑 verify.sh 与迁移 status；5) 灰度恢复流量。
set -euo pipefail

log() { printf '%s [restore] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=dsn.sh
source "${SCRIPT_DIR}/dsn.sh"

# pos_get <pos-file> <key>
#   读取位点 sidecar 中的单个字段。
#
# 用 grep 解析而不是 `. file`：备份可能来自共享对象存储，source 一个外部文件
# 等于执行任意 shell 代码（SEC-05）。sidecar 只含 key=value，grep 足够。
pos_get() {
  local file="$1" key="$2"
  grep -m1 -E "^${key}=" "${file}" 2>/dev/null | cut -d= -f2- || true
}

# pitr_precheck
#   在「恢复全量」之前完成全部 PITR 参数解析与可行性校验。
#
# 为什么必须提前：全量恢复是破坏性操作（DROP TABLE + CREATE + 导入，覆盖目标库）。
# 若参数冲突或位点文件缺失，等恢复完再报错意味着先毁一次库、再让操作者改参数
# 重来——在崩溃恢复场景下这是不可接受的时间损失。
pitr_precheck() {
  [[ "${PITR_REQUESTED}" == "1" ]] || return 0

  command -v mysqlbinlog >/dev/null 2>&1 || die \
    "mysqlbinlog not found; PITR requires the db-ops image (deploy/backup/Dockerfile) or mysql-community-client — see docs/runbooks/backup-restore.md"

  if [[ -n "${PITR_STOP_POS}" && -n "${PITR_STOP_DATETIME}" ]]; then
    die "set either PITR_STOP_POS or PITR_STOP_DATETIME, not both (POS is exact to the byte; DATETIME is second-granular)"
  fi
  if [[ -n "${PITR_STOP_POS}" && ! "${PITR_STOP_POS}" =~ ^[0-9]+$ ]]; then
    die "PITR_STOP_POS must be a numeric byte position, got '${PITR_STOP_POS}'"
  fi
  if [[ -n "${PITR_STOP_DATETIME}" ]] &&
    ! [[ "${PITR_STOP_DATETIME}" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}[[:space:]][0-9]{2}:[0-9]{2}:[0-9]{2}$ ]]; then
    die "PITR_STOP_DATETIME must be 'YYYY-MM-DD HH:MM:SS' (UTC), got '${PITR_STOP_DATETIME}'"
  fi

  # 起点：显式参数优先，否则取备份 sidecar。
  PITR_START_FILE_RESOLVED="${PITR_START_FILE}"
  PITR_START_POS_RESOLVED="${PITR_START_POS}"
  if [[ -f "${BACKUP_FILE}.pos" ]]; then
    [[ -n "${PITR_START_FILE_RESOLVED}" ]] || PITR_START_FILE_RESOLVED="$(pos_get "${BACKUP_FILE}.pos" BINLOG_FILE)"
    [[ -n "${PITR_START_POS_RESOLVED}" ]] || PITR_START_POS_RESOLVED="$(pos_get "${BACKUP_FILE}.pos" BINLOG_POS)"
  fi
  if [[ -z "${PITR_START_FILE_RESOLVED}" || -z "${PITR_START_POS_RESOLVED}" ]]; then
    die "PITR start position unknown: ${BACKUP_FILE}.pos missing or lacks BINLOG_FILE/BINLOG_POS; pass PITR_START_FILE and PITR_START_POS, or re-take the backup with --source-data (ENT-08 G2)"
  fi
  if ! [[ "${PITR_START_POS_RESOLVED}" =~ ^[0-9]+$ ]]; then
    die "start position must be numeric, got '${PITR_START_POS_RESOLVED}' from ${BACKUP_FILE}.pos"
  fi

  # binlog 定位也在预检内完成：需要连库列 SHOW BINARY LOGS，但只是只读查询。
  # 放在恢复前是因为「binlog 已被清理」是常见的 PITR 失败原因，而恢复全量要几十
  # 秒到数小时——先在只读阶段否掉，比覆盖完目标库再失败合理。
  parse_mysql_dsn "${DB_DSN}" || die "cannot parse mysql DSN for PITR precheck"
  local -a conn=()
  mysql_conn_args conn
  PITR_ALL_FILES="$(mysql "${conn[@]}" -N -e 'SHOW BINARY LOGS' | awk '$1 ~ /^[A-Za-z0-9._-]+$/ {print $1}')"
  [[ -n "${PITR_ALL_FILES}" ]] || die "SHOW BINARY LOGS returned nothing: log_bin is off, or the account lacks REPLICATION CLIENT"
  PITR_TOTAL="$(printf '%s\n' "${PITR_ALL_FILES}" | wc -l | tr -d ' ')"

  PITR_START_IDX="$(printf '%s\n' "${PITR_ALL_FILES}" | grep -nxF -- "${PITR_START_FILE_RESOLVED}" | cut -d: -f1 || true)"
  [[ -n "${PITR_START_IDX}" ]] || die \
    "binlog ${PITR_START_FILE_RESOLVED} no longer exists on the server (purged past the retention window?); PITR from this backup is impossible — check binlog_expire_logs_seconds and the archive under BINLOG_S3_URI"

  PITR_STOP_FILE_RESOLVED="${PITR_STOP_FILE}"
  [[ -n "${PITR_STOP_FILE_RESOLVED}" ]] || PITR_STOP_FILE_RESOLVED="$(printf '%s\n' "${PITR_ALL_FILES}" | tail -1)"
  PITR_STOP_IDX="$(printf '%s\n' "${PITR_ALL_FILES}" | grep -nxF -- "${PITR_STOP_FILE_RESOLVED}" | cut -d: -f1 || true)"
  [[ -n "${PITR_STOP_IDX}" ]] || die "target binlog ${PITR_STOP_FILE_RESOLVED} not found on the server; pass PITR_STOP_FILE explicitly if it was already archived"
  [[ "${PITR_STOP_IDX}" -ge "${PITR_START_IDX}" ]] || die \
    "target binlog ${PITR_STOP_FILE_RESOLVED} precedes start binlog ${PITR_START_FILE_RESOLVED}"
}

# pitr_replay
#   从全备记录的位点前滚 binlog 到 PITR_STOP_POS / PITR_STOP_DATETIME。
#
# 为什么按文件逐个回放：mysqlbinlog 的 --start-position 与 --stop-position 都是
# **文件内**偏移，跨文件时语义不同。只有起始文件带 --start-position、目标文件带
# --stop-position、中间文件全量回放，才能得到「从快照点连续到目标点」的效果。
pitr_replay() {
  parse_mysql_dsn "${DB_DSN}" || die "cannot parse mysql DSN for PITR"
  local -a conn=()
  mysql_conn_args conn

  log "PITR range: ${PITR_START_FILE_RESOLVED}:${PITR_START_POS_RESOLVED} -> ${PITR_STOP_FILE_RESOLVED}:${PITR_STOP_POS:-${PITR_STOP_DATETIME:-end-of-file}}"

  local idx f
  idx="${PITR_START_IDX}"
  while :; do
    f="$(printf '%s\n' "${PITR_ALL_FILES}" | sed -n "${idx}p")"
    local -a args=(--read-from-remote-server
      --host="${DB_HOST}" --port="${DB_PORT}" --user="${DB_USER}")
    [[ "${idx}" -eq "${PITR_START_IDX}" ]] && args+=(--start-position="${PITR_START_POS_RESOLVED}")
    if [[ "${f}" == "${PITR_STOP_FILE_RESOLVED}" ]]; then
      [[ -n "${PITR_STOP_POS}" ]] && args+=(--stop-position="${PITR_STOP_POS}")
      [[ -n "${PITR_STOP_DATETIME}" ]] && args+=(--stop-datetime="${PITR_STOP_DATETIME}")
    fi

    if [[ "${PITR_DRY_RUN}" == "1" ]]; then
      log "DRY-RUN: mysqlbinlog ${args[*]} ${f}"
    else
      log "replaying ${f}"
      # 失败时不自动重试：部分回放的状态无法用「再跑一次」修复（已成功的语句会
      # 被重复应用），必须整库重做。故明确告知调用方从全量恢复重来。
      if ! mysqlbinlog "${args[@]}" "${f}" | mysql "${conn[@]}" "${DB_NAME}"; then
        die "binlog replay failed on ${f}; database is now partially rolled forward — discard it and re-run restore from the full backup. Common causes: start position earlier than the backup snapshot (re-applies already-committed transactions), or a stop position that cuts a transaction in half — check ${BACKUP_FILE}.pos matches this backup file"
      fi
    fi

    [[ "${f}" == "${PITR_STOP_FILE_RESOLVED}" ]] && break
    idx=$((idx + 1))
    [[ "${idx}" -le "${PITR_TOTAL}" ]] || die "reached end of binlog list without hitting ${PITR_STOP_FILE_RESOLVED}"
  done

  log "PITR replay finished"
}

BACKUP_FILE="${1:-}"
[[ -n "${BACKUP_FILE}" ]] || die "usage: restore.sh <backup-file>"
[[ -f "${BACKUP_FILE}" ]] || die "backup file not found: ${BACKUP_FILE}"
[[ "${RESTORE_CONFIRM:-}" == "yes" ]] || die "set RESTORE_CONFIRM=yes to confirm the target environment"

DB_DRIVER="${DB_DRIVER:-}"
DB_DSN="${DB_DSN:-}"
[[ -n "${DB_DRIVER}" ]] || die "DB_DRIVER is required"
[[ -n "${DB_DSN}" ]] || die "DB_DSN is required"

PITR_STOP_POS="${PITR_STOP_POS:-}"
PITR_STOP_DATETIME="${PITR_STOP_DATETIME:-}"
PITR_STOP_FILE="${PITR_STOP_FILE:-}"
PITR_START_FILE="${PITR_START_FILE:-}"
PITR_START_POS="${PITR_START_POS:-}"
PITR_DRY_RUN="${PITR_DRY_RUN:-0}"
PITR_CONFIRM="${PITR_CONFIRM:-}"

# 预检解析结果（set -u 下必须有初值；未启用 PITR 时保持空）。
PITR_ALL_FILES=""
PITR_TOTAL=0
PITR_START_FILE_RESOLVED=""
PITR_START_POS_RESOLVED=""
PITR_START_IDX=0
PITR_STOP_FILE_RESOLVED=""
PITR_STOP_IDX=0

PITR_REQUESTED=0
if [[ -n "${PITR_STOP_POS}" || -n "${PITR_STOP_DATETIME}" ]]; then
  PITR_REQUESTED=1
  [[ "${DB_DRIVER}" == "mysql" ]] || die "PITR is implemented for mysql only (DB_DRIVER=${DB_DRIVER})"
  [[ "${PITR_DRY_RUN}" == "1" || "${PITR_CONFIRM}" == "yes" ]] || die \
    "set PITR_CONFIRM=yes to confirm binlog replay (it re-applies DELETE/DROP/TRUNCATE found in the binlog), or PITR_DRY_RUN=1 to inspect the range first"
fi

# 所有 PITR 前置校验必须在下面这行「覆盖目标库」之前完成。
pitr_precheck

WORK="${BACKUP_FILE}"
TMP_DIR="$(mktemp -d)"
# 清理失败不得改变退出码：恢复已经成功时，/tmp 权限或只读挂载导致的 rm 失败
# 不应把一次成功恢复报成失败（会误导值班判断，甚至触发不必要的回滚）。
trap 'rm -rf "${TMP_DIR}" 2>/dev/null || true' EXIT

if [[ "${BACKUP_FILE}" == *.gpg ]]; then
  [[ -n "${BACKUP_ENCRYPT_KEY:-}" && -f "${BACKUP_ENCRYPT_KEY}" ]] || die "BACKUP_ENCRYPT_KEY file required for encrypted backups"
  gpg --batch --yes --decrypt --passphrase-file "${BACKUP_ENCRYPT_KEY}" \
    --output "${TMP_DIR}/restore.dump.gz" "${BACKUP_FILE}"
  WORK="${TMP_DIR}/restore.dump.gz"
fi

log "verifying gzip integrity"
gzip -t "${WORK}"

[[ -f "${BACKUP_FILE}.sha256" ]] && sha256sum -c "${BACKUP_FILE}.sha256"

case "${DB_DRIVER}" in
  mysql)
    command -v mysql >/dev/null 2>&1 || die "mysql client not found"
    # DSN 不是 mysql CLI 的位置参数（位置参数是数据库名），必须拆成连接参数，
    # 否则会退化成尝试本地 socket 连接并报 ERROR 2002。
    parse_mysql_dsn "${DB_DSN}" ||
      die "cannot parse mysql DSN; expect mysql://user:pass@host:port/db or user:pass@tcp(host:port)/db"
    conn=()
    mysql_conn_args conn
    log "restoring into mysql/${DB_NAME}; ensure API traffic is drained and a safety backup exists"
    gzip -dc "${WORK}" | mysql "${conn[@]}" "${DB_NAME}"
    if [[ "${PITR_REQUESTED}" == "1" ]]; then
      if [[ "${PITR_DRY_RUN}" == "1" ]]; then
        log "full restore done; DRY-RUN — inspecting PITR range only, no replay"
      else
        log "full restore done; starting binlog replay (PITR)"
      fi
      pitr_replay
    fi
    ;;
  postgres)
    command -v pg_restore >/dev/null 2>&1 || die "pg_restore not found"
    log "restoring into a new empty postgres database (target must not be in use)"
    gzip -dc "${WORK}" | pg_restore --no-owner --no-privileges --exit-on-error --dbname="${DB_DSN}"
    ;;
  *)
    die "DB_DRIVER must be mysql or postgres"
    ;;
esac

if [[ "${PITR_REQUESTED}" == "1" ]]; then
  log "restore + PITR finished at ${PITR_STOP_POS:-${PITR_STOP_DATETIME}}; verify row counts and MAX(occurred_at) before routing traffic"
fi
log "restore finished; run scripts/backup/verify.sh and migrate -action status before routing traffic"
log "OK"
