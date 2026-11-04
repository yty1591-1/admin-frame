#!/usr/bin/env bash
# ENT-17 / RELS-11：金丝雀放量前的指标门禁。
#
# 判定三件事，任何一项不达标即拒绝放量（由 release.yml 触发回滚）：
#   1. 金丝雀**确实在承接流量**（请求速率下限）——否则「零错误」只是没流量的假绿；
#   2. 5xx 比例 ≤ 阈值；
#   3. p95 时延 ≤ 阈值。
#
# 只依赖 curl + awk：发布路径要能在跳板机上跑。
#
# 环境变量：
#   PROM_URL               Prometheus 基址（必填，如 http://prom:9090）
#   PROM_TOKEN             可选 Bearer Token
#   CANARY_WINDOW          统计窗口（PromQL range，默认 5m）
#   CANARY_MIN_RPS         最小请求速率，默认 0.5（低于视为「未接流量」）
#   CANARY_MAX_ERROR_RATE  5xx 比例上限，默认 0.01
#   CANARY_MAX_P95_MS      p95 上限（毫秒），默认 500
#   CANARY_EXTRA_SELECTOR  追加的标签选择器（如 job="admin-frame"），默认空
#   DRY_RUN=true           仅打印计划（本地演练用）
set -euo pipefail

PROM_URL="${PROM_URL:-}"
PROM_TOKEN="${PROM_TOKEN:-}"
WINDOW="${CANARY_WINDOW:-5m}"
MIN_RPS="${CANARY_MIN_RPS:-0.5}"
MAX_ERR="${CANARY_MAX_ERROR_RATE:-0.01}"
MAX_P95_MS="${CANARY_MAX_P95_MS:-500}"
EXTRA="${CANARY_EXTRA_SELECTOR:-}"
DRY_RUN="${DRY_RUN:-false}"

if [[ "${DRY_RUN}" == "true" ]]; then
  echo "DRY_RUN: would query ${PROM_URL:-<PROM_URL unset>} window=${WINDOW}"
  echo "DRY_RUN: thresholds min_rps=${MIN_RPS} max_error_rate=${MAX_ERR} max_p95_ms=${MAX_P95_MS}"
  exit 0
fi

if [[ -z "${PROM_URL}" ]]; then
  echo "ERROR: PROM_URL is required (canary gate must not pass without metrics)" >&2
  exit 1
fi
PROM_URL="${PROM_URL%/}"

FAIL=0
fail() {
  echo "ERROR: $*" >&2
  FAIL=1
}

# prom_query <promql> → 打印标量值（失败打印空串）
prom_query() {
  local query="$1" out
  if [[ -n "${PROM_TOKEN}" ]]; then
    out="$(curl -sS --max-time 10 -G \
      -H "Authorization: Bearer ${PROM_TOKEN}" \
      --data-urlencode "query=${query}" \
      "${PROM_URL}/api/v1/query" 2>&1 || true)"
  else
    out="$(curl -sS --max-time 10 -G \
      --data-urlencode "query=${query}" \
      "${PROM_URL}/api/v1/query" 2>&1 || true)"
  fi
  if ! grep -qE '"status"[[:space:]]*:[[:space:]]*"success"' <<<"${out}"; then
    echo "      prometheus response: ${out}" >&2
    return 1
  fi
  # Prometheus 的 JSON 可能带空格（不同版本 / 网关重排），正则必须容忍空白。
  printf '%s' "${out}" |
    sed -nE 's/.*"value"[[:space:]]*:[[:space:]]*\[[0-9.]+,[[:space:]]*"([^"]+)"[[:space:]]*\].*/\1/p' |
    head -1
}

# le <a> <b>：浮点比较（awk，避免依赖 bc）。
le() {
  awk -v a="$1" -v b="$2" 'BEGIN{exit !(a <= b)}'
}
ge() {
  awk -v a="$1" -v b="$2" 'BEGIN{exit !(a >= b)}'
}

echo "==> canary gate: ${PROM_URL} window=${WINDOW}"

# 1) 流量下限
# 选择器必须拼进花括号内部；有/无 EXTRA 各拼一次，保证 PromQL 合法。
if [[ -n "${EXTRA}" ]]; then
  rps_query="sum(rate(http_server_requests_total{${EXTRA}}[${WINDOW}]))"
else
  rps_query="sum(rate(http_server_requests_total[${WINDOW}]))"
fi
rps="$(prom_query "${rps_query}" || true)"
if [[ -z "${rps}" ]]; then
  fail "cannot read request rate (query failed)"
else
  printf '    request rate: %.3f rps (min %.3f)\n' "${rps}" "${MIN_RPS}"
  if ! ge "${rps}" "${MIN_RPS}"; then
    fail "canary is not receiving traffic (${rps} rps < ${MIN_RPS}); metrics would be green by absence of traffic"
  fi
fi

# 2) 5xx 比例
if [[ -n "${EXTRA}" ]]; then
  err_query="sum(rate(http_server_requests_total{code=~\"5..\",${EXTRA}}[${WINDOW}])) / clamp_min(sum(rate(http_server_requests_total{${EXTRA}}[${WINDOW}])), 0.001)"
else
  err_query="sum(rate(http_server_requests_total{code=~\"5..\"}[${WINDOW}])) / clamp_min(sum(rate(http_server_requests_total[${WINDOW}])), 0.001)"
fi
err_rate="$(prom_query "${err_query}" || true)"
if [[ -z "${err_rate}" ]]; then
  fail "cannot read 5xx ratio (query failed)"
else
  printf '    5xx ratio: %.6f (max %.4f)\n' "${err_rate}" "${MAX_ERR}"
  if ! le "${err_rate}" "${MAX_ERR}"; then
    fail "5xx ratio ${err_rate} exceeds ${MAX_ERR}"
  fi
fi

# 3) p95 时延
if [[ -n "${EXTRA}" ]]; then
  p95_query="histogram_quantile(0.95, sum(rate(http_server_request_duration_seconds_bucket{${EXTRA}}[${WINDOW}])) by (le))"
else
  p95_query="histogram_quantile(0.95, sum(rate(http_server_request_duration_seconds_bucket[${WINDOW}])) by (le))"
fi
p95="$(prom_query "${p95_query}" || true)"
if [[ -z "${p95}" ]]; then
  fail "cannot read p95 latency (query failed)"
else
  p95_ms="$(awk -v v="${p95}" 'BEGIN{printf "%.1f", v*1000}')"
  printf '    p95 latency: %s ms (max %s)\n' "${p95_ms}" "${MAX_P95_MS}"
  if ! le "${p95_ms}" "${MAX_P95_MS}"; then
    fail "p95 ${p95_ms}ms exceeds ${MAX_P95_MS}ms"
  fi
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "canary gate failed (ENT-17 / RELS-11): keeping canary only, production traffic unchanged" >&2
  exit 1
fi
echo "OK: canary gate"
