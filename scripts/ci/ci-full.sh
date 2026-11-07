#!/usr/bin/env bash
# 本地一条命令复现 GitHub CI 的「测试面」全集（CI-05 + CI-06 + Image 扫描）。
#
# 覆盖 make ci 覆盖不到的三处：
#   1. 真实 MySQL / Redis / MinIO —— 否则 IAM 总体覆盖率与 persistence 门禁静默消失；
#   2. tests/integration/... 全包串行（CI 的 integration job，与 test ci 不是同一批）；
#   3. -race 与镜像 Trivy 扫描（本地 CGO 缺失、Image workflow 不在 make ci 里）。
#
# 用法:
#   bash scripts/ci/ci-full.sh                # 全量
#   bash scripts/ci/ci-full.sh --skip-race    # 跳过 -race（加快到 ~20min）
#   bash scripts/ci/ci-full.sh --skip-image   # 跳过镜像构建与扫描
#   IMAGES="admin-api biz-api" ...            # 指定要扫的镜像（默认 admin-api 一个）
#   KEEP_INTEGRATION_STACK=1 ...              # 跑完保留容器，便于排查
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

COMPOSE_FILE="docker-compose.integration.yml"
SKIP_RACE=0
SKIP_IMAGE=0
SKIP_INTEGRATION=0
IMAGES="${IMAGES:-admin-api}"

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --skip-race) SKIP_RACE=1 ;;
    --skip-image) SKIP_IMAGE=1 ;;
    --skip-integration) SKIP_INTEGRATION=1 ;;
    --images=*) IMAGES="${1#*=}" ;;
    -h|--help) sed -n '2,20p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
  shift
done

if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  echo "error: ci-full 需要 docker + compose v2（它负责提供 CI 的 service 容器）" >&2
  echo "      只想跑不带容器的门禁请用: make ci-strict" >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "error: docker daemon 未运行" >&2
  exit 1
fi

elapsed() { printf '%dm%02ds' "$((SECONDS / 60))" "$((SECONDS % 60))"; }
step() { echo; echo "=== [$1] $2"; }

cleanup() {
  if [[ "${KEEP_INTEGRATION_STACK:-0}" == "1" ]]; then
    echo "==> 保留依赖容器（KEEP_INTEGRATION_STACK=1）"
    return
  fi
  docker compose -f "${COMPOSE_FILE}" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

step 1/5 "拉起 MySQL / Redis / MinIO"
docker compose -f "${COMPOSE_FILE}" up -d --wait --wait-timeout 180 mysql redis minio

# compose healthcheck 只证明容器内就绪；Windows Docker Desktop 的主机端口转发会晚几秒。
wait_host_port() {
  local port="$1" attempt
  for attempt in $(seq 1 30); do
    if (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null; then
      echo "    127.0.0.1:${port} reachable"
      return 0
    fi
    sleep 1
  done
  echo "error: 127.0.0.1:${port} 30s 内不可达" >&2
  return 1
}
for port in 3306 6379 9000; do wait_host_port "${port}"; done
docker compose -f "${COMPOSE_FILE}" run --rm minio-init >/dev/null

# 每个 step 的环境必须与该 step 对应的 CI job **完全一致**，不能图省事一次性全导出：
# GitHub 的 test job 只注入 4 个 DSN（services.mysql），Redis/S3 变量只属于 integration job。
# 多给的变量会让本应串行的集成用例在 `go test ./...` 的包级并行里互相踩库，
# 制造出 CI 根本不会出现的失败（2026-11-07 实测：multiinstance / tenant 三个用例假红）。
MYSQL_DSN="app:app@tcp(127.0.0.1:3306)/admin_frame_test?parseTime=true&loc=UTC"
REDIS_ADDR="127.0.0.1:6379"
S3_ENDPOINT="http://127.0.0.1:9000"
S3_BUCKET="admin-frame"

# 变更行覆盖率（P1-5）只有在拿到 base ref 时才运行，这里补上，避免它变成永久 SKIP。
for base in origin/main main; do
  if git rev-parse --verify --quiet "${base}" >/dev/null; then
    export GIT_BASE_REF="${base}"
    break
  fi
done

step 2/5 "make ci（严格模式：任何未豁免跳过即失败）"
# 对齐 CI `test` job 的 env（.github/workflows/ci.yml 的 jobs.test.env）。
IAM_TEST_MYSQL_DSN="${MYSQL_DSN}" \
  TENANT_TEST_MYSQL_DSN="${MYSQL_DSN}" \
  NOTIFY_TEST_MYSQL_DSN="${MYSQL_DSN}" \
  FEATUREFLAG_TEST_MYSQL_DSN="${MYSQL_DSN}" \
  CI_STRICT=1 bash scripts/ci/ci-run.sh ci

if [[ "${SKIP_INTEGRATION}" -eq 0 ]]; then
  step 3/5 "tests/integration/...（CI integration job，-p 1 串行）"
  # 对齐 scripts/ci/run-integration-tests.sh 的 env：只有这一步才注入 Redis / S3。
  TEST_MYSQL_DSN="${MYSQL_DSN}" \
    IAM_TEST_MYSQL_DSN="${MYSQL_DSN}" \
    TEST_REDIS_ADDR="${REDIS_ADDR}" \
    TEST_S3_ENDPOINT="${S3_ENDPOINT}" \
    TEST_S3_ACCESS_KEY="minioadmin" \
    TEST_S3_SECRET_KEY="minioadmin" \
    TEST_S3_BUCKET="${S3_BUCKET}" \
    go test -count=1 -p 1 ./tests/integration/...
else
  echo; echo "=== [3/5] integration 已跳过（--skip-integration）"
fi

if [[ "${SKIP_RACE}" -eq 0 ]]; then
  step 4/5 "-race（CI test job 的竞态检测）"
  bash scripts/ci/run-race-tests.sh
else
  echo; echo "=== [4/5] -race 已跳过（--skip-race）"
fi

if [[ "${SKIP_IMAGE}" -eq 0 ]]; then
  step 5/5 "镜像 Trivy 扫描（CI image job）: ${IMAGES}"
  IMAGES="${IMAGES}" bash scripts/ci/check-image-scan.sh
else
  echo; echo "=== [5/5] 镜像扫描已跳过（--skip-image）"
fi

echo
echo "OK: ci-full 通过（$(elapsed)）"
