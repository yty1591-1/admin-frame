#!/usr/bin/env bash
# CI / make test-ci：全量测试（优先 -race）+ TESTING_STANDARDS 门禁。
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

race_ok=0
if [[ "$(go env CGO_ENABLED)" == "1" ]] && { command -v gcc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1; }; then
  race_ok=1
fi

if [[ "${race_ok}" -eq 1 ]]; then
  echo "==> go test -race -count=1 -coverprofile=coverage.out ./..."
  go test -race -count=1 -coverprofile=coverage.out ./...
else
  if [[ "$(go env GOOS)" == "windows" ]]; then
    echo "WARN: CGO toolchain missing; running without -race (GitHub/Gitee Linux CI still uses -race)" >&2
    go test -count=1 -coverprofile=coverage.out ./...
  else
    echo "error: -race required but CGO toolchain unavailable (TEST-04)" >&2
    exit 1
  fi
fi

bash scripts/ci/check-test-gates.sh
