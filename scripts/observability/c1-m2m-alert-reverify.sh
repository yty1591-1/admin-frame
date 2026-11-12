#!/usr/bin/env bash
# C1 复证：M2M 签发告警阈值与 reason 分布（staging 真实流量）。
#
# 背景：S6-1 安全评审「有条件通过」的 C1——本地 kind 已实测
# identity.m2m.denied_spike 可 firing、identity.m2m.latency 不误报；
# staging 需在真实流量下复核阈值与 reason 分布，避免本地低流量样本外推。
# 评审记录：docs/archive/reviews/s6-1-security-review-record-2026-11-10.md §7
#
# 本脚本只做「造量 + 观测 + 出证据」，**不修改任何告警规则**：
#   1. 基线：用合法凭据签发一批令牌，确认 issued 计数与 P95 有样本；
#   2. 造量：每轮 2 次 invalid_client + 2 次 invalid_scope，持续 duration；
#   3. 观测：轮询 /api/v1/alerts 与 reason 分布，等待 denied_spike 进入 firing，
#      同时确认 latency 全程 inactive、P95 低于 100ms；
#   4. 恢复：停止造量，确认 denied_spike 回落到 inactive；
#   5. 输出 Markdown 证据块，可直接粘进评审记录 §7。
#
# 判定（全部满足才算 PASS）：
#   - denied_spike 在窗口内出现 firing；
#   - latency 在窗口内从未 firing；
#   - reason 分布同时包含 invalid_client 与 invalid_scope，且 issued 有样本；
#   - 停止造量后 denied_spike 在 recovery-wait 内回到 inactive。
#
# 用法（需 bash + curl + python3）：
#   export C1_CONFIRM=yes                      # 必须显式确认，避免误打生产
#   bash scripts/observability/c1-m2m-alert-reverify.sh \
#     --api-base https://admin.staging.example.com \
#     --prom-url https://prom.staging.example.com \
#     --admin-subject ops@example.com --admin-password '***' \
#     --create-client --scope identity:m2m:read \
#     --duration 720 --interval 20 --recovery-wait 330
#
# 复用既有客户端（不创建、不删除）：
#   ... --client-id svc_xxx --client-secret '***' --scope <该客户端 allowed_scopes 之一>
#
# 可选：--out-dir /tmp/c1 会把证据 Markdown 落盘（路径用正斜杠，避免 Windows 下反斜杠被转义）。
#
# 退出码：0=PASS；2=未达判定；3=参数 / 预检失败。
set -uo pipefail

API_BASE=""
PROM_URL=""
PROM_AUTH=""
PROM_TOKEN=""
ADMIN_TOKEN=""
ADMIN_SUBJECT=""
ADMIN_PASSWORD=""
CLIENT_ID=""
CLIENT_SECRET=""
SCOPE=""
DURATION=720
INTERVAL=20
RECOVERY_WAIT=330
POLL=15
CREATE_CLIENT=0
DELETE_CLIENT=0
CONFIRM="${C1_CONFIRM:-}"
INVALID_SCOPE="c1:not:allowed"
OUT_DIR="${C1_OUT_DIR:-}"

usage() {
  sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
}

need_value() {
  if [[ $# -lt 2 || -z "$2" ]]; then
    echo "ERROR: $1 需要参数值" >&2
    exit 3
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --api-base) need_value "$@"; API_BASE="$2"; shift 2;;
    --prom-url) need_value "$@"; PROM_URL="$2"; shift 2;;
    --prom-auth) need_value "$@"; PROM_AUTH="$2"; shift 2;;
    --prom-token) need_value "$@"; PROM_TOKEN="$2"; shift 2;;
    --admin-token) need_value "$@"; ADMIN_TOKEN="$2"; shift 2;;
    --admin-subject) need_value "$@"; ADMIN_SUBJECT="$2"; shift 2;;
    --admin-password) need_value "$@"; ADMIN_PASSWORD="$2"; shift 2;;
    --client-id) need_value "$@"; CLIENT_ID="$2"; shift 2;;
    --client-secret) need_value "$@"; CLIENT_SECRET="$2"; shift 2;;
    --scope) need_value "$@"; SCOPE="$2"; shift 2;;
    --duration) need_value "$@"; DURATION="$2"; shift 2;;
    --interval) need_value "$@"; INTERVAL="$2"; shift 2;;
    --recovery-wait) need_value "$@"; RECOVERY_WAIT="$2"; shift 2;;
    --out-dir) need_value "$@"; OUT_DIR="$2"; shift 2;;
    --create-client) CREATE_CLIENT=1; shift;;
    --delete-client) DELETE_CLIENT=1; shift;;
    --yes) CONFIRM=yes; shift;;
    -h|--help) usage; exit 0;;
    *) echo "ERROR: 未知参数 $1" >&2; usage >&2; exit 3;;
  esac
