#!/usr/bin/env bash
# SEC-08：生产代码不得挂载 pprof / swagger，除非显式标注豁免。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
# 仅扫描 Go 源码目录：仓库根下有 frontend/node_modules（数万文件），
# 整树递归 grep 在 WSL 的 /mnt/d（9p）上极慢。
GO_ROOTS=(cmd internal pkg scripts tests)
while IFS= read -r line; do
  file="${line%%:*}"
  if grep -q 'sec:debug-allowed' "${file}"; then
    continue
  fi
  echo "ERROR: debug endpoint import without //sec:debug-allowed: ${line}" >&2
  FAIL=1
done < <(grep -RIn --include='*.go' --exclude='*_test.go' -E 'net/http/pprof|swaggo/|swagger-ui' "${GO_ROOTS[@]}" || true)

if [[ "${FAIL}" -ne 0 ]]; then
  echo "debug endpoint check failed (SEC-08)" >&2
  exit 1
fi
echo "OK: no unapproved debug endpoints"
