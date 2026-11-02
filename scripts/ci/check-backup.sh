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

# 2026-11-02 演练发现：三个脚本原先只认 mysql:// URL，或把 DSN 直接当数据库名
# 传给 mysql CLI（退化为本地 socket 连接，ERROR 2002），导致备份/恢复在真实
# DSN 格式下不可用。此处锁定「共用 DSN 解析」这一实现，防止回归。
if [[ ! -f scripts/backup/dsn.sh ]]; then
  echo "ERROR: scripts/backup/dsn.sh missing (mysql DSN parsing shared by backup/restore/verify)" >&2
  FAIL=1
else
  if ! grep -q '@tcp(' scripts/backup/dsn.sh; then
    echo "ERROR: dsn.sh must parse the project Go-style DSN (user:pass@tcp(host:port)/db)" >&2
    FAIL=1
  fi
  for f in backup restore verify; do
    if ! grep -q 'source "${SCRIPT_DIR}/dsn.sh"' "scripts/backup/${f}.sh"; then
      echo "ERROR: scripts/backup/${f}.sh must source dsn.sh for DSN parsing" >&2
      FAIL=1
    fi
    if grep -qE 'mysql --default-character-set=utf8mb4 "\$\{DB_DSN\}"' "scripts/backup/${f}.sh"; then
      echo "ERROR: scripts/backup/${f}.sh passes DSN as a database name to mysql" >&2
      FAIL=1
    fi
  done
fi
# 加密失败必须清除明文与残片：否则「加密备份」失败会在备份目录留下完整明文全量。
if ! grep -q 'partial artifacts removed' scripts/backup/backup.sh; then
  echo "ERROR: backup.sh must remove plaintext/partial ciphertext when gpg fails" >&2
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

echo "==> ENT-08: binlog archive (G1)"
# 没有 binlog 归档时 RPO 退化为全备周期（24h），必须由制品保证而不是文档口头承诺。
if [[ ! -f scripts/backup/binlog-archive.sh ]]; then
  echo "ERROR: scripts/backup/binlog-archive.sh missing (binlog archive is required for RPO target)" >&2
  FAIL=1
else
  # 活跃 binlog 仍会增长：归档后再被同名跳过会造成静默数据缺失，必须显式跳过。
  if ! grep -q 'skip active binlog' scripts/backup/binlog-archive.sh; then
    echo "ERROR: binlog-archive.sh must skip the active (unsealed) binlog file" >&2
    FAIL=1
  fi
  if ! grep -q 'BINLOG_S3_URI' scripts/backup/binlog-archive.sh; then
    echo "ERROR: binlog-archive.sh must require BINLOG_S3_URI" >&2
    FAIL=1
  fi
fi
if ! grep -q 'name: admin-frame-binlog-archive' "${CJ}"; then
  echo "ERROR: ${CJ} must define the admin-frame-binlog-archive CronJob" >&2
  FAIL=1
fi
if ! grep -q 'BINLOG_S3_URI' "${CJ}"; then
  echo "ERROR: ${CJ} binlog archive job must set BINLOG_S3_URI" >&2
  FAIL=1
fi
# PITR 与归档都需要 mysqlbinlog；官方精简镜像不含该工具，必须由运维镜像提供。
OPS_IMAGE="deploy/backup/Dockerfile"
if [[ ! -f "${OPS_IMAGE}" ]]; then
  echo "ERROR: ${OPS_IMAGE} missing (db-ops image must ship mysqlbinlog for PITR/archive)" >&2
  FAIL=1
elif ! grep -q 'mysqlbinlog' "${OPS_IMAGE}"; then
  echo "ERROR: ${OPS_IMAGE} must install mysqlbinlog" >&2
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
