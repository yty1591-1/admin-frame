#!/usr/bin/env bash
# 由分端 OpenAPI 生成 TypeScript 类型（dist/ 已忽略，不入库；版本可按需覆盖）。
# 用法: bash scripts/openapi/gen-client.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

OPENAPI_TS_VERSION="${OPENAPI_TS_VERSION:-7.4.0}"
OUT_DIR="${OPENAPI_CLIENT_DIR:-dist/openapi/client}"
SPECS=(api/openapi/admin.yaml api/openapi/biz.yaml api/openapi/c.yaml)

if ! command -v npx >/dev/null 2>&1; then
  echo "error: npx not found; install Node.js 20+ (client types are a dev/CI artifact, not a runtime dependency)" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}"
for spec in "${SPECS[@]}"; do
  name="$(basename "${spec}" .yaml)"
  echo "==> client: ${spec} -> ${OUT_DIR}/${name}.d.ts"
  npx --yes "openapi-typescript@${OPENAPI_TS_VERSION}" "${spec}" -o "${OUT_DIR}/${name}.d.ts"
done

echo "OK: openapi TS types -> ${OUT_DIR}"
