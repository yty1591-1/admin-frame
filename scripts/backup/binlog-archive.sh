#!/usr/bin/env bash
# MySQL binlog 归档（ENT-08 G1）：把实例上的二进制日志搬运到对象存储，支撑 RPO ≤ 5 分钟。
#
# 为什么需要：全量备份每日一次，若 binlog 只留在实例本地，实例或 PVC 损坏时
# binlog 随之丢失，实际 RPO 退化为 24 小时。归档后可用「最近全量 + 归档 binlog」
# 恢复到故障前任意时间点，详见 docs/runbooks/backup-restore.md。
#
# 用法:
#   DB_DRIVER=mysql DB_DSN='user:pass@tcp(host:3306)/db' \
#   BINLOG_S3_URI='s3://bucket/prod/cluster-a/' \
#     bash scripts/backup/binlog-archive.sh
#
# 环境变量:
#   DB_DRIVER           固定 mysql（本脚本只处理 MySQL binlog）
#   DB_DSN              连接串，支持 mysql:// 与项目 Go 格式（见 dsn.sh）
#   BINLOG_S3_URI       归档前缀（必填）；**必须按实例/集群隔离**，详见下方说明
#   BINLOG_WORK_DIR     本地临时目录（默认 /var/backups/binlog）
#   BINLOG_FLUSH        1=每次归档前 FLUSH BINARY LOGS 封口当前文件（默认 1）
#   BINLOG_MAX_FILES    单次最多归档文件数（默认 200），防止首次运行拉取过多
#
# 权限要求:
#   - `REPLICATION SLAVE`（MySQL 8.0.22+ 可用 `BINLOG MONITOR`）读取 binlog；
#   - `RELOAD` 执行 FLUSH BINARY LOGS（BINLOG_FLUSH=1 时必须）。
#
# 命名与幂等:
#   对象键为 `${BINLOG_S3_URI}/${binlog 文件名}`。同名对象已存在即跳过；
#   若已存在但大小不同则报错退出（说明前缀被复用或对象损坏），不静默覆盖。
#   **实例重建后 binlog 编号会从 000001 重新开始**，因此前缀必须包含实例或集群
#   标识（如 `prod/cluster-a/`），否则会覆盖历史归档、破坏可恢复性。
set -euo pipefail

log() { printf '%s [binlog-archive] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=dsn.sh
source "${SCRIPT_DIR}/dsn.sh"

DB_DRIVER="${DB_DRIVER:-mysql}"
DB_DSN="${DB_DSN:-}"
BINLOG_S3_URI="${BINLOG_S3_URI:-}"
BINLOG_WORK_DIR="${BINLOG_WORK_DIR:-/var/backups/binlog}"
BINLOG_FLUSH="${BINLOG_FLUSH:-1}"
BINLOG_MAX_FILES="${BINLOG_MAX_FILES:-200}"

[[ "${DB_DRIVER}" == "mysql" ]] || die "DB_DRIVER must be mysql (got ${DB_DRIVER})"
[[ -n "${DB_DSN}" ]] || die "DB_DSN is required"
[[ -n "${BINLOG_S3_URI}" ]] || die "BINLOG_S3_URI is required (e.g. s3://bucket/prod/cluster-a/)"
command -v mysqlbinlog >/dev/null 2>&1 || die "mysqlbinlog not found (use deploy/backup/Dockerfile)"
command -v aws >/dev/null 2>&1 || die "aws cli not found (use deploy/backup/Dockerfile)"

parse_mysql_dsn "${DB_DSN}" || die "cannot parse mysql DSN; expect mysql://... or user:pass@tcp(host:port)/db"

CONN=()
mysql_conn_args CONN

mysql_exec() {
  mysql "${CONN[@]}" -N -e "$1" "${DB_NAME}"
}

log_bin_state="$(mysql_exec "SHOW VARIABLES LIKE 'log_bin';" | awk '{print $2}')"
[[ "${log_bin_state}" == "ON" ]] || die "log_bin is ${log_bin_state:-unknown}; enable binary logging before archiving"

mkdir -p "${BINLOG_WORK_DIR}"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}" 2>/dev/null || true' EXIT

URI="${BINLOG_S3_URI%/}"

# 1) 封口当前 binlog，使最新文件可被完整拉取。
#
# 不封口时最后一个文件仍在写入，--raw 拉到的副本可能被截断，且下次运行前
# 它一直是「不完整」状态。代价是每个执行周期产生一个 binlog 文件（5 分钟
# 间隔约 288 个/天），实例侧需用 binlog_expire_logs_seconds 控制数量。
if [[ "${BINLOG_FLUSH}" == "1" ]]; then
  log "flushing binary logs"
  mysql_exec "FLUSH BINARY LOGS;" >/dev/null
fi

