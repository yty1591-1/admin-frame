#!/usr/bin/env bash
# ENT-06 T6：告警故障注入演练。
#
# 目的：验证 deploy/observability/prometheus/rules 下的规则能被真实指标触发
# （AC-ENT-06-2：5xx / DB 池耗尽 / Outbox 积压三类故障可触发对应告警）。
#
# 前置（见 docs/archive/verify/alert-drill-ent06.md）：
#   - kind 集群 af-verify，admin-frame 已部署（values-kind.yaml，auth.mode=demo）
#   - 单体 Prometheus 在 monitoring 命名空间（scripts/observability/install-kind-prometheus.sh）
#   - MySQL / Redis 在 admin-frame 命名空间
#
# 用法：
#   bash scripts/observability/alert-drill.sh 5xx      [--duration 720]
#   bash scripts/observability/alert-drill.sh db-pool  [--duration 720]
#   bash scripts/observability/alert-drill.sh outbox   [--duration 720]
#
# 每个子命令都会：注入故障 → 持续打流量 → 轮询告警状态 → 撤销故障 → 复查恢复。
# 退出码 0 = 目标告警在演练窗口内进入 firing；退出码 2 = 超时未触发。
set -uo pipefail

MODE="${1:-}"
NAMESPACE="${NAM_NAMESPACE:-admin-frame}"
DURATION=720
PROM_NS="${PROM_NS:-monitoring}"
PROM_DEPLOY="prometheus-standalone"
MYSQL_DEPLOY="mysql"
MYSQL_USER="root"
MYSQL_PASS="verifypass"
MYSQL_DB="admin_frame"
ADMIN_DEPLOY="af-kind-admin-frame-admin"
WORKER_DEPLOY="af-kind-admin-frame-worker"
TOKEN="admin.t6"
AUDIT_TABLE="audit_event"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --duration) DURATION="$2"; shift 2;;
    --namespace) NAMESPACE="$2"; shift 2;;
    *) shift;;
  esac
done

log() { echo "[$(date -u +%H:%M:%S)] $*"; }
fail() { echo "FAIL: $*" >&2; exit 2; }

# mysql_exec 执行 SQL；-N 表示不打印表头。
mysql_exec() {
  kubectl -n "${NAMESPACE}" exec deploy/"${MYSQL_DEPLOY}" -- \
    mysql -u"${MYSQL_USER}" -p"${MYSQL_PASS}" "${MYSQL_DB}" -N -e "$1" 2>/dev/null
}

# prom_query 查询 Prometheus HTTP API。
prom_query() {
  kubectl -n "${PROM_NS}" exec deploy/"${PROM_DEPLOY}" -- wget -qO- "$1" 2>/dev/null
}

