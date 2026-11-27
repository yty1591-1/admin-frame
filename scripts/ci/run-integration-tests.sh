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

# 共享 runner 上的隔离（2026-11-26 Gitea 实测）：
# Act Runner 让多个 job 共用宿主 docker，compose 文件里的
# `name: admin-frame-integration` 与默认 3306 会让并发 job 互相撞车 ——
#   Bind for 127.0.0.1:3306 failed: port is already allocated
# 因此在 CI 里改用唯一 project name（-p）+ 临时端口。
#
# 端口不能钉死成某个固定值：同一 runner 上可能同时跑多个 workflow（如 ci.yml 与
# image.yml），固定 13306 只是把 3306 的冲突推迟到另一组进程之间。这里用
# 「随机基数 + 探测空闲」取端口，冲突概率降到可忽略。
# 本地保持原行为（固定 name、默认端口），方便联调时 `docker compose ps` 直接看到。
port_free() {
  ! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}
# 注意：pick_port 用「设置全局 PICK_PORT 再读取」而不是 echo + $()，
# 因为 $( ) 是子 shell，里面记录的已选端口集合不会回传，会导致两个服务取到同一端口。
PICKED_PORTS=" "
PICK_PORT=""
pick_port() {
  local base="$1" candidate attempt
  for attempt in $(seq 1 50); do
    candidate=$((base + RANDOM % 400))
    if [[ "${PICKED_PORTS}" != *" ${candidate} "* ]] && port_free "${candidate}"; then
      PICKED_PORTS="${PICKED_PORTS}${candidate} "
      PICK_PORT="${candidate}"
      return 0
    fi
  done
  # 兜底：退回基数（让 compose 自己报出真实错误，而不是静默用错端口）。
  PICK_PORT="${base}"
}

if [[ -n "${CI:-}" ]]; then
  PROJECT_NAME="${COMPOSE_PROJECT_NAME:-admin-frame-integration-${GITHUB_RUN_ID:-$$}}"
  MYSQL_PORT="${INTEGRATION_MYSQL_PORT:-}"
  if [[ -z "${MYSQL_PORT}" ]]; then pick_port 13306; MYSQL_PORT="${PICK_PORT}"; fi
  REDIS_PORT="${INTEGRATION_REDIS_PORT:-}"
  if [[ -z "${REDIS_PORT}" ]]; then pick_port 16379; REDIS_PORT="${PICK_PORT}"; fi
  MINIO_PORT="${INTEGRATION_MINIO_PORT:-}"
  if [[ -z "${MINIO_PORT}" ]]; then pick_port 19000; MINIO_PORT="${PICK_PORT}"; fi
  MINIO_CONSOLE_PORT="${INTEGRATION_MINIO_CONSOLE_PORT:-}"
  if [[ -z "${MINIO_CONSOLE_PORT}" ]]; then pick_port 19500; MINIO_CONSOLE_PORT="${PICK_PORT}"; fi
else
  PROJECT_NAME="${COMPOSE_PROJECT_NAME:-}"
  MYSQL_PORT="${INTEGRATION_MYSQL_PORT:-3306}"
  REDIS_PORT="${INTEGRATION_REDIS_PORT:-6379}"
  MINIO_PORT="${INTEGRATION_MINIO_PORT:-9000}"
  MINIO_CONSOLE_PORT="${INTEGRATION_MINIO_CONSOLE_PORT:-9001}"
fi
# 端口以导出变量交给 compose 做 `${VAR:-default}` 插值；-p 覆盖文件里的 name。
export INTEGRATION_MYSQL_PORT="${MYSQL_PORT}"
export INTEGRATION_REDIS_PORT="${REDIS_PORT}"
export INTEGRATION_MINIO_PORT="${MINIO_PORT}"
export INTEGRATION_MINIO_CONSOLE_PORT="${MINIO_CONSOLE_PORT}"

