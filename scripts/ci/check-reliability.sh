#!/usr/bin/env bash
# RELIABILITY_STANDARDS：禁止无超时 DefaultClient；限流业务码 42900 必须存在。
# 用法: bash scripts/ci/check-reliability.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

echo "==> REL-01: forbid http.DefaultClient / http.Get / http.Post in business code"
# 测试与生成代码除外。
SEARCH_ROOTS=(cmd internal)
EXCLUDE_GREP='_test\.go$'
HITS="$(grep -RIn --include='*.go' -E 'http\.DefaultClient|http\.Get\(|http\.Post\(|http\.PostForm\(' "${SEARCH_ROOTS[@]}" 2>/dev/null \
  | grep -v -E "${EXCLUDE_GREP}" \
  | grep -v -E '^\s*//' \
  || true)"

# 过滤纯注释行（grep 行内可能仍含代码）；再剔除掉注释主导的匹配。
FILTERED=""
while IFS= read -r line; do
  [[ -z "${line}" ]] && continue
  # file:line:content
  content="${line#*:}"
  content="${content#*:}"
  trimmed="${content#"${content%%[![:space:]]*}"}"
  if [[ "${trimmed}" == //* ]]; then
    continue
  fi
  FILTERED+="${line}"$'\n'
done <<< "${HITS}"

if [[ -n "${FILTERED}" ]]; then
  echo "ERROR: unscoped HTTP client helpers found (REL-01); use http.Client{Timeout: ...} or context deadline:" >&2
  printf '%s' "${FILTERED}" >&2
  FAIL=1
else
  echo "    OK: no DefaultClient/Get/Post in non-test business code"
fi

echo "==> REL-05: apperr must define CodeTooManyRequests=42900 and TooManyRequests helper"
APPERR="internal/shared/apperr/errors.go"
if [[ ! -f "${APPERR}" ]]; then
  echo "ERROR: missing ${APPERR}" >&2
  FAIL=1
else
  if ! grep -q 'CodeTooManyRequests.*=.*42900' "${APPERR}"; then
    echo "ERROR: CodeTooManyRequests = 42900 not found in ${APPERR} (REL-05)" >&2
    FAIL=1
  else
    echo "    OK: CodeTooManyRequests"
  fi
  if ! grep -q 'func TooManyRequests(' "${APPERR}"; then
    echo "ERROR: TooManyRequests() helper missing in ${APPERR} (REL-05)" >&2
    FAIL=1
  else
    echo "    OK: TooManyRequests()"
  fi
fi

COMPONENTS="api/openapi/components.yaml"
if [[ -f "${COMPONENTS}" ]]; then
  if ! grep -q '42900' "${COMPONENTS}"; then
    echo "ERROR: OpenAPI ErrorCode enum missing 42900 (REL-05)" >&2
    FAIL=1
  else
    echo "    OK: openapi includes 42900"
  fi
else
  echo "ERROR: missing ${COMPONENTS}" >&2
  FAIL=1
fi

echo "==> REL-02: ListenAndServe must accept shutdown timeout"
SERVER="internal/platform/httpserver/server.go"
if [[ -f "${SERVER}" ]] && grep -q 'shutdownTimeout' "${SERVER}"; then
  echo "    OK: shutdownTimeout wiring"
else
  echo "ERROR: ${SERVER} must take shutdownTimeout (REL-02)" >&2
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "reliability check failed (RELIABILITY_STANDARDS)" >&2
  exit 1
fi
echo "OK: reliability"
