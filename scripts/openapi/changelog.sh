#!/usr/bin/env bash
# 生成相对基线的 OpenAPI 变更日志（markdown），用于发布说明与契约订阅。
# 用法: bash scripts/openapi/changelog.sh [base-ref]
# 基线解析顺序：$1 -> OPENAPI_BASE_REF -> origin/main -> origin/master -> main -> master
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

OASDIFF_VERSION="${OASDIFF_VERSION:-v1.33.0}"
SEVERITY_FILE="api/openapi/.oasdiff-severity.txt"
OUT_DIR="${OPENAPI_CHANGELOG_DIR:-dist/openapi/changelog}"
SPECS=(api/openapi/admin.yaml api/openapi/biz.yaml api/openapi/c.yaml)
BUNDLER="./scripts/openapi/bundle"

TMP_DIR="$(mktemp -d)"
cleanup() { rm -rf "${TMP_DIR}"; }
trap cleanup EXIT

resolve_base_ref() {
  if [[ -n "${1:-}" ]]; then
    echo "${1}"
    return 0
  fi
  if [[ -n "${OPENAPI_BASE_REF:-}" ]]; then
    echo "${OPENAPI_BASE_REF}"
    return 0
  fi
  local cand
  for cand in origin/main origin/master main master; do
    if git rev-parse --verify "${cand}" >/dev/null 2>&1; then
      echo "${cand}"
      return 0
    fi
  done
  return 1
}

BASE_REF=""
if ! BASE_REF="$(resolve_base_ref "${1:-}")"; then
  echo "WARN: no git base ref; skipping changelog (set OPENAPI_BASE_REF or pass a ref)" >&2
  exit 0
fi

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

echo "==> oasdiff ${OASDIFF_VERSION} changelog (base: ${BASE_REF})"

# 基线为多文件目录树，整树取出后两侧各装配为单文件再比对。
BASE_DIR="${TMP_DIR}/base"
mkdir -p "${BASE_DIR}"
if ! git archive "${BASE_REF}" api/openapi | tar -x -C "${BASE_DIR}"; then
  echo "WARN: cannot export ${BASE_REF}:api/openapi; skipping changelog" >&2
  exit 0
fi

mkdir -p "${OUT_DIR}"
for spec in "${SPECS[@]}"; do
  name="$(basename "${spec}" .yaml)"
  out="${OUT_DIR}/${name}.md"
  if [[ ! -f "${BASE_DIR}/${spec}" ]]; then
    printf '# %s\n\n新契约（基线 %s 中不存在）。\n' "${spec}" "${BASE_REF}" >"${out}"
    echo "    ${spec}: new file, wrote ${out}"
    continue
  fi
  base_bundle="${TMP_DIR}/base-${name}.yaml"
  rev_bundle="${TMP_DIR}/rev-${name}.yaml"
  go run "${BUNDLER}" "${BASE_DIR}/${spec}" "${base_bundle}"
  go run "${BUNDLER}" "${spec}" "${rev_bundle}"
  {
    printf '# %s\n\n基线：`%s` → 当前工作区\n\n' "${spec}" "${BASE_REF}"
    go run "github.com/oasdiff/oasdiff@${OASDIFF_VERSION}" changelog \
      "${base_bundle}" "${rev_bundle}" \
      --severity-levels "${SEVERITY_FILE}" \
      --deprecation-days-stable 90 \
      --format markdown
  } >"${out}"
  echo "    ${spec}: wrote ${out}"
done

echo "OK: openapi changelog -> ${OUT_DIR}"
