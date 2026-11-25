#!/usr/bin/env bash
# FE-G-06：前端应用级冒烟用例与覆盖率门槛守卫。
#
# 校验项：
#   1. 两个 app 都存在应用级 *.test.ts（不允许“只有 packages 有测试”）
#   2. 用例覆盖 v-access 指令 / pageMap 菜单映射（FE-T-01 验收口径）
#   3. 覆盖率阈值由 apps/*/vitest.config.ts 的 coverage.thresholds 强制：
#      test:unit 的应用段低于阈值即整体非 0，本脚本不重复计算。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}/frontend"

fail=0

for app in web-admin web-biz; do
  if [ -z "$(find "apps/${app}/src" -name '*.test.ts' -print -quit)" ]; then
    echo "ERROR: apps/${app} 缺少应用级冒烟用例（FE-T-01）" >&2
    fail=1
  fi
done

if ! grep -rlq -e 'v-access' -e 'pageMap' --include='*.test.ts' apps; then
  echo "ERROR: 应用级用例未覆盖 v-access / pageMap（FE-T-01）" >&2
  fail=1
fi

if [ "${fail}" -ne 0 ]; then
  exit 1
fi

echo "✅ frontend app gates: apps 冒烟用例与覆盖率门槛已接入"
