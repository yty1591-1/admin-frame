#!/usr/bin/env bash
# 本地复现 Image workflow 的 Trivy 扫描（镜像级 HIGH/CRITICAL 门禁）。
#
# 为什么需要：`trivy` 扫的是**镜像内二进制的模块清单与 OS 包**，而 `make vuln`
# 的 govulncheck 只看代码可达漏洞——两者口径不同。2026-11-07 的 Image job 因
# golang.org/x/net CVE-2026-78661(HIGH) 五连红，而本地 `make vuln` 全绿。
#
# 与 CI 对齐的参数：severity=CRITICAL,HIGH、ignore-unfixed、命中即 exit 1；
# trivy 版本按 CI 的 trivy-action 钉扎（CI-13），可用 TRIVY_IMAGE 覆盖。
#
# 用法: bash scripts/ci/check-image-scan.sh
#   IMAGES="admin-api biz-api"  默认只扫 admin-api（全量五镜像较慢）
#   SCAN_KEEP=1                 保留构建出的本地镜像
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

TRIVY_IMAGE="${TRIVY_IMAGE:-aquasec/trivy:0.70.0}"
IMAGES="${IMAGES:-admin-api}"
TAG="${SCAN_TAG:-ci-scan}"
# mirror.gcr.io / ghcr 在部分网络下会超时或 EOF；允许用 TRIVY_* 覆盖。
TRIVY_TIMEOUT="${TRIVY_TIMEOUT:-15m}"
TRIVY_DB_REPOSITORY="${TRIVY_DB_REPOSITORY:-public.ecr.aws/aquasecurity/trivy-db:2}"
TRIVY_JAVA_DB_REPOSITORY="${TRIVY_JAVA_DB_REPOSITORY:-public.ecr.aws/aquasecurity/trivy-java-db:1}"

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker 不可用；镜像扫描依赖 docker build" >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "error: docker daemon 未运行" >&2
  exit 1
fi

# Git Bash 会改写 /var/run/docker.sock 这类参数，关掉路径转换。
export MSYS_NO_PATHCONV=1

built=()
cleanup() {
  [[ "${SCAN_KEEP:-0}" == "1" ]] && return
  for img in "${built[@]}"; do
    docker rmi "${img}" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT

FAIL=0
for cmd in ${IMAGES}; do
  if [[ ! -d "cmd/${cmd}" ]]; then
    echo "error: cmd/${cmd} 不存在（可选值见 Dockerfile 的 target）" >&2
    exit 1
  fi
  img="admin-frame/${cmd}:${TAG}"
  echo "==> docker build --target ${cmd}"
  docker build --target "${cmd}" --build-arg CMD="${cmd}" -t "${img}" . >/dev/null
  built+=("${img}")

  echo "==> trivy image ${img} (CRITICAL,HIGH, ignore-unfixed)"
  if ! docker run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v trivy-ci-cache:/root/.cache/ \
    -e TRIVY_TIMEOUT="${TRIVY_TIMEOUT}" \
    -e TRIVY_DB_REPOSITORY="${TRIVY_DB_REPOSITORY}" \
    -e TRIVY_JAVA_DB_REPOSITORY="${TRIVY_JAVA_DB_REPOSITORY}" \
    "${TRIVY_IMAGE}" image \
    --scanners vuln \
    --severity CRITICAL,HIGH \
    --ignore-unfixed \
    --exit-code 1 \
    --timeout "${TRIVY_TIMEOUT}" \
    --no-progress \
    "${img}"; then
    echo "    ERROR: ${cmd} 命中 HIGH/CRITICAL 漏洞或扫描失败（RELS-06）" >&2
    FAIL=1
  fi
done

if [[ "${FAIL}" -ne 0 ]]; then
  echo "image scan failed" >&2
  exit 1
fi
echo "OK: image scan"
