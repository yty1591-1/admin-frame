#!/usr/bin/env bash
# ENT-08：备份脚本、CronJob、演练 runbook 与 RPO/RTO 门禁。
# 用法: bash scripts/ci/check-backup.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

# code_has <file> <literal>
#   只在「非注释行」里查找，返回 0 表示代码中确实存在该字面量。
#
# 为什么不能直接 grep 整个文件：这些脚本的头部注释会说明自己用了哪些参数
# （如 `--source-data=2`），直接 grep 会匹配到注释，导致「代码已删除但门禁仍绿」。
# 证伪时发现过这个假阴性，故统一走 code_has。
code_has() {
  local file="$1" needle="$2"
  # 消费完整管道，避免 grep -q 提前退出令上游 SIGPIPE 被 pipefail 判为失败。
  grep -vE '^[[:space:]]*#' "${file}" 2>/dev/null | grep -F -- "${needle}" >/dev/null
}

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
if ! code_has scripts/backup/backup.sh 'set -euo pipefail'; then
  echo "ERROR: backup.sh must use strict mode" >&2
  FAIL=1
fi
if ! code_has scripts/backup/backup.sh 'LOCK_DIR'; then
  echo "ERROR: backup.sh must guard against concurrent runs" >&2
  FAIL=1
fi
if ! code_has scripts/backup/backup.sh 'sha256sum' || ! code_has scripts/backup/verify.sh 'sha256sum'; then
  echo "ERROR: backup/verify must checksum artifacts" >&2
  FAIL=1
fi
if ! code_has scripts/backup/restore.sh 'RESTORE_CONFIRM'; then
  echo "ERROR: restore.sh must require RESTORE_CONFIRM=yes" >&2
  FAIL=1
fi
if ! code_has scripts/backup/restore.sh 'gzip -t' || ! code_has scripts/backup/verify.sh 'gzip -t'; then
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
  if ! code_has scripts/backup/dsn.sh '@tcp('; then
    echo "ERROR: dsn.sh must parse the project Go-style DSN (user:pass@tcp(host:port)/db)" >&2
    FAIL=1
  fi
  for f in backup restore verify; do
    if ! code_has "scripts/backup/${f}.sh" 'source "${SCRIPT_DIR}/dsn.sh"'; then
      echo "ERROR: scripts/backup/${f}.sh must source dsn.sh for DSN parsing" >&2
      FAIL=1
    fi
    if code_has "scripts/backup/${f}.sh" 'mysql --default-character-set=utf8mb4 "${DB_DSN}"'; then
      echo "ERROR: scripts/backup/${f}.sh passes DSN as a database name to mysql" >&2
      FAIL=1
    fi
  done
fi
# 加密失败必须清除明文与残片：否则「加密备份」失败会在备份目录留下完整明文全量。
if ! code_has scripts/backup/backup.sh 'partial artifacts removed'; then
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
  if ! code_has scripts/backup/binlog-archive.sh 'skip active binlog'; then
    echo "ERROR: binlog-archive.sh must skip the active (unsealed) binlog file" >&2
    FAIL=1
  fi
  if ! code_has scripts/backup/binlog-archive.sh 'BINLOG_S3_URI'; then
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

echo "==> ENT-08: PITR (G2 position recording + truncating replay)"
# 全备必须记录位点：否则恢复只能停在备份瞬间，事故后写入全部丢失，
# 而这种「存在且可解压」的备份在监控里看起来是好的（静默失效）。
if ! code_has scripts/backup/backup.sh '--source-data'; then
  echo "ERROR: backup.sh must pass --source-data to mysqldump (PITR start point)" >&2
  FAIL=1
fi
if ! code_has scripts/backup/backup.sh 'write_backup_position'; then
  echo "ERROR: backup.sh must write a .pos sidecar next to the backup" >&2
  FAIL=1
fi
if ! code_has scripts/backup/backup.sh 'BACKUP_REQUIRE_POSITION'; then
  echo "ERROR: backup.sh must fail when no binlog position was recorded" >&2
  FAIL=1
fi
# 位点文件必须随备份一起上传：只上传备份而漏掉 sidecar，异地恢复同样无法前滚。
if ! code_has scripts/backup/backup.sh 'basename "${FINAL}.pos"'; then
  echo "ERROR: backup.sh must upload the .pos sidecar with the backup" >&2
  FAIL=1
fi
if ! code_has scripts/backup/restore.sh 'pitr_replay'; then
  echo "ERROR: restore.sh must implement PITR replay" >&2
  FAIL=1
fi
if ! code_has scripts/backup/restore.sh '--stop-position' || ! code_has scripts/backup/restore.sh '--start-position'; then
  echo "ERROR: restore.sh PITR must bound replay with --start-position and --stop-position" >&2
  FAIL=1
fi
# PITR_CONFIRM 必须真的挂在拒绝逻辑上，而不只是「出现过」：
# 变量定义行 `PITR_CONFIRM="${PITR_CONFIRM:-}"` 也含该字符串，只查存在会让
# 「保护被删但仍赋值」的情况假绿（证伪时验证过）。故要求同一行同时含 CONFIRM 与 die。
if ! grep -vE '^[[:space:]]*#' scripts/backup/restore.sh | awk '/PITR_CONFIRM/ && /die/ {found=1} END{exit !found}'; then
  echo "ERROR: restore.sh must refuse to replay binlog unless PITR_CONFIRM=yes" >&2
  FAIL=1
fi
# 预检必须早于「覆盖目标库」：全量恢复是破坏性操作，参数错误时不该先毁库再报错。
# 取最后一次出现的位置——函数定义在前、实际调用在后，最后一次即真正的调用点。
PRE_LINE="$(grep -nE '^[[:space:]]*pitr_precheck[[:space:]]*$' scripts/backup/restore.sh | cut -d: -f1 | tail -1 || true)"
LOAD_LINE="$(grep -nF 'gzip -dc "${WORK}" | mysql' scripts/backup/restore.sh | cut -d: -f1 | head -1 || true)"
if [[ -z "${PRE_LINE}" || -z "${LOAD_LINE}" ]]; then
  echo "ERROR: cannot locate pitr_precheck or the mysql load step in restore.sh" >&2
  FAIL=1
elif [[ "${PRE_LINE}" -ge "${LOAD_LINE}" ]]; then
  echo "ERROR: pitr_precheck must run BEFORE the destructive full restore (line ${PRE_LINE} vs ${LOAD_LINE})" >&2
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
# PITR 的两种截断方式与权限前提必须写在 runbook 里，否则事故时无人能照做。
if ! grep -q 'PITR_STOP_POS' "${RB}" || ! grep -q 'PITR_STOP_DATETIME' "${RB}"; then
  echo "ERROR: ${RB} must document PITR_STOP_POS / PITR_STOP_DATETIME usage" >&2
  FAIL=1
fi
if ! grep -q 'REPLICATION CLIENT' "${RB}"; then
  echo "ERROR: ${RB} must list REPLICATION CLIENT in the backup account privileges" >&2
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "backup check failed (ENT-08)" >&2
  exit 1
fi
echo "OK: backup"
