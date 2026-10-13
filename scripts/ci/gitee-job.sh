#!/usr/bin/env bash
# Gitee Go 与 GitHub Actions 共用的单 job 入口（对齐 CI_STANDARDS CI-03～CI-09）。
# 用法: bash scripts/ci/gitee-job.sh <fmt|mod|lint|test|vuln|arch|build>
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

MODULE="${MODULE:-github.com/admin-frame/admin-frame}"
GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-v2.14.0}"
GOIMPORTS_VERSION="${GOIMPORTS_VERSION:-v0.30.0}"
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.1.4}"

export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-sum.golang.google.cn}"
export PATH="$(go env GOPATH)/bin:${PATH}"

job="${1:-}"
if [[ -z "${job}" ]]; then
  echo "usage: $0 <fmt|mod|lint|test|vuln|arch|build>" >&2
  exit 2
fi

echo "==> go version: $(go version)"
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
    go test -race -count=1 -coverprofile=coverage.out ./...
    ;;
  vuln)
    go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"
    govulncheck ./...
    ;;
  arch)
    bash scripts/ci/check-transport-imports.sh
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
