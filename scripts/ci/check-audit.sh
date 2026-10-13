#!/usr/bin/env bash
# AUDIT_STANDARDS：事件目录、字段字典、audit 包与抽检单测。
# 用法: bash scripts/ci/check-audit.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> AUD-01/02: docs present"
for f in docs/AUDIT_STANDARDS.md docs/audit/EVENTS.md docs/audit/FIELDS.md; do
  if [[ ! -f "${f}" ]]; then
    echo "ERROR: missing ${f}" >&2
    FAIL=1
  else
    echo "    OK: ${f}"
  fi
done

echo "==> AUD-01: baseline actions listed in EVENTS.md"
BASELINE_ACTIONS=(
  auth.login
  auth.login_failed
  rbac.role_change
  config.change
  data.export
  data.query_scope
  payment.capture
  payment.refund
)
if [[ -f docs/audit/EVENTS.md ]]; then
  for a in "${BASELINE_ACTIONS[@]}"; do
    if ! grep -qF "\`${a}\`" docs/audit/EVENTS.md; then
      echo "ERROR: EVENTS.md missing action ${a} (AUD-01)" >&2
      FAIL=1
    else
      echo "    OK: ${a}"
    fi
  done
fi

echo "==> AUD-02: required fields documented"
REQUIRED_FIELDS=(event_id action actor_id channel resource_type resource_id outcome occurred_at trace_id service)
if [[ -f docs/audit/FIELDS.md ]]; then
  for field in "${REQUIRED_FIELDS[@]}"; do
    # 匹配表格列：| `field` |
    if ! grep -Fq "| \`${field}\` |" docs/audit/FIELDS.md; then
      echo "ERROR: FIELDS.md missing ${field} (AUD-02)" >&2
      FAIL=1
    else
      echo "    OK: field ${field}"
    fi
  done
fi

echo "==> AUD-03/04: audit package API"
PKG="internal/shared/audit"
for f in event.go actions.go emitter.go; do
  if [[ ! -f "${PKG}/${f}" ]]; then
    echo "ERROR: missing ${PKG}/${f}" >&2
    FAIL=1
  else
    echo "    OK: ${PKG}/${f}"
  fi
done
if [[ -f "${PKG}/event.go" ]]; then
  for sym in 'func (e Event) Validate' EventID Action ActorID Channel ResourceType ResourceID Outcome OccurredAt TraceID Service; do
    if ! grep -q "${sym}" "${PKG}/event.go"; then
      echo "ERROR: ${PKG}/event.go missing ${sym} (AUD-04)" >&2
      FAIL=1
    fi
  done
fi
if [[ -f "${PKG}/emitter.go" ]] && ! grep -q 'type Emitter interface' "${PKG}/emitter.go"; then
  echo "ERROR: Emitter interface missing (AUD-03)" >&2
  FAIL=1
fi

echo "==> AUD: retention baseline mentioned in standards"
if [[ -f docs/AUDIT_STANDARDS.md ]]; then
  if ! grep -q '180' docs/AUDIT_STANDARDS.md || ! grep -q '365' docs/AUDIT_STANDARDS.md; then
    echo "ERROR: AUDIT_STANDARDS must state 180d/365d retention baselines (AUD-07)" >&2
    FAIL=1
  else
    echo "    OK: retention baselines"
  fi
  if ! grep -qiE 'append-only|WORM|追加写' docs/AUDIT_STANDARDS.md; then
    echo "ERROR: AUDIT_STANDARDS must require append-only/WORM (AUD-06)" >&2
    FAIL=1
  else
    echo "    OK: immutability requirement"
  fi
fi

echo "==> AUD: required unit tests"
if ! command -v go >/dev/null 2>&1; then
  for d in "/c/Program Files/Go/bin" "/usr/local/go/bin" "${HOME}/go/bin"; do
    if [[ -x "${d}/go" || -x "${d}/go.exe" ]]; then
      export PATH="${d}:${PATH}"
      break
    fi
  done
fi
if command -v go >/dev/null 2>&1; then
  if ! grep -RIn --include='*_test.go' -E 'func TestAuditEventValidate\(' "${PKG}" >/dev/null; then
    echo "ERROR: missing TestAuditEventValidate (AUD抽检)" >&2
    FAIL=1
  else
    echo "    OK: TestAuditEventValidate exists"
  fi
  if ! go test -count=1 "./${PKG}/..."; then
    echo "ERROR: audit package tests failed" >&2
    FAIL=1
  else
    echo "    OK: go test ./${PKG}/..."
  fi
else
  echo "ERROR: go not found; cannot run audit package tests" >&2
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "audit check failed (AUDIT_STANDARDS)" >&2
  exit 1
fi
echo "OK: audit"
