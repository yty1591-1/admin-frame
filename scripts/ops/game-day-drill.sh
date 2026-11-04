#!/usr/bin/env bash
# Game Day 剧本离线自检（ENT-18）。
#
# 演练前先跑本脚本：确认剧本依赖的告警、runbook 命令、路由确实存在，
# 避免「演练当天才发现 runbook 里写的命令早已改名」。
#
# 用法:
#   bash scripts/ops/game-day-drill.sh --scenario all
#   bash scripts/ops/game-day-drill.sh --scenario g1 --json --artifact docs/incident/drills/artifacts/2026-11-05-g1.json
#
# 说明：本脚本不触碰任何集群，只做存在性/一致性核对；真实执行记录写
# docs/incident/drills/ 下的演练文档（见 GAME_DAY.md §3）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

SCENARIO="all"
JSON=0
ARTIFACT=""

usage() {
  sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --scenario) SCENARIO="${2:-}"; shift 2 ;;
    --json) JSON=1; shift ;;
    --artifact) ARTIFACT="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "ERROR: 未知参数 $1" >&2; exit 2 ;;
  esac
done

now_ms() { python3 -c 'import time; print(int(time.time()*1000))'; }

RESULTS=()
FAILED=0

# check_alert_severity <alert_id> <expected>  → 打印 "ok|mismatch|missing <actual>"
check_alert_severity() {
  local alert_id="$1" expected="$2"
  python3 - "$alert_id" "$expected" <<'PY'
import pathlib, re, sys
aid, expected = sys.argv[1], sys.argv[2]
path = pathlib.Path("docs/runbooks/ALERTS.md")
if not path.is_file():
    print("missing ALERTS.md")
    raise SystemExit(0)
for line in path.read_text(encoding="utf-8").splitlines():
    m = re.match(r"\|\s*`([a-z0-9._-]+)`\s*\|\s*(P[123])\s*\|", line)
    if m and m.group(1) == aid:
        actual = m.group(2)
        print("ok" if actual == expected else f"mismatch {actual}")
        raise SystemExit(0)
print("missing")
PY
}

# check_rule <alert_id> → 规则文件中是否存在该 alert_id
check_rule() {
  if grep -rqE "alert_id:[[:space:]]*$1$" deploy/observability/prometheus/rules; then
    echo "ok"
  else
    echo "missing"
  fi
}

run_scenario() {
  local id="$1" alert="$2" severity="$3" receiver="$4" runbook="$5"
  shift 5
  local -a markers=("$@")
  local started checks=() failures=0 detail

  started="$(now_ms)"

  if [[ ! -f "${runbook}" ]]; then
    checks+=("runbook|fail|${runbook} 不存在")
    failures=$((failures + 1))
  else
    checks+=("runbook|pass|${runbook}")
  fi

  local sev_result
  sev_result="$(check_alert_severity "${alert}" "${severity}")"
  if [[ "${sev_result}" == "ok" ]]; then
    checks+=("alerts_catalog|pass|${alert}=${severity}")
  else
    checks+=("alerts_catalog|fail|${alert} 目录级别: ${sev_result}")
    failures=$((failures + 1))
  fi

  local rule_result
  rule_result="$(check_rule "${alert}")"
  if [[ "${rule_result}" == "ok" ]]; then
    checks+=("rule|pass|${alert} 规则存在")
  else
    checks+=("rule|fail|${alert} 无对应告警规则")
    failures=$((failures + 1))
  fi

  for marker in "${markers[@]}"; do
    if [[ -f "${runbook}" ]] && grep -qF -- "${marker}" "${runbook}"; then
      checks+=("marker|pass|${marker}")
    else
      checks+=("marker|fail|${runbook} 缺少命令/指标标记: ${marker}")
      failures=$((failures + 1))
    fi
  done

  local route_out route_rc=0
  route_out="$(python3 scripts/ops/paging_probe.py \
    --labels "severity=${severity}" --labels "service=admin-api" \
    --expect-receiver "${receiver}" --allow-placeholder 2>&1)" || route_rc=$?
  if [[ "${route_rc}" -eq 0 ]]; then
    checks+=("route|pass|severity=${severity} → ${receiver}")
  else
    checks+=("route|fail|severity=${severity} 未路由到 ${receiver}: ${route_out}")
    failures=$((failures + 1))
  fi

  local elapsed=$(( $(now_ms) - started ))
  local verdict="PASS"
  if [[ "${failures}" -gt 0 ]]; then
    verdict="FAIL"
    FAILED=1
  fi

  echo "== ${id} ${alert} (${severity}) → ${receiver} : ${verdict} (${elapsed}ms)"
  for detail in "${checks[@]}"; do
    IFS='|' read -r kind status message <<<"${detail}"
    printf '   %-4s %-14s %s\n' "${status^^}" "${kind}" "${message}"
  done

  # 汇总行：id|verdict|elapsed|checks(分号分隔)
  RESULTS+=("${id}|${verdict}|${elapsed}|$(IFS=';'; echo "${checks[*]}")")
}

case "${SCENARIO}" in
  all)
    run_scenario G1 api.5xx.rate_high P1 pager docs/runbooks/release-rollback.md \
      "helm rollback" "release-smoke" "release-canary-gate"
    run_scenario G2 async.dlq.depth P2 ticket docs/runbooks/async-dlq.md \
      "replay-dlq" "async_dlq_depth" "WHERE replayed_at IS NULL"
    run_scenario G3 identity.revocation.store_error P1 pager docs/runbooks/identity-auth-failure.md \
      "identity_revocation_store_errors_total" "fail_closed" "503"
    ;;
  g1) run_scenario G1 api.5xx.rate_high P1 pager docs/runbooks/release-rollback.md \
      "helm rollback" "release-smoke" "release-canary-gate" ;;
  g2) run_scenario G2 async.dlq.depth P2 ticket docs/runbooks/async-dlq.md \
      "replay-dlq" "async_dlq_depth" "WHERE replayed_at IS NULL" ;;
  g3) run_scenario G3 identity.revocation.store_error P1 pager docs/runbooks/identity-auth-failure.md \
      "identity_revocation_store_errors_total" "fail_closed" "503" ;;
  *) echo "ERROR: --scenario 只支持 all|g1|g2|g3（得到 ${SCENARIO}）" >&2; exit 2 ;;
esac

if [[ "${JSON}" -eq 1 || -n "${ARTIFACT}" ]]; then
  json="$(python3 - "${RESULTS[@]}" <<'PY'
import json, sys
scenarios = []
for row in sys.argv[1:]:
    sid, verdict, elapsed, checks = row.split("|", 3)
    items = []
    for chunk in filter(None, checks.split(";")):
        kind, status, message = chunk.split("|", 2)
        items.append({"check": kind, "status": status, "detail": message})
    scenarios.append({"id": sid, "verdict": verdict, "elapsed_ms": int(elapsed), "checks": items})
print(json.dumps({"generated_by": "scripts/ops/game-day-drill.sh", "scenarios": scenarios}, ensure_ascii=False, indent=2))
PY
)"
  if [[ "${JSON}" -eq 1 ]]; then
    printf '%s\n' "${json}"
  fi
  if [[ -n "${ARTIFACT}" ]]; then
    mkdir -p "$(dirname "${ARTIFACT}")"
    printf '%s\n' "${json}" >"${ARTIFACT}"
    echo "artifact: ${ARTIFACT}"
  fi
fi

if [[ "${FAILED}" -ne 0 ]]; then
  echo "game-day drill precheck failed（先修剧本/告警/路由，再演练）" >&2
  exit 1
fi
echo "OK: game-day drill precheck"
