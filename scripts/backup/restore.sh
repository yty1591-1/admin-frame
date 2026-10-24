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
trap 'rm -rf "${TMP_DIR}"' EXIT

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
    log "restoring into mysql; ensure API traffic is drained and a safety backup exists"
    gzip -dc "${WORK}" | mysql --default-character-set=utf8mb4 "${DB_DSN}"
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