done

log() { echo "[$(date -u +%H:%M:%S)] $*"; }
fail() { echo "FAIL: $*" >&2; }

[[ -n "$API_BASE" ]] || { echo "ERROR: --api-base 必填" >&2; exit 3; }
[[ -n "$PROM_URL" ]] || { echo "ERROR: --prom-url 必填" >&2; exit 3; }
[[ -n "$SCOPE" ]] || { echo "ERROR: --scope 必填（需在该客户端 allowed_scopes 内）" >&2; exit 3; }
API_BASE="${API_BASE%/}"
PROM_URL="${PROM_URL%/}"

if [[ "$CONFIRM" != "yes" ]]; then
  cat >&2 <<EOF
ERROR: 该脚本会向目标环境持续制造认证失败流量（默认 ${DURATION}s）。
请先确认 --api-base 指向 staging：${API_BASE}
确认后加 --yes 或设置 C1_CONFIRM=yes 重新执行。
EOF
  exit 3
fi

WORK_DIR="$(mktemp -d)"
REPORT_MD="${OUT_DIR:+${OUT_DIR}/}c1-m2m-reverify-$(date -u +%Y%m%dT%H%M%SZ).md"
if [[ -n "$OUT_DIR" ]]; then
  mkdir -p "$OUT_DIR"
  REPORT_MD="${OUT_DIR}/c1-m2m-reverify-$(date -u +%Y%m%dT%H%M%SZ).md"
else
  REPORT_MD=""
fi
CREATED_CLIENT=0

