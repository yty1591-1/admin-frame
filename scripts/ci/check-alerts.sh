#!/usr/bin/env bash
# ENT-06：ALERTS.md ↔ Prometheus 规则 ↔ runbook ↔ 看板一致性。
# 用法: bash scripts/ci/check-alerts.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

RULES_DIR="deploy/observability/prometheus/rules"
ALERTS="docs/runbooks/ALERTS.md"
DASH_DIR="deploy/observability/grafana/dashboards"

echo "==> ENT-06: rule files exist"
for f in api iam async db slo release; do
  if [[ ! -f "${RULES_DIR}/${f}.yaml" ]]; then
    echo "ERROR: missing ${RULES_DIR}/${f}.yaml (AC-ENT-06-1)" >&2
    exit 1
  fi
done

echo "==> ENT-06: alert id / severity / runbook consistency"
python3 - "$RULES_DIR" "$ALERTS" <<'PY'
import pathlib, re, sys

rules_dir = pathlib.Path(sys.argv[1])
alerts_path = pathlib.Path(sys.argv[2])

catalog = {}
for line in alerts_path.read_text(encoding="utf-8").splitlines():
    m = re.match(r"\|\s*`([a-z0-9._-]+)`\s*\|\s*(P[0123])\s*\|", line)
    if m:
        catalog[m.group(1)] = m.group(2)

rules = {}
missing_runbook = []
for path in sorted(rules_dir.glob("*.yaml")):
    text = path.read_text(encoding="utf-8")
    for block in text.split("- alert:"):
        m_id = re.search(r"alert_id:\s*([a-z0-9._-]+)", block)
        if not m_id:
            continue
        alert_id = m_id.group(1)
        sev = re.search(r"severity:\s*(P[0123])", block)
        rules[alert_id] = sev.group(1) if sev else "?"
        rb = re.search(r"runbook_url:\s*(\S+)", block)
        if not rb:
            missing_runbook.append((alert_id, "no runbook_url"))
            continue
        target = pathlib.Path(rb.group(1))
        if not target.exists():
            missing_runbook.append((alert_id, str(target)))

failed = False
for alert_id, sev in catalog.items():
    if sev not in ("P0", "P1", "P2"):
        continue
    if alert_id not in rules:
        print(f"ERROR: ALERTS.md entry `{alert_id}` has no rule (AC-ENT-06-1)", file=sys.stderr)
        failed = True
    elif rules[alert_id] != sev:
        print(f"ERROR: severity mismatch for {alert_id}: ALERTS={sev} rule={rules[alert_id]}", file=sys.stderr)
        failed = True

for alert_id in rules:
    if alert_id not in catalog:
        print(f"ERROR: rule alert_id `{alert_id}` missing from ALERTS.md (RB-05)", file=sys.stderr)
        failed = True

for alert_id, reason in missing_runbook:
    print(f"ERROR: {alert_id} runbook issue: {reason}", file=sys.stderr)
    failed = True

if failed:
    sys.exit(1)
print(f"    OK: {len(rules)} rules mapped to {len(catalog)} catalog entries")
PY

echo "==> ENT-06: dashboards must be valid JSON"
for f in "${DASH_DIR}"/*.json; do
  python3 -c 'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' "${f}"
  echo "    OK: $(basename "${f}")"
done

echo "==> ENT-06: promtool (optional)"
if command -v promtool >/dev/null 2>&1; then
  promtool check rules "${RULES_DIR}"/*.yaml
else
  echo "    SKIP: promtool not installed; CI observability job must run it"
fi

echo "OK: alerts"
