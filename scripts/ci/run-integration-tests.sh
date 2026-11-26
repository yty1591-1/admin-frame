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

MYSQL_PORT="${INTEGRATION_MYSQL_PORT:-3306}"
REDIS_PORT="${INTEGRATION_REDIS_PORT:-6379}"
MINIO_PORT="${INTEGRATION_MINIO_PORT:-9000}"
export TEST_MYSQL_DSN="app:app@tcp(127.0.0.1:${MYSQL_PORT})/admin_frame_test?parseTime=true&loc=UTC"
# 各域集成包使用独立 DSN 开关；值相同，指向 compose MySQL 测试库。
export IAM_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export TENANT_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export NOTIFY_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export FEATUREFLAG_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export CONFIGCENTER_TEST_MYSQL_DSN="${TEST_MYSQL_DSN}"
export TEST_REDIS_ADDR="127.0.0.1:${REDIS_PORT}"
export TEST_S3_ENDPOINT="http://127.0.0.1:${MINIO_PORT}"
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
for port in "${MYSQL_PORT}" "${REDIS_PORT}" "${MINIO_PORT}"; do wait_host_port "${port}"; done

echo "==> create test bucket"
docker compose -f "${COMPOSE_FILE}" run --rm minio-init

# 各包共用同一套 MySQL 实例，且 IAM 用例会清空 async 表；串行执行包避免相互清表。
echo "==> go test -count=1 -p 1 ./tests/integration/..."
go test -count=1 -p 1 ./tests/integration/...