cleanup() {
  rm -rf "$WORK_DIR"
  if [[ "$CREATED_CLIENT" == "1" && "$DELETE_CLIENT" == "1" && -n "$CLIENT_ID" ]]; then
    log "清理：删除临时 M2M 客户端 ${CLIENT_ID}"
    api_request DELETE "/admin/v1/auth/m2m/clients/${CLIENT_ID}" "" "Idempotency-Key: $(uuid)" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

uuid() {
  if command -v uuidgen >/dev/null 2>&1; then
    uuidgen | tr 'A-Z' 'a-z'
  else
    python3 -c 'import uuid;print(uuid.uuid4())'
  fi
}

# ---- Prometheus ---------------------------------------------------------------

prom_query() {
  local q="$1"
  local args=(-sG --data-urlencode "query=${q}")
  [[ -n "$PROM_AUTH" ]] && args+=(-u "$PROM_AUTH")
  [[ -n "$PROM_TOKEN" ]] && args+=(-H "Authorization: Bearer ${PROM_TOKEN}")
  curl "${args[@]}" "${PROM_URL}/api/v1/query"
}

prom_alerts() {
  local args=(-s)
  [[ -n "$PROM_AUTH" ]] && args+=(-u "$PROM_AUTH")
  [[ -n "$PROM_TOKEN" ]] && args+=(-H "Authorization: Bearer ${PROM_TOKEN}")
  curl "${args[@]}" "${PROM_URL}/api/v1/alerts"
}

# json_get <file> <python 表达式>
json_get() {
  python3 - "$1" "$2" <<'PY'
import json, sys
raw = open(sys.argv[1], encoding="utf-8").read()
try:
    doc = json.loads(raw)
except Exception:
    print("")
    raise SystemExit(0)
try:
    value = eval(sys.argv[2], {"d": doc, "x": doc})
except Exception:
    value = ""
if value is None:
    value = ""
print(value)
PY
}

alert_state_of() {
  local name="$1"
  local file="$WORK_DIR/alerts-${name}.json"
  prom_alerts >"$file"
  python3 - "$file" "$name" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    print("")
    raise SystemExit(0)
name = sys.argv[2]
for alert in doc.get("data", {}).get("alerts", []):
    if alert.get("labels", {}).get("alertname") == name:
        print(alert.get("state", ""))
        raise SystemExit(0)
print("inactive")
PY
}

reason_distribution() {
  prom_query 'sum by (result, reason) (increase(identity_m2m_token_total[5m]))' >"$WORK_DIR/reasons.json"
  python3 - "$WORK_DIR/reasons.json" <<'PY'
import json, sys
try:
    doc = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    print("")
    raise SystemExit(0)
rows = []
for item in doc.get("data", {}).get("result", []):
    labels = item.get("metric", {})
    value = float(item.get("value", [0, "0"])[1])
    if value <= 0:
        continue
    reason = labels.get("reason", "") or "-"
    rows.append(f'{labels.get("result","?")}/{reason}={value:.2f}')
print(", ".join(sorted(rows)))
PY
}

p95_issued() {
  prom_query 'histogram_quantile(0.95, sum by (le) (rate(identity_m2m_token_duration_seconds_bucket{result="issued"}[5m])))' >"$WORK_DIR/p95.json"
  json_get "$WORK_DIR/p95.json" '(d.get("data",{}).get("result") or [{}])[0].get("value",[0,"NaN"])[1]'
}

issued_samples() {
  prom_query 'sum(increase(identity_m2m_token_duration_seconds_count{result="issued"}[5m]))' >"$WORK_DIR/samples.json"
  json_get "$WORK_DIR/samples.json" 'float((d.get("data",{}).get("result") or [{"value":[0,"0"]}])[0].get("value",[0,"0"])[1])' \
    | awk '{printf "%.1f", $1}'
}

# ---- 业务 API ----------------------------------------------------------------

api_request() {
  local method="$1" path="$2" body="${3:-}" header="${4:-}"
  local args=(-s -o "$WORK_DIR/resp.json" -w '%{http_code}' -X "$method")
  [[ -n "$header" ]] && args+=(-H "$header")
  [[ -n "${ADMIN_TOKEN}" ]] && args+=(-H "Authorization: Bearer ${ADMIN_TOKEN}")
  if [[ -n "$body" ]]; then
    args+=(-H 'Content-Type: application/json' --data-binary "$body")
  fi
  curl "${args[@]}" "${API_BASE}${path}"
}

login() {
  local payload
  payload=$(python3 -c 'import json,sys;print(json.dumps({"subject":sys.argv[1],"password":sys.argv[2]}))' \
    "$ADMIN_SUBJECT" "$ADMIN_PASSWORD")
  local code
  code=$(api_request POST /admin/v1/auth/login "$payload")
  if [[ "$code" != "200" ]]; then
    echo "ERROR: 管理员登录失败 HTTP=${code}: $(head -c 300 "$WORK_DIR/resp.json")" >&2
    exit 3
  fi
  ADMIN_TOKEN=$(json_get "$WORK_DIR/resp.json" 'd.get("data",{}).get("access_token","")')
  if [[ -z "$ADMIN_TOKEN" ]]; then
    echo "ERROR: 登录未返回 access_token（可能要求 MFA / 强制改密，请改用 --admin-token）" >&2
    exit 3
  fi
  log "管理员登录成功（token 长度 ${#ADMIN_TOKEN}）"
}

ensure_client() {
  if [[ -n "$CLIENT_ID" && -n "$CLIENT_SECRET" ]]; then
    log "复用既有客户端 ${CLIENT_ID}"
    return
  fi
  if [[ "$CREATE_CLIENT" != "1" ]]; then
    echo "ERROR: 必须提供 --client-id/--client-secret，或加 --create-client 由脚本创建临时客户端" >&2
    exit 3
  fi
  [[ -n "$ADMIN_TOKEN" ]] || login
  local payload code client_json
  payload=$(python3 -c 'import json,sys,time;print(json.dumps({"name":"c1-reverify-"+time.strftime("%Y%m%d%H%M%S",time.gmtime()),"owner":"sre","allowed_channels":["admin"],"allowed_scopes":[sys.argv[1]]}))' "$SCOPE")
  code=$(api_request POST /admin/v1/auth/m2m/clients "$payload" "Idempotency-Key: $(uuid)")
  if [[ "$code" != "200" ]]; then
    echo "ERROR: 创建 M2M 客户端失败 HTTP=${code}: $(head -c 300 "$WORK_DIR/resp.json")" >&2
    exit 3
  fi
  client_json="$WORK_DIR/client.json"
  cp "$WORK_DIR/resp.json" "$client_json"
  CLIENT_ID=$(json_get "$client_json" 'd.get("data",{}).get("client",{}).get("client_id","")')
  CREATED_CLIENT=1
  if [[ -z "$CLIENT_ID" ]]; then
    echo "ERROR: 创建响应缺少 data.client.client_id: $(head -c 300 "$client_json")" >&2
    exit 3
  fi
  log "已创建临时客户端 ${CLIENT_ID}"
  # 创建响应的 client_secret 只出现一次；缺失时走轮换再取一次。
  CLIENT_SECRET=$(json_get "$client_json" 'd.get("data",{}).get("client_secret","")')
  if [[ -z "$CLIENT_SECRET" ]]; then
    rotate_secret
  fi
}

rotate_secret() {
  local code
  code=$(api_request POST "/admin/v1/auth/m2m/clients/${CLIENT_ID}/secrets" "" "Idempotency-Key: $(uuid)")
  if [[ "$code" != "200" ]]; then
    echo "ERROR: 轮换 secret 失败 HTTP=${code}: $(head -c 300 "$WORK_DIR/resp.json")" >&2
    exit 3
  fi
  CLIENT_SECRET=$(json_get "$WORK_DIR/resp.json" 'd.get("data",{}).get("client_secret","")')
  [[ -n "$CLIENT_SECRET" ]] || { echo "ERROR: 轮换响应缺少 client_secret" >&2; exit 3; }
  log "已轮换 secret（fingerprint=$(json_get "$WORK_DIR/resp.json" 'd.get("data",{}).get("fingerprint","")')）"
}

# mint <secret> <scope> -> 打印 HTTP 状态码；-u 走 Basic 认证。
mint() {
  local secret="$1" scope="$2"
  curl -s -o "$WORK_DIR/mint.json" -w '%{http_code}' \
    -u "${CLIENT_ID}:${secret}" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data-urlencode 'grant_type=client_credentials' \
    --data-urlencode "scope=${scope}" \
    "${API_BASE}/admin/v1/auth/token"
}

# ---- 预检 --------------------------------------------------------------------

preflight() {
  local code p95
  code=$(curl -s -o /dev/null -w '%{http_code}' "${PROM_URL}/-/ready" || true)
  if [[ "$code" != "200" ]]; then
    echo "ERROR: Prometheus 不可用（${PROM_URL}/-/ready => ${code}）" >&2
    exit 3
  fi
  code=$(curl -s -o "$WORK_DIR/preflight.json" -w '%{http_code}' -X POST \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data 'grant_type=client_credentials&scope=probe' \
    "${API_BASE}/admin/v1/auth/token" || true)
  case "$code" in
    400|401) log "预检：M2M 签发端点已注册（未认证请求 => HTTP ${code}）";;
    404) echo "ERROR: ${API_BASE}/admin/v1/auth/token 返回 404，确认该环境已启用 M2M（enabled=true）" >&2; exit 3;;
    *) echo "ERROR: M2M 端点预检异常 HTTP=${code}" >&2; exit 3;;
  esac
  code=$(mint "$CLIENT_SECRET" "$SCOPE")
  if [[ "$code" != "200" ]]; then
    echo "ERROR: 合法凭据签发失败 HTTP=${code}: $(head -c 300 "$WORK_DIR/mint.json")" >&2
    echo "       检查 --scope 是否在该客户端 allowed_scopes 内、客户端是否已启用" >&2
    exit 3
  fi
  log "预检：合法凭据签发成功"
  p95=$(p95_issued)
  log "预检：当前 issued P95=${p95}（基线，供对比）"
}

