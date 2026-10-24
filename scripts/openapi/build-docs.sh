#!/usr/bin/env bash
# 将分端 OpenAPI 渲染为静态 HTML（仅本地/CI 产物，禁止在生产进程挂载，SEC-08）。
# 用法: bash scripts/openapi/build-docs.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

REDOCLY_CLI_VERSION="${REDOCLY_CLI_VERSION:-1.25.0}"
OUT_DIR="${OPENAPI_DOCS_DIR:-dist/openapi/docs}"
SPECS=(api/openapi/admin.yaml api/openapi/biz.yaml api/openapi/c.yaml)

if ! command -v npx >/dev/null 2>&1; then
  echo "error: npx not found; install Node.js 20+ (docs are a dev/CI artifact, not a runtime dependency)" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}"
for spec in "${SPECS[@]}"; do
  name="$(basename "${spec}" .yaml)"
  echo "==> docs: ${spec} -> ${OUT_DIR}/${name}.html"
  npx --yes "@redocly/cli@${REDOCLY_CLI_VERSION}" build-docs "${spec}" --output "${OUT_DIR}/${name}.html"
done

echo "OK: openapi docs -> ${OUT_DIR}"
