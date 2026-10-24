#!/usr/bin/env bash
# ENT-08：备份脚本、CronJob、演练 runbook 与 RPO/RTO 门禁。
# 用法: bash scripts/ci/check-backup.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> ENT-08: backup artifacts exist"
for f in scripts/backup/backup.sh scripts/backup/restore.sh scripts/backup/verify.sh \
         deploy/backup/cronjob.yaml docs/runbooks/backup-restore.md; do
  if [[ ! -f "${f}" ]]; then
    echo "ERROR: missing ${f} (AC-ENT-08-1)" >&2
    FAIL=1
  fi
done

echo "==> ENT-08: scripts shell syntax"
for f in scripts/backup/*.sh; do
  if ! bash -n "${f}"; then
    echo "ERROR: syntax error in ${f}" >&2
    FAIL=1
  fi
done

echo "==> ENT-08: backup safety rails"
if ! grep -q 'set -euo pipefail' scripts/backup/backup.sh; then
  echo "ERROR: backup.sh must use strict mode" >&2
  FAIL=1
fi
if ! grep -q 'LOCK_DIR' scripts/backup/backup.sh; then
  echo "ERROR: backup.sh must guard against concurrent runs" >&2
  FAIL=1
fi
if ! grep -q 'sha256sum' scripts/backup/backup.sh || ! grep -q 'sha256sum' scripts/backup/verify.sh; then
  echo "ERROR: backup/verify must checksum artifacts" >&2
  FAIL=1
fi
if ! grep -q 'RESTORE_CONFIRM' scripts/backup/restore.sh; then
  echo "ERROR: restore.sh must require RESTORE_CONFIRM=yes" >&2
  FAIL=1
fi
if ! grep -q 'gzip -t' scripts/backup/restore.sh || ! grep -q 'gzip -t' scripts/backup/verify.sh; then
  echo "ERROR: restore/verify must check gzip integrity" >&2
  FAIL=1
fi

echo "==> ENT-08: cronjob hardening"
CJ="deploy/backup/cronjob.yaml"
for key in 'concurrencyPolicy: Forbid' 'serviceAccountName:' 'automountServiceAccountToken: false' 'BACKUP_RETENTION_DAYS' 'backoffLimit' 'activeDeadlineSeconds'; do
  if ! grep -q "${key}" "${CJ}"; then
    echo "ERROR: ${CJ} missing ${key} (AC-ENT-08-3)" >&2
    FAIL=1
  fi
done
if grep -qE 'image: .*:latest' "${CJ}"; then
  echo "ERROR: backup image must not use latest (RELS-01)" >&2
  FAIL=1
fi

echo "==> ENT-08: runbook must document RPO/RTO and drill"
RB="docs/runbooks/backup-restore.md"
if ! grep -q 'RPO' "${RB}" || ! grep -q 'RTO' "${RB}"; then
  echo "ERROR: ${RB} must document RPO/RTO (AC-ENT-08-2)" >&2
  FAIL=1
fi
if ! grep -q '演练' "${RB}"; then
  echo "ERROR: ${RB} must document restore drills (AC-ENT-08-4)" >&2
  FAIL=1
fi
if ! grep -q '版本化' "${RB}"; then
  echo "ERROR: ${RB} must document object storage versioning (T6)" >&2
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "backup check failed (ENT-08)" >&2
  exit 1
fi
echo "OK: backup"
