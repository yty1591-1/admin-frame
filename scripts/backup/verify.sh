#!/usr/bin/env bash
# 备份校验（ENT-08 T2/T7）：校验和、压缩完整性，可选把备份载入临时库做行数抽查。
#
# 用法:
#   bash scripts/backup/verify.sh /var/backups/admin-frame/mysql-20260101T030000Z.dump.gz
#   DB_DRIVER=mysql DB_DSN='...' SCRATCH_TABLE=iam_role \
#     bash scripts/backup/verify.sh <file> --load
#
# 退出码: 0 通过；非 0 失败（可直接用于备份失败告警）。
set -euo pipefail

log() { printf '%s [verify] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=dsn.sh
source "${SCRIPT_DIR}/dsn.sh"

FILE="${1:-}"
MODE="${2:-}"
[[ -n "${FILE}" ]] || die "usage: verify.sh <backup-file> [--load]"
[[ -f "${FILE}" ]] || die "file not found: ${FILE}"

if [[ -f "${FILE}.sha256" ]]; then
  log "checking sha256"
  sha256sum -c "${FILE}.sha256"
else
  log "WARN: ${FILE}.sha256 missing; skipping checksum"
fi

WORK="${FILE}"
if [[ "${FILE}" == *.gpg ]]; then
  [[ -n "${BACKUP_ENCRYPT_KEY:-}" && -f "${BACKUP_ENCRYPT_KEY}" ]] || die "BACKUP_ENCRYPT_KEY required for .gpg"
  TMP="$(mktemp)"
  gpg --batch --yes --decrypt --passphrase-file "${BACKUP_ENCRYPT_KEY}" --output "${TMP}" "${FILE}"
  WORK="${TMP}"
fi
log "checking gzip integrity"
gzip -t "${WORK}"
[[ -s "${WORK}" ]] || die "empty backup"

if [[ "${MODE}" == "--load" ]]; then
  DB_DRIVER="${DB_DRIVER:?DB_DRIVER required for --load}"
  DB_DSN="${DB_DSN:?DB_DSN required for --load}"
  command -v mysql >/dev/null 2>&1 || die "mysql client not found for --load"
  case "${DB_DRIVER}" in
    mysql)
      # 同 restore.sh：DSN 必须拆成 --host/--port/--user + 数据库名。
      parse_mysql_dsn "${DB_DSN}" ||
        die "cannot parse mysql DSN; expect mysql://user:pass@host:port/db or user:pass@tcp(host:port)/db"
      conn=()
      mysql_conn_args conn
      log "loading into scratch mysql/${DB_NAME} (must be disposable)"
      gzip -dc "${WORK}" | mysql "${conn[@]}" "${DB_NAME}"
      ;;
    postgres)
      log "loading into scratch postgres (must be disposable)"
      gzip -dc "${WORK}" | pg_restore --no-owner --no-privileges --exit-on-error --dbname="${DB_DSN}"
      ;;
    *) die "unsupported DB_DRIVER: ${DB_DRIVER}" ;;
  esac
  log "scratch load complete; run application smoke checks against the scratch DB"
fi

log "OK"