compose() {
  if [[ -n "${PROJECT_NAME}" ]]; then
    docker compose -p "${PROJECT_NAME}" -f "${COMPOSE_FILE}" "$@"
  else
    docker compose -f "${COMPOSE_FILE}" "$@"
  fi
}

# ---------------------------------------------------------------------------
# 寻址方式：分「job 跑在容器里」与「job 直接跑在宿主/VM 上」两种。
#
# Gitea Act Runner 用 `docker create ... runner-images:ubuntu-latest` 把每个 job
# 放进容器执行（2026-11-27 日志实测）。此时 compose 把端口发布在**宿主**上，
# 而 job 容器里的 127.0.0.1 是它自己 —— 于是即使容器 Healthy、端口也映射成功，
# 测试仍然全部 connection refused：
#   WARN: 127.0.0.1:13638 not reachable after 30s
#   dial tcp 127.0.0.1:13638: connect: connection refused
# 这不是端口冲突（随机端口已避开），而是网络命名空间不通。
#
# 对策：把 compose 的默认网络接到 job 容器上，之后用服务名（mysql/redis/minio）
# 走 Docker 内建 DNS 寻址，完全绕开宿主端口映射。
# GitHub hosted runner 与本地开发都不是这种形态（127.0.0.1 直连可用），
# 保持原路径，不引入额外依赖。
# ---------------------------------------------------------------------------
inside_container() {
  [[ -f /.dockerenv ]] && return 0
  grep -qaE '(docker|containerd|kubepods)' /proc/1/cgroup 2>/dev/null && return 0
  return 1
}

SERVICE_MODE="host"   # host=127.0.0.1 + 宿主端口；network=Docker 服务名 + 容器内端口
MYSQL_HOST="127.0.0.1"
REDIS_HOST="127.0.0.1"
MINIO_HOST="127.0.0.1"
MYSQL_ACCESS_PORT="${MYSQL_PORT}"
REDIS_ACCESS_PORT="${REDIS_PORT}"
MINIO_ACCESS_PORT="${MINIO_PORT}"
COMPOSE_NETWORK=""

if inside_container; then
  SERVICE_MODE="network"
  # 容器内端口是 compose 文件里写死的服务端口，与宿主映射端口无关。
  MYSQL_HOST="mysql"; MYSQL_ACCESS_PORT="3306"
  REDIS_HOST="redis"; REDIS_ACCESS_PORT="6379"
  MINIO_HOST="minio"; MINIO_ACCESS_PORT="9000"
  if [[ -n "${PROJECT_NAME}" ]]; then
    COMPOSE_NETWORK="${PROJECT_NAME}_default"
  else
    # 文件里 name: admin-frame-integration 时，默认网络名即 <name>_default。
    COMPOSE_NETWORK="admin-frame-integration_default"
  fi
  echo "==> job 运行在容器内：集成服务改走 Docker 网络 ${COMPOSE_NETWORK}（服务名寻址）"
fi

export TEST_MYSQL_DSN="app:app@tcp(${MYSQL_HOST}:${MYSQL_ACCESS_PORT})/admin_frame_test?parseTime=true&loc=UTC"
# 各域集成包使用独立 DSN 开关；值相同，指向 compose MySQL 测试库。
export IAM_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export TENANT_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export NOTIFY_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export FEATUREFLAG_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export CONFIGCENTER_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export TEST_REDIS_ADDR="${REDIS_HOST}:${REDIS_ACCESS_PORT}"
export TEST_S3_ENDPOINT="http://${MINIO_HOST}:${MINIO_ACCESS_PORT}"
export TEST_S3_ACCESS_KEY="minioadmin"
export TEST_S3_SECRET_KEY="minioadmin"
export TEST_S3_BUCKET="admin-frame"

