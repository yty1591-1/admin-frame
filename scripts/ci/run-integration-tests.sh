#!/usr/bin/env bash
# Wave 0（ENT-16 前置切片）：集成测试入口（Tier B，不并入 make ci）。
#
# 依赖 Docker + Compose v2；缺失时打印 SKIP 并以 0 退出，保证本地可空跑。
# 仅使用 compose 中的测试默认凭据，不注入任何生产 secret（TEST-05 / CI-12）。
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

if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  echo "SKIP: docker compose v2 not available; integration tests skipped" >&2
  exit 0
fi

COMPOSE_FILE="docker-compose.integration.yml"

# 端口与 docker-compose.integration.yml 保持固定一致；compose 不再读取环境变量覆盖。
export TEST_MYSQL_DSN="app:app@tcp(127.0.0.1:3306)/admin_frame_test?parseTime=true&loc=UTC"
# 兼容既有 tests/integration/iam（沿用其 IAM_TEST_MYSQL_DSN 开关，避免复制测试代码）。
export IAM_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export TEST_REDIS_ADDR="127.0.0.1:6379"
export TEST_S3_ENDPOINT="http://127.0.0.1:9000"
export TEST_S3_ACCESS_KEY="minioadmin"
export TEST_S3_SECRET_KEY="minioadmin"
export TEST_S3_BUCKET="admin-frame"

cleanup() {
  if [[ "${KEEP_INTEGRATION_STACK:-0}" == "1" ]]; then
    echo "==> keep integration stack running (KEEP_INTEGRATION_STACK=1)"
    return
  fi
  docker compose -f "${COMPOSE_FILE}" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> docker compose up: mysql / redis / minio"
docker compose -f "${COMPOSE_FILE}" up -d --wait --wait-timeout 120 mysql redis minio

# compose 的 healthcheck 只证明「容器内」就绪。Windows Docker Desktop 的主机端口
# 转发会晚几秒生效，测试进程连的是 127.0.0.1，此时会拿到 connection refused
# （2026-11-06 实测：Healthy 之后立刻 go test，9 个包全部报 3306 拒绝连接）。
# 显式等主机侧 TCP 可连再继续；Linux 第一次探测即成功，不影响 CI。
wait_host_port() {
  local port="$1" attempt
  for attempt in $(seq 1 30); do
    if (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null; then
      echo "    OK: 127.0.0.1:${port} reachable"
      return 0
    fi
    sleep 1
  done
  # 探测手段本身可能在某些 shell 下不可用（非 bash 或缺 net-redirections）。
  # 这时不该把「探测不到」误判成「依赖没起来」：给警告并交给 go test 给出真实错误。
  echo "WARN: 127.0.0.1:${port} not reachable after 30s (host forwarding or /dev/tcp unsupported)" >&2
}

echo "==> wait for host-side port forwarding"
for port in 3306 6379 9000; do wait_host_port "${port}"; done

echo "==> create test bucket"
docker compose -f "${COMPOSE_FILE}" run --rm minio-init

# 各包共用同一套 MySQL 实例，且 IAM 用例会清空 async 表；串行执行包避免相互清表。
echo "==> go test -count=1 -p 1 ./tests/integration/..."
go test -count=1 -p 1 ./tests/integration/...
