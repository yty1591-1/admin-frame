#!/usr/bin/env bash
# CI-09: 禁止同一业务域内的跨端 transport 互引（admin / biz / c）。
#
# 目录按 internal/*/transport 动态发现，新增业务域时无需改脚本。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
SEEN=0

for base in internal/*/transport; do
  [[ -d "${base}" ]] || continue
  domain="$(basename "$(dirname "${base}")")"
  for dir in "${base}"/*/; do
    [[ -d "${dir}" ]] || continue
    name="$(basename "${dir}")"
    case "${name}" in
      admin|biz|c) ;;
      *) continue ;;
    esac
    SEEN=$((SEEN + 1))
    for other in admin biz c; do
      [[ "${name}" == "${other}" ]] && continue
      if grep -RIn --include='*.go' --exclude='*_test.go' \
        "internal/${domain}/transport/${other}" "${dir}" 2>/dev/null; then
        echo "ERROR: ${domain}/transport/${name} must not import transport/${other}" >&2
        FAIL=1
      fi
    done
  done
done

if [[ "${SEEN}" -eq 0 ]]; then
  echo "ERROR: no internal/*/transport/{admin,biz,c} directories found (CI-09)" >&2
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "cross-channel transport import check failed (CI-09)" >&2
  exit 1
fi

echo "OK: no cross-channel transport imports"
