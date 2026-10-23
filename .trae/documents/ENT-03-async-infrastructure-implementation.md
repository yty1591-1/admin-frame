# ENT-03 通用异步基础设施实施计划

> **来源**：`docs/production-readiness-implementation-plan.md` §ENT-03（T1～T8）
> **前置**：ENT-02 已关闭（多实例 ID / 幂等 / 限流 / worker 发布器）
> **本轮决策**：T7 仅预留 broker 接口（不引入 NATS/Kafka）；T8 将 IAM Outbox 完整迁移到通用实现；按两阶段交付
> **最后更新**：2026-10-22

---

## 一、背景与目标从

IAM 已有一套经生产验证的事务性 Outbox（`iam_outbox` + 条件更新认领 + 租约回收 + 指数退避 + 毒消息），但它绑定在 IAM 装配内，未来业务域无法复用。`pkg/mq` 只有内存 Broker 与初版 `OutboxRelay`，缺少：

- 统一事件 envelope（`event_id/event_type/version/tenant_id/occurred_at/trace_id/idempotency_key`）；
- 可复用的租约化 Outbox Store、并发 Relay、死信与积压指标；
- 消费运行时（handler 注册、至少一次、幂等、并发、优雅退出）；
- worker 跨进程消费 API 写入事件的能力。

目标：把 IAM 已验证的可靠性语义沉淀为 `pkg/outbox` + `pkg/mq` 信封，让新业务域只声明「事件类型 + handler」即可接入。

## 二、现状（复用而非重建）

| 能力 | 现有位置 | 处理方式 |
|---|---|---|
| 消息信封 / Header / 重试分类 | `pkg/mq/message.go`、`headers.go`、`disposition.go` | 扩展 envelope，不重写 |
| 内存 Broker | `pkg/mq/memory.go` | 保留，作为单测/本地驱动 |
| Outbox 初版接口 | `pkg/mq/outbox.go`（`OutboxStore`/`OutboxRelay`） | 由 `pkg/outbox` 取代；`pkg/mq` 中旧实现删除，避免两套语义 |
| IAM Outbox 状态机/仓储/发布器/指标 | `internal/iam/domain/outbox.go`、`internal/iam/app/outbox.go`、`internal/iam/infrastructure/persistence/mysql_outbox.go`、`cmd/internal/iamruntime/outbox.go`、`internal/iam/observability/metrics.go` | 完整迁移到 `pkg/outbox` + IAM 仅保留审计投递适配 |
| 指标注册 | `pkg/metrics`、`internal/iam/observability` | 新增 platform 级 `async_*` 指标 |
| 测试基座 | `pkg/testkit`、`tests/integration/*` | 复用 MySQL/Redis 容器与串行执行 |

## 三、目标结构

```text
pkg/mq/
  envelope.go        # Envelope：event_id/event_type/version/tenant_id/occurred_at/trace_id/idempotency_key
  headers.go         # 新增 mq.event_id / mq.event_type / mq.event_version / mq.tenant_id / mq.occurred_at / mq.idempotency_key
  broker.go          # 预留 Broker 接口（组合 Producer/Consumer），本轮不提供 NATS 实现

pkg/outbox/
  event.go           # Event（Envelope + aggregate + 投递状态机）
  policy.go          # RetryPolicy（Base/Max/MaxAttempts，指数退避）
  store.go           # Store 接口 + Stats
  relay.go           # Relay：并发认领、租约回收、退避、死信、优雅退出
  router.go          # Handler 注册表：topic → handler，未知 topic 永久失败
  dedupe.go          # Deduper（至少一次 + 消费幂等包装）
  memory.go          # MemoryStore / MemoryDeduper（单测）
  mysql.go           # MySQLStore / MySQLDeduper（async_outbox / async_dlq / async_consumed）
  doc.go

internal/platform/
  config.go          # async 配置段 + Validate + 默认值
  async.go           # 通用 Store/Relay/Runtime 装配（不 import 业务域）
  async_metrics.go   # async_* 指标与 GaugeFunc
```

分层约束：`pkg/outbox` 不 import `internal/`；`internal/platform` 不 import 业务域；IAM handler 由 `cmd/internal/iamruntime` 注册。

## 四、数据模型与迁移

### 阶段一迁移 `migrations/000009_create_async_outbox.{up,down}.sql`

