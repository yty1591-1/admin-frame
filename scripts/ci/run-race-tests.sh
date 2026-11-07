#!/usr/bin/env bash
# 本地补跑 CI `test` job 的 -race 段。
#
# 为什么需要：`scripts/ci/run-tests.sh` 在缺少 CGO 工具链时会降级为「不带 -race」
# （Windows 无 gcc 即如此），于是本地绿、CI 用 -race 跑出数据竞争就红。
# 本脚本在 Linux 容器里跑同一批测试，得到与 ubuntu-latest 等价的竞态检测结果。
#
# 镜像按 go.mod 的 go 指令钉扎（CI-13），可用 RACE_IMAGE 覆盖。
# 用法: bash scripts/ci/run-race-tests.sh [package...]
#   RACE_PKGS 也可用环境变量指定（默认 ./...）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker 不可用；-race 需要 Linux 容器提供 CGO 工具链" >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "error: docker daemon 未运行" >&2
  exit 1
fi

go_version="$(awk '/^go [0-9]/{print $2; exit}' go.mod)"
if [[ -z "${go_version}" ]]; then
  echo "error: 无法从 go.mod 解析 go 版本" >&2
  exit 1
fi
go_minor="$(printf '%s' "${go_version}" | awk -F. '{print $1"."$2}')"
image="${RACE_IMAGE:-golang:${go_minor}}"

pkgs="${*:-${RACE_PKGS:-./...}}"

# Git Bash 会把 /src 之类的参数改写成 Windows 路径，这里统一转成 docker 认识的形态。
src="$(pwd)"
if command -v cygpath >/dev/null 2>&1; then
  src="$(cygpath -m "${src}")"
fi
export MSYS_NO_PATHCONV=1

echo "==> ${image} go test -race -count=1 ${pkgs}"
docker run --rm \
  -v "${src}:/src" \
  -w /src \
  -e CGO_ENABLED=1 \
  -e GOFLAGS="${GOFLAGS:-}" \
  "${image}" \
  go test -race -count=1 ${pkgs}

echo "OK: race tests"
