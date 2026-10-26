#!/usr/bin/env bash
# Gitee Go 与 GitHub Actions 共用的单 job 入口（对齐各 STANDARDS 门禁）。
# 用法: bash scripts/ci/gitee-job.sh <fmt|mod|lint|test|vuln|arch|secrets|openapi|build>
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

MODULE="${MODULE:-github.com/admin-frame/admin-frame}"
GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-v2.14.0}"
GOIMPORTS_VERSION="${GOIMPORTS_VERSION:-v0.30.0}"
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.1.4}"

export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-sum.golang.google.cn}"
if command -v go >/dev/null 2>&1; then
  export PATH="$(go env GOPATH)/bin:${PATH}"
fi

job="${1:-}"
if [[ -z "${job}" ]]; then
  echo "usage: $0 <fmt|mod|lint|test|vuln|arch|secrets|openapi|build>" >&2
  exit 2
fi

if command -v go >/dev/null 2>&1; then
  echo "==> go version: $(go version)"
fi
echo "==> job: ${job}"

case "${job}" in
  fmt)
    out="$(gofmt -l .)"
    if [[ -n "${out}" ]]; then
      echo "gofmt needed on:" >&2
      echo "${out}" >&2
      exit 1
    fi
    go install "golang.org/x/tools/cmd/goimports@${GOIMPORTS_VERSION}"
    out="$(goimports -local "${MODULE}" -l .)"
    if [[ -n "${out}" ]]; then
      echo "goimports needed on:" >&2
      echo "${out}" >&2
      exit 1
    fi
    ;;
  mod)
    # 与 Makefile tidy-check 一致：要求 tidy 幂等（CI 干净检出 + 已提交 go.mod 时等价于 git diff 干净）
    tmp="$(mktemp -d)"
    cp go.mod "${tmp}/"
    if [[ -f go.sum ]]; then cp go.sum "${tmp}/"; fi
    go mod tidy
    if ! cmp -s "${tmp}/go.mod" go.mod || { [[ -f go.sum ]] && ! cmp -s "${tmp}/go.sum" go.sum; }; then
      echo "error: go mod tidy changed go.mod/go.sum; commit the result" >&2
      diff -u "${tmp}/go.mod" go.mod || true
      rm -rf "${tmp}"
      exit 1
    fi
    rm -rf "${tmp}"
    ;;
  lint)
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
      | sh -s -- -b "$(go env GOPATH)/bin" "${GOLANGCI_LINT_VERSION}"
    golangci-lint run ./...
    ;;
  test)
    bash scripts/ci/run-tests.sh
    ;;
  vuln)
    go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"
    govulncheck ./...
    bash scripts/ci/check-deps.sh
    ;;
  arch)
    bash scripts/ci/check-transport-imports.sh
    bash scripts/ci/check-layering.sh
    bash scripts/ci/check-migrations.sh
    bash scripts/ci/check-reliability.sh
    bash scripts/ci/check-observability.sh
    bash scripts/ci/check-git.sh
    bash scripts/ci/check-release.sh
    bash scripts/ci/check-runbooks.sh
    bash scripts/ci/check-audit.sh
    bash scripts/ci/check-license.sh
    bash scripts/ci/check-codegen.sh
    bash scripts/ci/check-perf.sh
    bash scripts/ci/check-idempotency.sh
    bash scripts/ci/check-outbox.sh
    bash scripts/ci/check-authn.sh
    bash scripts/ci/check-alerts.sh
    bash scripts/ci/check-backup.sh
    ;;
  secrets)
    bash scripts/ci/check-debug-endpoints.sh
    bash scripts/ci/gitleaks.sh
    bash scripts/ci/check-config.sh
    ;;
  openapi)
    bash scripts/ci/check-openapi.sh
    ;;
  build)
    mkdir -p bin
    go build -ldflags "-s -w" -o bin/admin-api ./cmd/admin-api
    go build -ldflags "-s -w" -o bin/biz-api ./cmd/biz-api
    go build -ldflags "-s -w" -o bin/c-api ./cmd/c-api
    go build -ldflags "-s -w" -o bin/worker ./cmd/worker
    go build -ldflags "-s -w" -o bin/migrate ./cmd/migrate
    ;;
  *)
    echo "unknown job: ${job}" >&2
    exit 2
    ;;
esac

echo "OK: ${job}"