- `async_outbox`：`id VARCHAR(64)` 主键、`topic`、`event_type`、`version`、`tenant_id`、`aggregate_type/id`、`idempotency_key`、`trace_id`、`payload JSON`、`status`、`attempts`、`next_retry_at`、`last_error`、`occurred_at`、`created_at`、`updated_at`；索引 `(status,next_retry_at)`、`(created_at)`、`(topic)`。
- `async_dlq`：`source_id` 唯一、payload、attempts、last_error、failed_at、replayed_at。
- `async_consumed`：`(event_id, consumer)` 主键、`status`（processing/done）、`updated_at`。
- down：三张表 `DROP TABLE`。

### 阶段二迁移 `migrations/000010_migrate_iam_outbox.{up,down}.sql`

- up：把 `iam_outbox` 中 `succeeded/dead/failed` 行与未完成行按 `topic='iam.audit'`（`cache.invalidate` 映射为 `iam.cache`）复制进 `async_outbox`，随后 `DROP TABLE iam_outbox`。
- down：按 `000002` 原结构重建 `iam_outbox`，把 `async_outbox` 中 IAM topic 行复制回（忽略通用新增列），再删除复制行。
- 迁移只由 `cmd/migrate` / 部署 Job 执行，API/worker 启动不迁移。

## 五、配置规划（`internal/platform/config.go`）

```yaml
async:
  enabled: false                 # 默认关闭；prod/staging + iam.mode=enforce 强制 true
  driver: db                     # db；nats/kafka 本轮未实现，Validate 明确拒绝
  outbox:
    publish_in_api: false        # 生产强制 false，由 worker 独占投递
    poll_interval: 1s            # [100ms, 1m]
    batch_size: 100              # [1, 500]
    lease: 30s                   # [5s, 10m]
    max_attempts: 10             # [1, 20]
    concurrency: 2               # [1, 16]
  consumer:
    concurrency: 4               # [1, 32]
    visibility_timeout: 60s      # [5s, 30m]，消费幂等行接管窗口
    retry_backoff: exponential   # exponential|fixed
```

- 默认值、环境变量覆盖、`configs/{admin,biz,c,worker}.yaml.example`、`docs/CONFIG_STANDARDS.md` 同步。
- `Validate`：枚举/范围；`async.enabled=true` 时 `db.driver != memory`；prod/staging + enforce 时 `async.enabled=true`、`driver=db`、`publish_in_api=false`。
- 删除 `iam.outbox.*` 配置（完整迁移），替换为 `async.outbox.*`；同步更新 ENT-02 相关测试与文档。

## 六、阶段一：信封 + Store + Relay（T1/T2/T3）

| 任务 | 产出 | 关键文件 |
|---|---|---|
| T1 envelope | `Envelope.Validate/ToMessage/FromMessage`，必填校验、版本非空、payload JSON object、≤32KiB | `pkg/mq/envelope.go`、`headers.go`、`envelope_test.go` |
| T2 Store | `Event` 状态机（pending→processing→succeeded/failed/dead）、`RetryPolicy`、`Store` 接口、`MySQLStore`（条件更新认领、`ClaimBatch` 租约、`ReclaimStale`、`MoveToDeadLetter` 事务写 DLQ） | `pkg/outbox/{event,policy,store,mysql,memory}.go` |
| T3 Relay | 并发 worker 各自 `ClaimBatch`；成功 MarkDone；可重试 Reschedule + 指数退避；永久/耗尽 MoveToDeadLetter；启动即跑一轮；ctx 取消后等待在途 handler 汇合；Observer 回调 | `pkg/outbox/relay.go`、`relay_test.go` |
| 指标 | `async_outbox_delivery_total{result}`、`async_outbox_pending`、`async_outbox_lag_seconds`、`async_dlq_depth` | `internal/platform/async_metrics.go` |
| 门禁 | `scripts/ci/check-outbox.sh`：包存在、无 internal 依赖、信封字段、迁移表、配置样例、指标名；接入 `Makefile ci` 与 `gitee-job.sh` | `Makefile`、`scripts/ci/` |
| 文档 | `CONFIG_STANDARDS`、`OBSERVABILITY_STANDARDS`、`pkg-roadmap` | 对应 docs |

阶段一验收：`pkg/outbox` 单测 + MySQL 集成用例证明「已提交未发布 → 重启续投」「并发副本只投一次」。

## 七、阶段二：消费运行时 + DLQ + worker + IAM 迁移（T4/T5/T6/T7/T8）