# ---- 主流程 ------------------------------------------------------------------

START_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
INITIAL_DENIED=""
INITIAL_LATENCY=""
DENIED_FIRING_SEEN=0
LATENCY_FIRING_SEEN=0
MAX_P95=""
RATE_LIMITED=0
ISSUED_OK=0
DENIED_SENT=0
LAST_REASONS=""
FIRST_DENIED_AT=""
FIRST_FIRING_AT=""
RECOVER_AT=""
STATUS="FAIL"

run_traffic() {
  local deadline=$((SECONDS + DURATION)) valid_code code
  while (( SECONDS < deadline )); do
    # 每轮 1 次合法签发（保证 latency 有分母）+ 2 次 invalid_client + 2 次 invalid_scope。
    valid_code=$(mint "$CLIENT_SECRET" "$SCOPE")
    [[ "$valid_code" == "200" ]] && ISSUED_OK=$((ISSUED_OK + 1))
    [[ "$valid_code" == "429" ]] && RATE_LIMITED=$((RATE_LIMITED + 1))

    code=$(mint "c1-invalid-secret" "$SCOPE")
    [[ "$code" == "401" ]] && DENIED_SENT=$((DENIED_SENT + 1))
    [[ "$code" == "429" ]] && RATE_LIMITED=$((RATE_LIMITED + 1))
    code=$(mint "c1-invalid-secret" "$SCOPE")
    [[ "$code" == "401" ]] && DENIED_SENT=$((DENIED_SENT + 1))
    [[ "$code" == "429" ]] && RATE_LIMITED=$((RATE_LIMITED + 1))

    code=$(mint "$CLIENT_SECRET" "$INVALID_SCOPE")
    [[ "$code" == "400" ]] && DENIED_SENT=$((DENIED_SENT + 1))
    [[ "$code" == "429" ]] && RATE_LIMITED=$((RATE_LIMITED + 1))
    code=$(mint "$CLIENT_SECRET" "$INVALID_SCOPE")
    [[ "$code" == "400" ]] && DENIED_SENT=$((DENIED_SENT + 1))
    [[ "$code" == "429" ]] && RATE_LIMITED=$((RATE_LIMITED + 1))

    LAST_REASONS=$(reason_distribution)
    [[ -z "$FIRST_DENIED_AT" && -n "$LAST_REASONS" ]] && FIRST_DENIED_AT=$(date -u +%H:%M:%SZ)

    local denied latency p95 samples
    denied=$(alert_state_of IdentityM2MTokenDeniedSpike)
    latency=$(alert_state_of IdentityM2MTokenLatencyP95High)
    p95=$(p95_issued)
    samples=$(issued_samples)
    [[ "$p95" != "NaN" && -n "$p95" ]] && MAX_P95="$p95"
    log "denied=${denied} latency=${latency} p95=${p95} samples=${samples} | ${LAST_REASONS}"

    if [[ "$denied" == "firing" ]]; then
      DENIED_FIRING_SEEN=1
      [[ -z "$FIRST_FIRING_AT" ]] && FIRST_FIRING_AT=$(date -u +%H:%M:%SZ)
    fi
    [[ "$latency" == "firing" ]] && LATENCY_FIRING_SEEN=1

    # 目标已达成（firing 已观测且 latency 未误报）可提前结束造量。
    if [[ "$DENIED_FIRING_SEEN" == "1" && "$LATENCY_FIRING_SEEN" == "0" ]]; then
      log "已观测到 denied_spike firing，提前结束造量"
      break
    fi
    sleep "$INTERVAL"
  done
}