# alert_state 输出「告警名:状态」列表。
alert_state() {
  prom_query 'http://127.0.0.1:9090/api/v1/alerts' | python -c "
import sys, json
try:
    data = json.load(sys.stdin)
except Exception:
    print('')
    raise SystemExit(0)
alerts = data.get('data', {}).get('alerts', [])
print(','.join(sorted(f\"{a['labels']['alertname']}:{a['state']}\" for a in alerts)))
" 2>/dev/null
}

# wait_alert 轮询直到目标告警进入 firing。
wait_alert() {
  local target="$1"
  local deadline=$((SECONDS + DURATION))
  while (( SECONDS < deadline )); do
    local state
    state="$(alert_state)"
    log "alerts: ${state}"
    if [[ "${state}" == *"${target}:firing"* ]]; then
      log "OK: ${target} 进入 firing"
      return 0
    fi
    sleep 20
  done
  fail "${target} 在 ${DURATION}s 内未进入 firing"
}

# audit_window 打印最近一小时的审计查询时间窗。
audit_window() {
  local from to
  from="$(date -u -d '-1 hour' +%Y-%m-%dT%H:%M:%SZ)"
  to="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "${from}" "${to}"
}

# drive_audit 在 admin-api 容器内并发打审计查询流量（触发 5xx / 占满连接池）。
drive_audit() {
  local workers="$1"
  local from to
  read -r from to <<<"$(audit_window)"
  kubectl -n "${NAMESPACE}" exec deploy/"${ADMIN_DEPLOY}" -- sh -c "
    for j in \$(seq 1 ${workers}); do
      (
        sleep \$((j % 3))
        i=0
        while [ \$i -lt 400 ]; do
          wget -qO /dev/null --header='Authorization: Bearer ${TOKEN}' \
            'http://127.0.0.1:8080/admin/v1/audit/events?from=${from}&to=${to}'
          i=\$((i+1))
        done
      ) &
    done
    wait" >/dev/null 2>&1 &
  echo $!
}

case "${MODE}" in
  5xx)
    log "注入：审计表改名 → 审计查询返回 500"
    mysql_exec "RENAME TABLE ${AUDIT_TABLE} TO ${AUDIT_TABLE}_drill;" || fail "改表失败"
    DRIVER_PID="$(drive_audit 1)"
    wait_alert "API5xxRateHigh"
    kill "${DRIVER_PID}" 2>/dev/null
    log "恢复：表名还原"
    mysql_exec "RENAME TABLE ${AUDIT_TABLE}_drill TO ${AUDIT_TABLE};" || fail "还原表名失败"
    ;;
  db-pool)
    log "注入：持有 ${AUDIT_TABLE} 元数据锁 → 连接池打满"
    mysql_exec "SET GLOBAL innodb_lock_wait_timeout = 3600;" || fail "设置锁等待超时失败"
    mysql_exec "LOCK TABLES ${AUDIT_TABLE} WRITE; SELECT SLEEP($((DURATION + 600)));" &
    LOCK_PID=$!
    sleep 3
    DRIVER_PID="$(drive_audit 12)"
    wait_alert "DBPoolExhaustion"
    kill "${DRIVER_PID}" 2>/dev/null
    log "释放：终止持锁会话"
    LOCK_SESSION="$(mysql_exec "SELECT id FROM information_schema.processlist WHERE info LIKE 'SELECT SLEEP%' LIMIT 1;")"
    [[ -n "${LOCK_SESSION}" ]] && mysql_exec "KILL ${LOCK_SESSION};" || true
    mysql_exec "SET GLOBAL innodb_lock_wait_timeout = 50;" || true
    ;;
  outbox)
    log "注入：worker 缩容到 0 → Outbox 无消费者"
    kubectl -n "${NAMESPACE}" scale deploy/"${WORKER_DEPLOY}" --replicas=0 >/dev/null || fail "worker 缩容失败"
    sleep 5
    log "造事件：批量创建 IAM 角色（每次写一条 iam.audit Outbox 事件）"
    # 事件生成命令整体用单引号传给容器内的 sh：JSON 里的双引号与循环变量都在
    # 容器内展开，避免宿主 shell 提前吃掉 $i（曾经导致 code 原样带着 "$i" 被服务端拒绝）。
    kubectl -n "${NAMESPACE}" exec deploy/"${ADMIN_DEPLOY}" -- sh -c '
      token="$1"
      base="$2"
      total="$3"
      i=1
      while [ "$i" -le "$total" ]; do
        body=$(printf "{\"channel\":\"admin\",\"scope_type\":\"platform\",\"code\":\"drill%s\",\"name\":\"drill %s\",\"reason\":\"alert drill\"}" "$i" "$i")
        wget -qO /dev/null --method=POST \
          --header="Authorization: Bearer $token" \
          --header="Content-Type: application/json" \
          --body-data="$body" \
          "$base/admin/v1/iam/roles"
        i=$((i+1))
      done' _ "${TOKEN}" "http://127.0.0.1:8080" 1200 >/dev/null 2>&1 &
    EVENT_PID=$!
    wait_alert "AsyncOutboxBacklog"
    kill "${EVENT_PID}" 2>/dev/null
    log "恢复：worker 副本还原"
    kubectl -n "${NAMESPACE}" scale deploy/"${WORKER_DEPLOY}" --replicas=1 >/dev/null || fail "worker 扩容失败"
    ;;
  *)
    echo "用法: bash scripts/observability/alert-drill.sh {5xx|db-pool|outbox} [--duration 秒]" >&2
    exit 1
    ;;
esac

log "演练结束：$(alert_state)"