# 2) 列出实例上的 binlog（Log_name、File_size）。
LIST="$(mysql_exec "SHOW BINARY LOGS;" | awk '$1 ~ /^[A-Za-z0-9._-]+$/ {print $1" "$2}')"
[[ -n "${LIST}" ]] || die "no binary logs reported by SHOW BINARY LOGS"

# 最后一个文件仍在写入，**不能归档**：先归档再让它继续增长，下次运行会因
# 「同名对象已存在」而跳过，归档副本就永久缺失后续写入（静默数据丢失）。
# 因此每次归档的都是已封口的文件；封口由上面的 FLUSH BINARY LOGS 完成。
ACTIVE_LOG="$(printf '%s\n' "${LIST}" | tail -1 | awk '{print $1}')"

# 3) 已归档集合（对象键去掉 sha256 后缀）。
#
# 以对象存储为准而不是本地状态文件：CronJob 每次都是新 Pod，持久化本地状态
# 既不稳定也容易与实际不一致。
ARCHIVED="$(aws s3 ls "${URI}/" 2>/dev/null | awk '{print $4}' | grep -v '\.sha256$' || true)"

archived_count=0
skipped_count=0
failed_count=0
total_bytes=0

while read -r name size; do
  [[ -n "${name}" ]] || continue

  if [[ "${name}" == "${ACTIVE_LOG}" ]]; then
    log "skip active binlog ${name} (still being written; will archive after next flush)"
    skipped_count=$((skipped_count + 1))
    continue
  fi

  if printf '%s\n' "${ARCHIVED}" | grep -qx -- "${name}"; then
    skipped_count=$((skipped_count + 1))
    continue
  fi

  if [[ "${archived_count}" -ge "${BINLOG_MAX_FILES}" ]]; then
    log "WARN: reached BINLOG_MAX_FILES=${BINLOG_MAX_FILES}; remaining logs deferred to next run"
    break
  fi

  # --raw 输出原始 binlog 副本；--result-file 以目录结尾时保留原文件名。
  if ! mysqlbinlog --read-from-remote-server --raw \
    --host="${DB_HOST}" --port="${DB_PORT}" --user="${DB_USER}" \
    --result-file="${WORK}/" "${name}" >/dev/null 2>"${WORK}/err.log"; then
    log "ERROR: failed to fetch ${name}: $(tr '\n' ' ' <"${WORK}/err.log" | cut -c1-200)"
    failed_count=$((failed_count + 1))
    continue
  fi

  LOCAL="${WORK}/${name}"
  [[ -s "${LOCAL}" ]] || die "empty binlog copy for ${name}"

  local_size="$(wc -c <"${LOCAL}" | tr -d ' ')"
  # 封口后的 binlog 大小应与 SHOW BINARY LOGS 报告一致；不一致说明拉取被截断。
  if [[ "${BINLOG_FLUSH}" == "1" && "${local_size}" != "${size}" ]]; then
    log "ERROR: size mismatch for ${name}: local=${local_size} reported=${size}"
    failed_count=$((failed_count + 1))
    continue
  fi

  # 同名对象已存在但大小不同 → 前缀被复用或对象损坏，拒绝覆盖。
  remote_size="$(aws s3 ls "${URI}/${name}" 2>/dev/null | awk '{print $3}' | head -1 || true)"
  if [[ -n "${remote_size}" && "${remote_size}" != "${local_size}" ]]; then
    log "ERROR: ${URI}/${name} exists with different size (remote=${remote_size} local=${local_size}); refusing to overwrite — check BINLOG_S3_URI instance isolation"
    failed_count=$((failed_count + 1))
    continue
  fi
  if [[ -n "${remote_size}" ]]; then
    skipped_count=$((skipped_count + 1))
    continue
  fi

  sha256sum "${LOCAL}" | sed "s#${WORK}/##" >"${LOCAL}.sha256"
  if ! aws s3 cp "${LOCAL}" "${URI}/${name}" >/dev/null; then
    log "ERROR: upload failed for ${name}"
    failed_count=$((failed_count + 1))
    continue
  fi
  if ! aws s3 cp "${LOCAL}.sha256" "${URI}/${name}.sha256" >/dev/null; then
    log "ERROR: checksum upload failed for ${name}"
    failed_count=$((failed_count + 1))
    continue
  fi

  archived_count=$((archived_count + 1))
  total_bytes=$((total_bytes + local_size))
  log "archived ${name} (${local_size} bytes)"
  rm -f "${LOCAL}" "${LOCAL}.sha256"
done <<<"${LIST}"

log "summary: archived=${archived_count} skipped=${skipped_count} failed=${failed_count} bytes=${total_bytes}"

# 失败不静默：CronJob 需要非 0 退出码来触发 backup.failed 告警。
[[ "${failed_count}" -eq 0 ]] || die "${failed_count} binlog file(s) failed to archive"

log "OK"