wait_recovery() {
  local deadline=$((SECONDS + RECOVERY_WAIT)) state
  while (( SECONDS < deadline )); do
    state=$(alert_state_of IdentityM2MTokenDeniedSpike)
    log "恢复观测：denied_spike=${state}"
    if [[ "$state" == "inactive" ]]; then
      RECOVER_AT=$(date -u +%H:%M:%SZ)
      return 0
    fi
    sleep "$POLL"
  done
  return 1
}

write_report() {
  local end_at p95_now samples_now reasons_now latency_cell
  end_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  p95_now=$(p95_issued)
  samples_now=$(issued_samples)
  reasons_now=$(reason_distribution)
  if [[ "$LATENCY_FIRING_SEEN" == "1" ]]; then
    latency_cell="出现 firing（不达标，需排查）"
  else
    latency_cell="全程 inactive"
  fi
  {
    echo ""
    echo "<!-- C1 复证证据（脚本生成，粘贴到 s6-1 评审记录 §7） -->"
    echo "**C1 staging 复证**（${START_AT} ~ ${end_at}）"
    echo ""
    echo "| 项目 | 实测 |"
    echo "|---|---|"
    echo "| 目标环境 | \`${API_BASE}\` |"
    echo "| 造量时长 / 间隔 | ${DURATION}s / ${INTERVAL}s（已发送 ${DENIED_SENT} 次预期拒绝） |"
    echo "| 造量前初始状态 | denied_spike=${INITIAL_DENIED:-unknown}，latency=${INITIAL_LATENCY:-unknown} |"
    echo "| reason 分布（5m increase） | ${reasons_now:-无样本} |"
    echo "| \`IdentityM2MTokenDeniedSpike\` | 首次观测 firing：${FIRST_FIRING_AT:-未出现} |"
    echo "| \`IdentityM2MTokenLatencyP95High\` | ${latency_cell} |"
    echo "| issued P95（窗口内最大值） | ${MAX_P95:-无样本} s（阈值 0.1s） |"
    echo "| issued 样本数（5m） | ${samples_now:-0} |"
    echo "| 限流（HTTP 429）次数 | ${RATE_LIMITED} |"
    echo "| 恢复：denied_spike 回到 inactive | ${RECOVER_AT:-未在 ${RECOVERY_WAIT}s 内恢复} |"
    echo "| 结论 | **${STATUS}** |"
    echo ""
  } >"$WORK_DIR/report.md"
  cat "$WORK_DIR/report.md"
  if [[ -n "$REPORT_MD" ]]; then
    cp "$WORK_DIR/report.md" "$REPORT_MD"
    log "证据已写入 ${REPORT_MD}"
  fi
}

main() {
  log "C1 复证开始：api=${API_BASE} prom=${PROM_URL}"
  ensure_client
  preflight
  INITIAL_DENIED=$(alert_state_of IdentityM2MTokenDeniedSpike)
  INITIAL_LATENCY=$(alert_state_of IdentityM2MTokenLatencyP95High)
  log "初始告警状态：denied_spike=${INITIAL_DENIED:-unknown} latency=${INITIAL_LATENCY:-unknown}"
  run_traffic
  if wait_recovery; then
    :
  else
    log "WARN: denied_spike 未在 ${RECOVERY_WAIT}s 内恢复 inactive"
  fi

  if [[ "$DENIED_FIRING_SEEN" == "1" && "$LATENCY_FIRING_SEEN" == "0" && -n "$RECOVER_AT" ]]; then
    STATUS="PASS"
  fi
  write_report

  if [[ "$STATUS" == "PASS" ]]; then
    log "PASS：denied_spike firing 已复现、latency 未误报、恢复后告警消除"
    exit 0
  fi
  fail "未满足全部判定（denied_firing=${DENIED_FIRING_SEEN} latency_firing=${LATENCY_FIRING_SEEN} recovered=${RECOVER_AT:-no}）"
  exit 2
}

main
