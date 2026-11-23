#!/usr/bin/env bash
# CI-15：门禁脚本引用的仓库内路径必须存在。
#
# 背景：平台层拆包把 server.go / pipeline.go / middleware.go 等文件迁入子包后，
# 多个 check-*.sh 仍指向旧路径。后果分两种：
#   - 正向断言（路径存在且含某符号）→ 直接报 ERROR，arch job 长期红灯；
#   - 负向断言（路径不存在即视为通过）→ grep 失败被当成「无违规」，静默失去覆盖。
# 本门禁把「文件搬家必须同步门禁脚本」变成可执行断言，避免同类问题再次以红灯或假绿的形式潜伏。
#
# 用法: bash scripts/ci/check-ci-paths.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0
fail() { echo "    ERROR: $1" >&2; FAIL=1; }

echo "==> CP-01: paths referenced by gate scripts must exist"
# 只取代码与文档目录：configs 下的 yaml 名多由脚本拼接（如 configs/prod/${name}.yaml），
# 纳入会误报为「路径不存在」。
PATTERN='(internal|pkg|cmd|tests|migrations|deploy|api|docs|frontend)/[A-Za-z0-9_./-]+\.(go|sql|yaml|yml|sh|md|json)'

refs="$(grep -rhoE "${PATTERN}" scripts/ci/*.sh | sort -u || true)"
if [[ -z "${refs}" ]]; then
  fail "no path reference extracted from scripts/ci/*.sh (pattern broken?)"
  total=0
else
  total="$(printf '%s\n' "${refs}" | wc -l | tr -d ' ')"
  missing=0
  while IFS= read -r p; do
    [[ -n "${p}" ]] || continue
    if [[ ! -e "${p}" ]]; then
      fail "${p} is referenced by a gate script but does not exist (CP-01)"
      missing=$((missing + 1))
    fi
  done <<< "${refs}"
  echo "    checked ${total} referenced paths, missing ${missing}"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "ci path check failed (CI_STANDARDS CI-15)" >&2
  exit 1
fi
echo "OK: ci paths (${total} refs)"
