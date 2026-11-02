#!/usr/bin/env bash
# 数据库恢复（ENT-08 T4/T5）：默认拒绝执行，必须显式确认目标环境。
#
# 用法:
#   RESTORE_CONFIRM=yes DB_DRIVER=mysql DB_DSN='...' \
#     bash scripts/backup/restore.sh /var/backups/admin-frame/mysql-20260101T030000Z.dump.gz
#
# 环境变量:
#   RESTORE_CONFIRM  必须为 yes（防止误操作生产）
#   DB_DRIVER        mysql | postgres
#   DB_DSN           目标连接串；PostgreSQL 必须是**新建的空库**（脚本不覆盖现有库）
#   BACKUP_ENCRYPT_KEY gpg 口令文件（加密备份必填）
#   RESTORE_SCRATCH  为 yes 时仅恢复到 DB_DSN 指定的临时库（演练用）
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

BACKUP_FILE="${1:-}"
[[ -n "${BACKUP_FILE}" ]] || die "usage: restore.sh <backup-file>"
[[ -f "${BACKUP_FILE}" ]] || die "backup file not found: ${BACKUP_FILE}"
[[ "${RESTORE_CONFIRM:-}" == "yes" ]] || die "set RESTORE_CONFIRM=yes to confirm the target environment"

DB_DRIVER="${DB_DRIVER:-}"
DB_DSN="${DB_DSN:-}"
[[ -n "${DB_DRIVER}" ]] || die "DB_DRIVER is required"
[[ -n "${DB_DSN}" ]] || die "DB_DSN is required"

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

log "restore finished; run scripts/backup/verify.sh and migrate -action status before routing traffic"
log "OK"
