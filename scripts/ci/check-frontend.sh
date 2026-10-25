#!/usr/bin/env bash
# 前端质量门禁（FE-01～FE-20，见 docs/frontend-architecture.md §15/§16）。
# 与 .github/workflows/ci.yml frontend job、Gitee scripts/ci/gitee-job.sh frontend 同一入口。
# 本地等价：make frontend-ci
# 用法: bash scripts/ci/check-frontend.sh [--skip-install]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}/frontend"

SKIP_INSTALL=0
for arg in "$@"; do
  case "${arg}" in
    --skip-install) SKIP_INSTALL=1 ;;
    *) echo "unknown arg: ${arg}" >&2; exit 2 ;;
  esac
done

# FE-17：pnpm 版本钉扎在 package.json packageManager；CI 用 corepack 兜底。
if ! command -v pnpm >/dev/null 2>&1; then
  echo "==> pnpm not found; enabling via corepack"
  corepack enable
  corepack prepare --activate
fi
echo "==> node $(node --version) / pnpm $(pnpm --version)"

# 与 frontend/package.json engines 对齐；低版本 Node 会让 Vite/Vitest 以晦涩错误失败。
NODE_MAJOR="$(node -p 'process.versions.node.split(".")[0]')"
NODE_MINOR="$(node -p 'process.versions.node.split(".")[1]')"
if [[ "${NODE_MAJOR}" -lt 20 || ( "${NODE_MAJOR}" -eq 20 && "${NODE_MINOR}" -lt 19 ) ]]; then
  echo "ERROR: Node $(node --version) 不满足 frontend engines（>=20.19.0）" >&2
  exit 1
fi

# FE-17：lockfile 必须入库且与 package.json 一致。
if [[ ! -f pnpm-lock.yaml ]]; then
  echo "ERROR: frontend/pnpm-lock.yaml missing; run pnpm install and commit it (FE-17)" >&2
  exit 1
fi

if [[ "${SKIP_INSTALL}" -eq 0 ]]; then
  echo "==> install (frozen lockfile)"
  pnpm install --frozen-lockfile
fi

# FE-08：OpenAPI 生成的 TS 类型必须入库；重新生成后工作区必须干净。
echo "==> FE-08: regenerate API types and assert committed output"
pnpm gen:api
if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  if ! git diff --exit-code -- packages/api-client/src/generated; then
    echo "ERROR: generated API types are stale; run 'pnpm gen:api' and commit (FE-08)" >&2
    exit 1
  fi
fi

echo "==> FE-03: prettier"
pnpm format:check

echo "==> FE-02: eslint (flat config, typed rules)"
pnpm lint

echo "==> FE-04: dependency-cruiser"
bash ../scripts/ci/check-frontend-boundaries.sh

echo "==> FE-01: tsc strict"
pnpm typecheck

echo "==> FE-18: vitest + coverage thresholds"
pnpm test:ci

echo "==> FE-12: production build"
pnpm build

echo "==> FE-19: bundle size budget"
pnpm size

echo "==> FE-17: license allowlist"
pnpm license

# §16.2 依赖安全：离线或无 registry 访问时显式失败，禁止静默跳过。
echo "==> dependency audit (high+)"
pnpm audit --audit-level=high

echo "OK: frontend"
