#!/usr/bin/env bash
# FE-SEC-02：静态站安全头巡检（FE-S-02）
#
# 目标：nginx 示例配置必须下发 5 个安全头，CSP 首版为 Report-Only；
# 同源静态站不得再出现 Access-Control-Allow-Origin: *。
#
# 说明：这是静态件校验；「curl -sI 见 5 个头」需部署后取证，
# 发布流水线（FE-B）落地后补 staging 实测。
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

CONF="frontend/scripts/deploy/nginx.conf"
errors=0

fail() {
  echo "ERROR: $*"
  errors=$((errors + 1))
}

need() {
  local pattern="$1" label="$2"
  if ! grep -qE "$pattern" "$CONF"; then
    fail "missing $label in $CONF"
  fi
}

echo "==> FE-SEC-02: nginx static headers"

if [[ ! -f "$CONF" ]]; then
  echo "ERROR: $CONF not found"
  exit 1
fi

need 'X-Content-Type-Options[[:space:]]+"?nosniff' 'X-Content-Type-Options: nosniff'
need 'X-Frame-Options[[:space:]]+"?DENY' 'X-Frame-Options: DENY'
need 'Referrer-Policy[[:space:]]+"?strict-origin-when-cross-origin' 'Referrer-Policy'
need 'Permissions-Policy[[:space:]]+"?camera=' 'Permissions-Policy'
need 'Strict-Transport-Security' 'Strict-Transport-Security (HSTS)'
need 'Content-Security-Policy-Report-Only' 'Content-Security-Policy-Report-Only'

if grep -qE "Access-Control-Allow-Origin[[:space:]]+'?\\*'?" "$CONF"; then
  fail "same-origin static site must not send Access-Control-Allow-Origin: *"
fi

if grep -qE 'Content-Security-Policy[[:space:]]' "$CONF"; then
  fail "CSP must stay Report-Only until staging observation completes"
fi

if [[ "$errors" -gt 0 ]]; then
  echo "frontend headers check failed (FE-SEC-02)"
  exit 1
fi

echo "OK: nginx static headers (5 security headers + CSP Report-Only)"