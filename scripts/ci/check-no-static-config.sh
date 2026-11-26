#!/usr/bin/env bash
# CFG-01 防回退：仓库不得再跟踪任何 configs/ 下的静态配置（含 *.example）。
#
# M3 起基线已清空：本脚本仅做「零跟踪」校验，不再以白名单放行样例文件。
# scripts/ci/configs-legacy-baseline.txt 仅保留历史说明注释，不得再登记路径。
#
# 检查项：
#   CFG-01a git ls-files configs 必须为空
#   CFG-01b 基线文件不得再包含非注释路径
#   CFG-01c configs/** 下不得存在未忽略的未跟踪文件
#
# 用法: bash scripts/ci/check-no-static-config.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

BASELINE="scripts/ci/configs-legacy-baseline.txt"
FAIL=0

fail() {
  echo "ERROR: $*" >&2
  FAIL=1
}

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "ERROR: not a git work tree; CFG-01 需要 git 跟踪信息（请在仓库根执行）" >&2
  exit 1
fi

tracked="$(git ls-files 'configs' | LC_ALL=C sort -u || true)"

echo "==> CFG-01a: git ls-files configs must be empty (M3)"
if [[ -n "${tracked}" ]]; then
  fail "tracked files under configs/ are forbidden after M3 (CFG-01 / CC-F-43):"
  printf '%s\n' "${tracked}" >&2
else
  echo "    OK: no tracked files under configs/"
fi

echo "==> CFG-01b: legacy baseline must not list paths"
if [[ ! -f "${BASELINE}" ]]; then
  fail "missing ${BASELINE} (keep as empty marker with comments only)"
else
  listed="$(grep -Ev '^[[:space:]]*(#|$)' "${BASELINE}" || true)"
  if [[ -n "${listed}" ]]; then
    fail "${BASELINE} still lists paths; M3 requires an empty baseline:"
    printf '%s\n' "${listed}" >&2
  else
    echo "    OK: baseline contains comments only"
  fi
fi

echo "==> CFG-01c: no untracked, non-ignored files under configs/"
untracked="$(git ls-files --others --exclude-standard 'configs' || true)"
if [[ -n "${untracked}" ]]; then
  fail "untracked files under configs/ must not be committed (CFG-01):"
  printf '%s\n' "${untracked}" >&2
else
  echo "    OK: no pending files under configs/"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "static config check failed (CFG-01)" >&2
  exit 1
fi
echo "OK: no static config"