# 把 compose 网络接到当前 job 容器，使服务名可解析。
# 失败必须硬退出：否则测试会以 connection refused 的形式报错，
# 错误信息指向被测代码而不是接线问题（正是本轮踩过的坑）。
join_compose_network() {
  [[ "${SERVICE_MODE}" == "network" ]] || return 0
  local self
  self="$(hostname)"   # act 的 job 容器 hostname 即容器 ID
  if ! docker network inspect "${COMPOSE_NETWORK}" >/dev/null 2>&1; then
    echo "ERROR: compose 网络 ${COMPOSE_NETWORK} 不存在，无法为 job 容器接线" >&2
    return 1
  fi
  if docker network connect "${COMPOSE_NETWORK}" "${self}" 2>/dev/null; then
    echo "    OK: job 容器 ${self} 已接入 ${COMPOSE_NETWORK}"
    return 0
  fi
  # 已接入（重复执行）时 connect 会报错，用 inspect 复核后视为成功。
  if docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' "${self}" 2>/dev/null |
    grep -qw "${COMPOSE_NETWORK}"; then
    echo "    OK: job 容器 ${self} 已在 ${COMPOSE_NETWORK} 中"
    return 0
  fi
  echo "ERROR: 无法把 job 容器 ${self} 接入 ${COMPOSE_NETWORK}" >&2
  return 1
}

