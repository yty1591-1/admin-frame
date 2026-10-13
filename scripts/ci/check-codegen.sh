#!/usr/bin/env bash
# CODEGEN_STANDARDS：MANIFEST 存在；已提交的生成 Go 文件须含 DO NOT EDIT。
# 用法: bash scripts/ci/check-codegen.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
MANIFEST="docs/codegen/MANIFEST.md"

echo "==> GEN-01: manifest and standards"
for f in "${MANIFEST}" docs/CODEGEN_STANDARDS.md .mockery.yaml; do
  if [[ ! -f "${f}" ]]; then
    echo "ERROR: missing ${f}" >&2
    FAIL=1
  else
    echo "    OK: ${f}"
  fi
done

echo "==> GEN-01: manifest lists openapi sources"
if [[ -f "${MANIFEST}" ]]; then
  for needle in openapi-admin openapi-biz openapi-c; do
    if ! grep -q "${needle}" "${MANIFEST}"; then
      echo "ERROR: ${MANIFEST} missing ${needle}" >&2
      FAIL=1
    else
      echo "    OK: ${needle}"
    fi
  done
fi

echo "==> GEN-02: generated Go files must have DO NOT EDIT / Code generated"
GEN_COUNT=0
while IFS= read -r f; do
  [[ -z "${f}" ]] && continue
  GEN_COUNT=$((GEN_COUNT + 1))
  head="$(head -n 5 "${f}" || true)"
  if ! printf '%s' "${head}" | grep -qiE 'Code generated|DO NOT EDIT'; then
    echo "ERROR: ${f} looks generated but missing Code generated / DO NOT EDIT (GEN-02)" >&2
    FAIL=1
  else
    echo "    OK: ${f}"
  fi
done < <(
  # portable: prefer git ls-files when available
  if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git ls-files '*.go' | grep -E '(^|/)mocks/|/[^/]+\.gen\.go$|/[^/]+_gen\.go$|\.sql\.go$' || true
  else
    find . -type f \( -path '*/mocks/*.go' -o -name '*.gen.go' -o -name '*_gen.go' -o -name '*.sql.go' \) ! -path './.git/*' 2>/dev/null || true
  fi
)

if [[ "${GEN_COUNT}" -eq 0 ]]; then
  echo "    OK: no generated Go files yet (reserved OK)"
fi

echo "==> GEN: openapi sources exist (hand-written)"
for f in api/openapi/admin.yaml api/openapi/biz.yaml api/openapi/c.yaml api/openapi/components.yaml; do
  if [[ ! -f "${f}" ]]; then
    echo "ERROR: missing OpenAPI source ${f} (GEN-06)" >&2
    FAIL=1
  else
    echo "    OK: ${f}"
  fi
done

if [[ "${FAIL}" -ne 0 ]]; then
  echo "codegen check failed (CODEGEN_STANDARDS)" >&2
  exit 1
fi
echo "OK: codegen"
