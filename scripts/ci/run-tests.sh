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

# -p 1：集成包共用同一套 MySQL（CI 的 service 容器 / 本地 compose），且每个包启动时
# 都会 reset 清表（见 tests/integration/iam 的 testDB）。并行执行包会互相清表、
# 争抢固定 bootstrap 账号，产生与被测代码无关的假红（2026-11-07 在多核机器上稳定复现：
# iam TestSQLUnitOfWorkRollsBackRoleWhenOutboxFails、tenant TestMemberRoleBindingSyncOnMySQL）。
# run-integration-tests.sh 早已串行，这里对齐；CI runner 核数少不易撞，但不该靠运气。
if [[ "${race_ok}" -eq 1 ]]; then
  echo "==> go test -race -p 1 -count=1 -coverprofile=coverage.out ./..."
  go test -race -p 1 -count=1 -coverprofile=coverage.out ./...
else
  if [[ "$(go env GOOS)" == "windows" ]]; then
    echo "WARN: CGO toolchain missing; running without -race (GitHub/Gitee Linux CI still uses -race)" >&2
    go test -p 1 -count=1 -coverprofile=coverage.out ./...
  else
    echo "error: -race required but CGO toolchain unavailable (TEST-04)" >&2
    exit 1
  fi
fi

bash scripts/ci/check-test-gates.sh
