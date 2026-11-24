#!/usr/bin/env bash
# FE-SEC-02 / FE-SEC-03：静态站安全头与 HTML 禁缓存巡检（FE-S-02/FE-S-06）
#
# 目标：
# - security-headers.conf 下发 5 个安全头，CSP 首版为 Report-Only；
# - nginx.conf 在 server 与 /index.html 两处 include 该片段；
# - SPA 入口（/index.html）带 Cache-Control: no-store（MFA/登录页承载）；
# - 同源静态站不得再出现 Access-Control-Allow-Origin: *。
#
# 说明：这是静态件校验；「curl -sI 见 5 个头」需部署后取证，
# 发布流水线（FE-B）落地后补 staging 实测。
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

CONF="frontend/scripts/deploy/nginx.conf"
HEADERS="frontend/scripts/deploy/security-headers.conf"
DOCKERFILE="frontend/scripts/deploy/Dockerfile"
errors=0

fail() {
  echo "ERROR: $*"
  errors=$((errors + 1))
}

need() {
  local file="$1" pattern="$2" label="$3"
  if ! grep -qE "$pattern" "$file"; then
    fail "missing $label in $file"
  fi
}

echo "==> FE-SEC-02: nginx static headers"

for f in "$CONF" "$HEADERS" "$DOCKERFILE"; do
  if [[ ! -f "$f" ]]; then
    echo "ERROR: $f not found"
    exit 1
  fi
done

need "$HEADERS" 'X-Content-Type-Options[[:space:]]+"?nosniff' 'X-Content-Type-Options: nosniff'
need "$HEADERS" 'X-Frame-Options[[:space:]]+"?DENY' 'X-Frame-Options: DENY'
need "$HEADERS" 'Referrer-Policy[[:space:]]+"?strict-origin-when-cross-origin' 'Referrer-Policy'
need "$HEADERS" 'Permissions-Policy[[:space:]]+"?camera=' 'Permissions-Policy'
need "$HEADERS" 'Strict-Transport-Security' 'Strict-Transport-Security (HSTS)'
need "$HEADERS" 'Content-Security-Policy-Report-Only' 'Content-Security-Policy-Report-Only'

if grep -qE "Access-Control-Allow-Origin[[:space:]]+'?\\*'?" "$HEADERS" "$CONF"; then
  fail "same-origin static site must not send Access-Control-Allow-Origin: *"
fi

if grep -qE 'Content-Security-Policy[[:space:]]' "$HEADERS" "$CONF"; then
  fail "CSP must stay Report-Only until staging observation completes"
fi

# server 级与 /index.html 都必须 include 片段（location 出现 add_header 后不继承）。
if [[ "$(grep -cE 'include[[:space:]]+/etc/nginx/security-headers.conf;' "$CONF" || true)" -lt 2 ]]; then
  fail "nginx.conf must include security-headers.conf at server and /index.html (>=2)"
fi

if ! grep -qE 'location[[:space:]]+=[[:space:]]+/index\.html' "$CONF"; then
  fail "nginx.conf must define 'location = /index.html'"
fi

need "$CONF" 'Cache-Control[[:space:]]+"no-store, no-cache, must-revalidate, private"' 'Cache-Control no-store on /index.html'
need "$CONF" 'Pragma[[:space:]]+"no-cache"' 'Pragma: no-cache on /index.html'

need "$DOCKERFILE" 'security-headers.conf /etc/nginx/security-headers.conf' 'Dockerfile copies security-headers.conf'

if [[ "$errors" -gt 0 ]]; then
  echo "frontend headers check failed (FE-SEC-02/03)"
  exit 1
fi

echo "OK: nginx static headers (5 security headers + CSP Report-Only + index.html no-store)"
