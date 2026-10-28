#!/usr/bin/env bash
# 将分端多文件 OpenAPI 装配为单文件（分发产物，落 dist/，不入库）。
# 用法: bash scripts/openapi/bundle.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

OUT_DIR="${OPENAPI_BUNDLE_DIR:-dist/openapi/bundled}"
SPECS=(api/openapi/admin.yaml api/openapi/biz.yaml api/openapi/c.yaml)

# Git Bash on Windows 常未继承系统 PATH 中的 Go（与 scripts/ci/check-openapi.sh 一致）。
if ! command -v go >/dev/null 2>&1; then
  for d in "/c/Program Files/Go/bin" "/usr/local/go/bin" "${HOME}/go/bin"; do
    if [[ -x "${d}/go" || -x "${d}/go.exe" ]]; then
      export PATH="${d}:${PATH}"
      break
    fi
  done
fi
if ! command -v go >/dev/null 2>&1; then
  echo "error: go not found in PATH" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}"
for spec in "${SPECS[@]}"; do
  name="$(basename "${spec}" .yaml)"
  echo "==> bundle: ${spec} -> ${OUT_DIR}/${name}.yaml"
  go run ./scripts/openapi/bundle "${spec}" "${OUT_DIR}/${name}.yaml"
done

echo "OK: openapi bundle -> ${OUT_DIR}"
