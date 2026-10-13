#!/usr/bin/env bash
# DEPENDENCY_STANDARDS：go.sum、replace 白名单、许可证允许列表。
# 用法: bash scripts/ci/check-deps.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

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

GO_LICENSES_VERSION="${GO_LICENSES_VERSION:-v1.6.0}"
MODULE="$(go list -m -f '{{.Path}}')"
ALLOW_REPLACE="deps/replace-allowlist.txt"
ALLOW_LICENSES="deps/allowed-licenses.txt"
FAIL=0

echo "==> DEP-01: go.sum must exist and be non-empty"
if [[ ! -s go.sum ]]; then
  echo "ERROR: go.sum missing or empty (DEP-01)" >&2
  FAIL=1
else
  echo "    OK: go.sum"
fi

echo "==> DEP-03: replace directives must be allowlisted"
# 收集 go.mod 中 replace 左侧模块路径（忽略注释行）。
replaces=()
while IFS= read -r line; do
  [[ -z "${line}" ]] && continue
  replaces+=("${line}")
done < <(awk '
  function left_module(s,    parts, left, w) {
    gsub(/\/\/.*/, "", s)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
    if (s == "") return ""
    split(s, parts, /=>/)
    left = parts[1]
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", left)
    split(left, w, /[[:space:]]+/)
    return w[1]
  }
  /^[[:space:]]*\/\// { next }
  /^replace[[:space:]]*\(/ { inblock=1; next }
  inblock && /^\)/ { inblock=0; next }
  inblock {
    m = left_module($0)
    if (m != "") print m
    next
  }
  /^replace[[:space:]]+/ {
    sub(/^replace[[:space:]]+/, "")
    m = left_module($0)
    if (m != "") print m
  }
' go.mod)

allowed_replace=()
if [[ -f "${ALLOW_REPLACE}" ]]; then
  while IFS= read -r line; do
    [[ -z "${line}" || "${line}" =~ ^[[:space:]]*# ]] && continue
    allowed_replace+=("${line}")
  done < "${ALLOW_REPLACE}"
fi

is_allowed_replace() {
  local mod="$1" a
  for a in "${allowed_replace[@]+"${allowed_replace[@]}"}"; do
    [[ "${a}" == "${mod}" ]] && return 0
  done
  return 1
}

if [[ "${#replaces[@]}" -eq 0 ]]; then
  echo "    OK: no replace directives"
else
  for mod in "${replaces[@]}"; do
    if is_allowed_replace "${mod}"; then
      echo "    OK (allowlisted): replace ${mod}"
    else
      echo "ERROR: replace ${mod} not in ${ALLOW_REPLACE} (DEP-03); add ADR + allowlist entry" >&2
      FAIL=1
    fi
  done
fi

echo "==> DEP-05: license allowlist (go-licenses ${GO_LICENSES_VERSION})"
lic_args=()
while IFS= read -r line; do
  [[ -z "${line}" || "${line}" =~ ^[[:space:]]*# ]] && continue
  lic_args+=("${line}")
done < "${ALLOW_LICENSES}"
if [[ "${#lic_args[@]}" -eq 0 ]]; then
  echo "ERROR: ${ALLOW_LICENSES} has no licenses (DEP-05)" >&2
  FAIL=1
else
  IFS=','
  allowed_csv="${lic_args[*]}"
  unset IFS
  if ! go run "github.com/google/go-licenses@${GO_LICENSES_VERSION}" check ./... \
    --ignore="${MODULE}" \
    --allowed_licenses="${allowed_csv}"; then
    echo "ERROR: license check failed (DEP-05)" >&2
    FAIL=1
  else
    echo "    OK: licenses within allowlist"
  fi
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "dependency check failed (DEPENDENCY_STANDARDS)" >&2
  exit 1
fi
echo "OK: deps"
