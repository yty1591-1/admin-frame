#!/usr/bin/env bash
# 前端必跑门禁唯一入口（CI-18）。
#
# 消费者（必须同时存在，CI-16 parity 校验）：
#   - .github/workflows/ci.yml 的 frontend job
#   - Gitee Go 三个流水线的「前端门禁」step（bash scripts/ci/gitee-job.sh frontend）
#   - 本地 make frontend-check（刻意不并入 make ci：pnpm install 依赖网络且耗时数分钟）
#
# 判定口径与 CI 完全一致；任何一步非 0 即整体失败。
# 用法: bash scripts/ci/frontend-check.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}/frontend"

if ! command -v pnpm >/dev/null 2>&1; then
  echo "error: pnpm not found (frontend/package.json 要求 pnpm@11；可用 corepack enable 启用)" >&2
  exit 1
fi

echo "==> [1/9] pnpm install --frozen-lockfile"
pnpm install --frozen-lockfile

echo "==> [2/9] pnpm lint（oxlint/oxfmt/eslint/stylelint 全量，非 staged）"
pnpm lint

echo "==> [3/9] pnpm check:cspell（含 **/*.vue 模板，FE-G-04）"
pnpm check:cspell

# 依赖检查与循环依赖检查此前恒 exit 0，这里接入是 FE-G-02 的消费者。
echo "==> [4/9] pnpm check:dep"
pnpm check:dep

echo "==> [5/9] pnpm check:circular"
pnpm check:circular

echo "==> [6/9] pnpm check:type（32 个包 + 2 个 app，FE-G-05）"
pnpm check:type

# 根 vitest 覆盖 packages/internal/scripts；两个 app 各自一份配置，
# 因为 `#/` 别名与覆盖率阈值必须按应用隔离（FE-G-06）。
echo "==> [7/9] pnpm test:unit（根 + web-admin + web-biz，含覆盖率阈值）"
pnpm test:unit

echo "==> [8/9] check-frontend-gates.sh（apps 冒烟用例与覆盖率门禁，FE-G-06）"
bash "${ROOT}/scripts/ci/check-frontend-gates.sh"

echo "==> [9/9] pnpm build:admin && pnpm build:biz"
pnpm build:admin
pnpm build:biz

echo "==> frontend gates passed"
