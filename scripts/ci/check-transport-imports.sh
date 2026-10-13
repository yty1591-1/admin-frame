#!/usr/bin/env bash
# CI-09: 禁止跨端 transport 互引（admin / biz / c）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BASE="${ROOT}/internal/order/transport"
FAIL=0

check_no_ref() {
  local dir="$1"
  local forbidden="$2"
  if grep -RIn --include='*.go' --exclude='*_test.go' \
    "internal/order/transport/${forbidden}" "${BASE}/${dir}" 2>/dev/null; then
    echo "ERROR: ${dir} must not import transport/${forbidden}" >&2
    FAIL=1
  fi
}

check_no_ref admin biz
check_no_ref admin c
check_no_ref biz admin
check_no_ref biz c
check_no_ref c admin
check_no_ref c biz

if [[ "${FAIL}" -ne 0 ]]; then
  echo "cross-channel transport import check failed (CI-09)" >&2
  exit 1
fi

echo "OK: no cross-channel transport imports"
