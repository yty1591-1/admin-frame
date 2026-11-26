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
# 钉扎到 go.mod 完整补丁号。仅用 major.minor（如 golang:1.26）时镜像可能滞后
# 一个补丁，在 GOTOOLCHAIN=local 下会直接拒绝编译（go 1.26.10 < go 1.26.11）。
image="${RACE_IMAGE:-golang:${go_version}}"

pkgs="${*:-${RACE_PKGS:-./...}}"

# Git Bash 会把 /src 之类的参数改写成 Windows 路径，这里统一转成 docker 认识的形态。
src="$(pwd)"
if command -v cygpath >/dev/null 2>&1; then
  src="$(cygpath -m "${src}")"
fi
export MSYS_NO_PATHCONV=1

# 容器默认 GOPROXY=proxy.golang.org；国内/受限网络会 dial refused。
# 与 docs/CI_STANDARDS.md、gitee-job.sh 对齐，默认 goproxy.cn，可被环境覆盖。
goproxy="${GOPROXY:-https://goproxy.cn,direct}"
gosumdb="${GOSUMDB:-sum.golang.google.cn}"
modcache="$(go env GOMODCACHE)"
modcache_args=()
if [[ -n "${modcache}" && -d "${modcache}" ]]; then
  modcache_args=(-v "${modcache}:/go/pkg/mod")
fi

echo "==> ${image} go test -race -count=1 ${pkgs}"
docker run --rm \
  -v "${src}:/src" \
  "${modcache_args[@]}" \
  -w /src \
  -e CGO_ENABLED=1 \
  -e GOFLAGS="${GOFLAGS:-}" \
  -e GOTOOLCHAIN=local \
  -e GOPROXY="${goproxy}" \
  -e GOSUMDB="${gosumdb}" \
  "${image}" \
  go test -race -count=1 ${pkgs}

echo "OK: race tests"