cleanup() {
  if [[ "${KEEP_INTEGRATION_STACK:-0}" == "1" ]]; then
    echo "==> keep integration stack running (KEEP_INTEGRATION_STACK=1)"
    return
  fi
  # 容器内模式下 job 容器挂在这个网络上，不先断开则 `compose down` 删不掉网络。
  if [[ "${SERVICE_MODE:-host}" == "network" ]]; then
    docker network disconnect -f "${COMPOSE_NETWORK}" "$(hostname)" >/dev/null 2>&1 || true
  fi
  compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# S5-5：集成测试清单门禁（先于 go test 执行）。
#
# 为什么要先做存在性断言：`go test ./tests/integration/...` 对「测试被删」
# 的天然结果是「跑得更少、照样 PASS」——CI integration job 不会因此变红，
# 删掉 SQL 租户隔离 / Outbox 断电不丢等关键用例可以静默合并。清单断言把
# 「删测试」变成确定性失败，与 S5-5 退出条件（移除集成测试能阻断合并）对应。
#
# 三层断言（grep 单包计数，纯文本、无运行时推断）：
#   a. 包级：tests/integration 下每个包必须仍有 *_test.go（整包删除必炸）；
#   b. 用例级：关键场景用例点名存在（单文件删除也炸）；
#   c. 数量级：测试函数总数不得低于基线（防「只删一半」的温和侵蚀）。
# 基线调整走 PR：修改 REQUIRED_* 与 MIN 时必须在演练记录/计划中说明理由。
# ---------------------------------------------------------------------------
REQUIRED_INTEGRATION_PKGS=(async audit featureflag iam identity multiinstance notification smoke tenant)

# 关键场景清单：租户隔离 / 乐观锁 / 唯一约束 / 主体保护 / 事务回滚 /
# 断电不丢 / 保留期 / 冒烟迁移 / 令牌重放 / fail-closed / 复核扫描。
REQUIRED_INTEGRATION_TESTS=(
  TestSQLBindingRepositoryTenantIsolation
  TestSQLRoleRepositoryOptimisticLock
  TestSQLRoleRepositoryUniqueCode
  TestSQLAccountSubjectProtection
  TestSQLSessionArchiveSameTransaction
  TestSQLUnitOfWorkRollsBackRoleWhenOutboxFails
  TestCertificationSweepFullFlowOnMySQL
  TestRefreshReuseRevokesFamily
  TestRevocationFailClosed
  TestSQLOAuthSubjectProtection
  TestOutboxRestartNoLossNoDuplicate
  TestAuditRetentionArchivesThenDeletes
  TestSmokeMySQLMigration
  TestSeatLimitConcurrencyOnMySQL
  TestDeliveryRepositoryReceiptLookup
)
MIN_INTEGRATION_TESTS="${TEST_INTEGRATION_MIN:-70}"

INV_FAIL=0
echo "==> S5-5 inventory: package-level presence"
for pkg in "${REQUIRED_INTEGRATION_PKGS[@]}"; do
  found=0
  for f in tests/integration/"${pkg}"/*_test.go; do
    [[ -f "${f}" ]] && found=1 && break
  done
  if [[ "${found}" -eq 0 ]]; then
    echo "ERROR: tests/integration/${pkg} has no *_test.go left (S5-5)" >&2
    INV_FAIL=1
  else
    echo "    OK: tests/integration/${pkg}"
  fi
done

echo "==> S5-5 inventory: required scenario tests"
for name in "${REQUIRED_INTEGRATION_TESTS[@]}"; do
  if grep -rqs "^func ${name}(" tests/integration --include=*_test.go; then
    echo "    OK: ${name}"
  else
    echo "ERROR: required integration test ${name} is missing (S5-5)" >&2
    INV_FAIL=1
  fi
done

echo "==> S5-5 inventory: total count >= ${MIN_INTEGRATION_TESTS}"
TOTAL_TESTS="$(grep -rh '^func Test' tests/integration --include=*_test.go | wc -l)"
if [[ "${TOTAL_TESTS}" -lt "${MIN_INTEGRATION_TESTS}" ]]; then
  echo "ERROR: integration test count ${TOTAL_TESTS} < baseline ${MIN_INTEGRATION_TESTS} (S5-5)" >&2
  INV_FAIL=1
else
  echo "    OK: ${TOTAL_TESTS} test functions"
fi
if [[ "${INV_FAIL}" -ne 0 ]]; then
  echo "ERROR: integration test inventory check failed; merge blocked (S5-5)" >&2
  exit 1
fi

echo "==> docker compose up: mysql / redis / minio"
compose up -d --wait --wait-timeout 120 mysql redis minio

# 探测目标与实际寻址方式一致：容器内走服务名 + 容器端口，宿主走 127.0.0.1 + 映射端口。
# compose 的 healthcheck 只证明「容器内」就绪；Windows Docker Desktop 的主机端口
# 转发会晚几秒生效，测试进程连的是 127.0.0.1，此时会拿到 connection refused
# （2026-11-06 实测：Healthy 之后立刻 go test，9 个包全部报 3306 拒绝连接）。
# Linux 第一次探测即成功，不影响 CI。
wait_host_port() {
  local host="$1" port="$2" attempt
  for attempt in $(seq 1 30); do
    if (exec 3<>"/dev/tcp/${host}/${port}") 2>/dev/null; then
      echo "    OK: ${host}:${port} reachable"
      return 0
    fi
    sleep 1
  done
  # 探测手段本身可能在某些 shell 下不可用（非 bash 或缺 net-redirections）。
  # 这时不该把「探测不到」误判成「依赖没起来」：给警告并交给 go test 给出真实错误。
  echo "WARN: ${host}:${port} not reachable after 30s (host forwarding or /dev/tcp unsupported)" >&2
}

# 容器内模式下必须先接线：否则服务名不可解析，探测必然失败。
if [[ "${SERVICE_MODE}" == "network" ]]; then
  echo "==> join compose network"
  join_compose_network
fi

echo "==> wait for services reachable"
wait_host_port "${MYSQL_HOST}" "${MYSQL_ACCESS_PORT}"
wait_host_port "${REDIS_HOST}" "${REDIS_ACCESS_PORT}"
wait_host_port "${MINIO_HOST}" "${MINIO_ACCESS_PORT}"

echo "==> create test bucket"
compose run --rm minio-init

# 各包共用同一套 MySQL 实例，且 IAM 用例会清空 async 表；串行执行包避免相互清表。
echo "==> go test -count=1 -p 1 ./tests/integration/..."
go test -count=1 -p 1 ./tests/integration/...
