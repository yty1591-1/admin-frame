#!/usr/bin/env bash
# FE-04/FE-05：前端分层与跨包边界门禁（dependency-cruiser）。
# 与 docs/frontend-architecture.md §5 对齐：apps 分层、packages 单向依赖、
# 禁止 apps 互引 / packages→apps / 深链 internal、禁止 HTTP 库越界。
# 用法: bash scripts/ci/check-frontend-boundaries.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}/frontend"

if [[ ! -f dependency-cruiser.cjs ]]; then
  echo "ERROR: frontend/dependency-cruiser.cjs missing (FE-04)" >&2
  exit 1
fi
if [[ ! -d node_modules ]]; then
  echo "ERROR: frontend/node_modules missing; run pnpm install first" >&2
  exit 1
fi

echo "==> FE-04: dependency-cruiser (layers + package boundaries)"
pnpm boundaries

echo "OK: frontend boundaries"