| 任务 | 产出 | 关键文件 |
|---|---|---|
| T4 消费运行时 | `Router.Register(topic, handler)`；未知 topic 永久失败；`Idempotent` 包装（`Deduper.Acquire/Complete/Release`，处理中租约接管）；并发度 = `async.consumer.concurrency`；优雅退出 | `pkg/outbox/router.go`、`dedupe.go`、`memory.go`、`mysql.go` |
| T5 DLQ | `MoveToDeadLetter` 写 `async_dlq`；`worker -replay-dlq=<id|all>` 把 dead 行重置 pending 并标记 `replayed_at`；runbook | `cmd/worker/main.go`、`internal/platform/async.go`、`docs/runbooks/async-dlq.md` |
| T6 worker | platform 装配通用 Store/Relay/Runtime；worker 启动 Relay + 注册 handler；API 仅在 `publish_in_api=true` 时启动 Relay；退出等待收敛 | `internal/platform/async.go`、`cmd/worker/main.go` |
| T7 预留 | `pkg/mq/broker.go` 定义 `Broker` 组合接口；`async.driver` 仅接受 `db`，其他值 Validate 返回「未实现」；文档写明扩展点 | `pkg/mq/broker.go`、`config.go` |
| T8 IAM 完整迁移 | 删除 IAM 自有 outbox 状态机/仓储/发布器；`OutboxRecorder` 改写入 `outbox.Store`（topic=`iam.audit`）；worker 注册 IAM 审计 handler；IAM 指标名保持不变（`iam_outbox_*`）由 Observer 映射 | `internal/iam/{app,domain,infrastructure}`、`cmd/internal/iamruntime/*`、`internal/iam/observability/metrics.go` |
| 集成测试 | `tests/integration/async/async_test.go` 覆盖 AC-ENT-03-1～5 | `tests/integration/async/`、`tests/integration/iam/` |
| 文档/状态 | gaps、implementation-plan 状态跟踪、ARCHITECTURE、配置样例 | 对应 docs |

## 八、验收标准映射

| AC | 用例 |
|---|---|
| `AC-ENT-03-1` 事务提交后、发布前崩溃仍续投 | 集成用例：Append 后不启动 Relay / 在投递前取消 ctx，新建 Relay 回收 pending 并投递成功 |
| `AC-ENT-03-2` 重复投递无重复副作用 | 同一 event 投递两次 + `Idempotent` 包装，业务计数恒为 1 |
| `AC-ENT-03-3` 毒消息进 DLQ 且可观测 | handler 返回永久错误 → `async_dlq` 有行；`async_outbox_pending/lag` 与 `async_dlq_depth` 可见 |
| `AC-ENT-03-4` worker 跨进程消费 API 事件 | 连接 A（模拟 API）Append，连接 B（模拟 worker）Relay 消费成功 |
| `AC-ENT-03-5` 新业务域接入不改平台核心 | 单测/集成中仅通过 `Router.Register` + 事件类型接入示例域，platform 无业务分支 |

## 九、验证命令

```powershell
gofmt -l .
go run golang.org/x/tools/cmd/goimports@v0.30.0 -local github.com/admin-frame/admin-frame -l .
go build ./...
go test ./...
golangci-lint run ./...
bash scripts/ci/check-outbox.sh
bash scripts/ci/check-config.sh
bash scripts/ci/check-observability.sh
bash scripts/ci/check-migrations.sh
bash scripts/ci/run-integration-tests.sh
```

（本机无 `make`，逐项执行 `Makefile ci` 等价命令；集成测试必须 `-p 1` 串行。）

## 十、风险与回滚

| 风险 | 缓解 | 回滚 |
|---|---|---|
| 重复投递放大副作用 | `Idempotent` 包装 + handler 幂等约定；指标暴露 retry | 关闭 `async.enabled`，业务回退同步处理 |
| IAM 迁移导致审计丢失 | 迁移复制 + 行为用例不回归 + staging 验证 | 执行 000010 down，回退镜像 |
| DLQ 堆积无人处理 | `async_dlq_depth` 告警 + replay runbook | 暂停 Relay，人工重放后恢复 |
| 迁移 drop 旧表不可逆 | up/down 均验证；PR 标注；staging 先演练 | down 重建并回填 |
| 多实例重复认领 | 条件更新认领 + 租约回收；集成用例两副本并发 | 暂停其中一个 Relay 实例 |

## 十一、执行顺序

1. 阶段一：T1 → T2（interface + memory）→ T2（MySQL + 迁移）→ T3 → 指标/门禁 → 阶段一验证。
2. 阶段二：T4 → T5 → T6（通用 worker 装配）→ T7（接口预留）→ T8（IAM 迁移）→ 集成测试 → 文档回写 → 全量门禁。
3. 每阶段结束更新 `docs/production-readiness-implementation-plan.md`「状态跟踪」；不自行 commit。
