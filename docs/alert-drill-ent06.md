# ENT-06 T6：告警故障注入演练（AC-ENT-06-2）

## 1. 目的与验收对应

`ALERTS.md` 里的告警必须有真实指标支撑。本演练在 kind 真集群上注入三类故障，
逐条验证规则能被触发，并在故障撤销后确认告警自动恢复：

| 注入 | 目标告警 | 触发条件（规则） | 恢复验证 |
|---|---|---|---|
| 审计表改名 → 查询 500 | `API5xxRateHigh`（P1） | 5xx 比率 > 1%，`for: 5m` | 告警在流量停止后 5 分钟内退回 inactive |
| 持有元数据锁 → 连接池打满 | `DBPoolExhaustion`（P1） | `in_use/max_open > 0.9`，`for: 5m` | 锁释放后指标回落 |
| worker 缩容 → Outbox 无消费者 | `AsyncOutboxBacklog`（P1） | `pending > 1000` 或 `lag > 300s`，`for: 10m` | worker 恢复后积压被清空 |

配套门禁：`make check-alerts`（`ALERTS.md` ↔ 规则 ↔ runbook 三方一致）。

## 2. 环境

```bash
# 集群
kind create cluster --name af-verify --image kindest/node:v1.34.3 --wait 300s
kind export kubeconfig --name af-verify

# 应用镜像（本地构建 → 导入节点）
for t in admin-api biz-api c-api worker migrate; do
  docker build --target $t --build-arg CMD=$t -t admin-frame/$t:verify .
done
kind load docker-image admin-frame/{admin-api,biz-api,c-api,worker,migrate}:verify --name af-verify

# MySQL / Redis / Prometheus 镜像（多平台 manifest，kind load 会失败）
for img in mysql:8.0 redis:7-alpine prom/prometheus:v3.1.0; do
  safe=$(echo "$img" | tr ':/' '__')
  docker save "$img" -o "/tmp/$safe.tar"
  docker cp "/tmp/$safe.tar" "af-verify-control-plane:/root/$safe.tar"
  docker exec af-verify-control-plane ctr -n k8s.io images import "/root/$safe.tar"
done

# 依赖与命名空间（MySQL / Redis 清单见 docs/deploy-kind-verify.md）
kubectl create namespace admin-frame
kubectl -n admin-frame create secret generic admin-frame-db \
  --from-literal=dsn="root:verifypass@tcp(mysql.admin-frame.svc.cluster.local:3306)/admin_frame?parseTime=true&loc=UTC&charset=utf8mb4"
kubectl -n admin-frame create secret generic admin-frame-redis --from-literal=password=""

# 规则与抓取（单体 Prometheus，不需要 Operator）
bash scripts/observability/install-kind-prometheus.sh

# 应用
helm install af-kind deploy/helm/admin-frame -f deploy/helm/admin-frame/values-kind.yaml \
  --set config.auth.mode=demo -n admin-frame --timeout 8m
```

`auth.mode=demo` 只用于本地：`demo` 令牌形如 `admin.t6`（通道.主体），`iam.mode=shadow`
下 IAM 只评估不拦截，因此本地造流量不必准备真实 JWT 与 IAM 绑定。

## 3. 演练脚本

```bash
bash scripts/observability/alert-drill.sh 5xx      # 审计查询 500
bash scripts/observability/alert-drill.sh db-pool  # 连接池耗尽
bash scripts/observability/alert-drill.sh outbox   # Outbox 积压
```

脚本做四件事：注入故障 → 持续打流量 → 轮询告警直到 `firing` → 撤销故障并复查。
退出码 `0` 表示目标告警在窗口内触发，`2` 表示超时未触发。

## 4. 2026-11-02 演练结果

环境：kind `af-verify`（单节点）、`values-kind.yaml`（`db.max_open_conns=6`、
`iam.mode=shadow`、`async.enabled=true`、worker 1 副本）、单体 Prometheus v3.1.0
（抓取 4 个应用 Pod，规则文件与 `deploy/observability/prometheus/rules` 同源）。

| 注入 | 结果 | 证据 |
|---|---|---|
| 5xx | `API5xxRateHigh` **firing**（service=admin-api，实测 5xx 比率 0.99） | 撤销后 5 分 27 秒退回 inactive |
| DB 池耗尽 | `DBPoolExhaustion` **firing**（in_use=6 / max=6），`DBPoolWaitHigh` 同时 firing | 首次尝试失败，根因见 §5.1，修复后复测通过 |
| Outbox 积压 | `AsyncOutboxBacklog` **firing**（pending=1200，14:52:30 进入 pending，15:03:07 firing） | worker 恢复后 1201 条事件全部转 `succeeded` |

复现过程中的两个实测细节：

- **锁等待超时要一起调**：`innodb_lock_wait_timeout` 默认 50s，8 个并发请求会在同一时刻超时，
  连接池出现采样空档（`in_use` 掉到 0/4），`for: 5m` 的连续条件被打断、告警反复重置。
  演练把该值调到 3600s 并用 12 个错峰 worker 压池，条件才连续成立。
- **释放锁要杀会话，不能只杀客户端**：杀掉 `kubectl exec` 的客户端后，MySQL 侧的
  `SELECT SLEEP()` 仍在跑，表锁继续持有（进程列表里仍是 `Waiting for table metadata lock`）。
  正确做法是 `KILL <connection_id>`。

## 5. 本轮发现

### 5.1 P1：DB 池打满时，Prometheus 抓不到指标，`DBPoolExhaustion` 永远不响

**现象**：把连接池打满后，`db_pool_in_use_connections` 从 Prometheus 里消失
（查询返回空），`DBPoolExhaustion` 连 `pending` 都不出现；而 MySQL 侧明确有 31 个
`Waiting for table metadata lock` 的查询。

**根因**：`/metrics` 的 GaugeFunc 在抓取时同步查库，且每个 Gauge 各查一次：

- `async_outbox_pending` / `async_outbox_lag_seconds` / `async_dlq_depth`：各 3s 超时 → 合计 9s
- `iam_outbox_pending` / `iam_outbox_lag_seconds`：各 1s 超时 → 合计 2s

DB 池打满时这些查询全部要等连接，最坏耗时约 11s > Prometheus 默认 `scrape_timeout`（10s）
→ 抓取失败 → **该进程的全部指标一起丢失**，包括本来能反映池状态的 `db_pool_*`。
告警因此「自噬」：越需要它的时候越拿不到数据。

**修复**：新增 `pkg/metrics.SnapshotCache`，把需要访问外部依赖的采集结果缓存下来
（TTL 2s，失败也按 TTL 节流，并保留上一次成功快照）。平台 async 与 IAM / identity
的 Outbox Gauge 改为共用一份快照，抓取周期内只查一次库，最坏耗时降到约 3s。

**回归门禁**：`TestAsyncMetricsGaugesShareOneSnapshot`（三个 Gauge 只查一次库）、
`TestAsyncMetricsGaugesSurviveSlowStats`（采集失败时 Gather 仍返回指标）、
`pkg/metrics` 的快照缓存单测。

### 5.2 P1：`db.max_open_conns` 在代码里不存在，池大小无法按环境调整

`platform.OpenDatabase` 只传 `Driver` + `DSN`，连接池上限固定取 `pkg/db` 默认值 25；
而 `db_metrics.go` 的指标说明却写着「由 db.max_open_conns 派生」，仓库里根本没有这个配置项。
后果：容量无法按环境规划，`DBPoolExhaustion` 的分母锁死在 25，故障注入只能靠 23+ 并发硬顶。

**修复**：`DBConfig` 新增 `max_open_conns` / `max_idle_conns` / `conn_max_lifetime`
（0 = 沿用默认），`validateDB` 拒绝负值与 `idle > open`，`values-kind.yaml` 取 6/3。

### 5.3 观察项：影子期 IAM 不一致计数会随流量持续增长

`IAMShadowMismatch` 的表达式是 `sum(increase(iam_shadow_mismatch_total[1h])) > 0`。
本地用 demo 令牌访问（IAM 里没有对应绑定）时，每次请求都算一次不一致，1 小时内累计
181 次即触发 P2。这是规则的设计意图（切换 enforce 前必须归零），但**在有流量、
无 IAM 绑定的环境里会长期常亮**，值班需要能区分「真不一致」与「影子期正常流量」。
