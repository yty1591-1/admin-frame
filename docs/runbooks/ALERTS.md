# 告警目录（ALERTS）

> 每个 **P1/P2** 必须指向仓库内 runbook。新增告警时同步更新本表（RB-03/05）。

| 告警 ID | 级别 | 说明 | Runbook |
|---------|------|------|---------|
| `api.5xx.rate_high` | P1 | 任一 API 进程 5xx 比率持续升高 | [api-5xx-spike.md](./api-5xx-spike.md) |
| `db.pool.exhaustion` | P1 | 数据库连接池耗尽或获取超时飙升 | [db-connection-pool.md](./db-connection-pool.md) |
| `release.health_failed` | P1 | 发版后 readiness 失败或错误激增需回滚 | [release-rollback.md](./release-rollback.md)（后端）/ [frontend-rollback.md](./frontend-rollback.md)（前端） |
| `api.latency.p99_high` | P2 | 端侧 P99 延迟超过 SLO | [api-5xx-spike.md](./api-5xx-spike.md) |
| `worker.queue_lag` | P2 | worker 队列积压超过阈值 | [async-dlq.md](./async-dlq.md) |
| `iam.authz.unavailable` | P1 | 鉴权依赖不可用（`result=unavailable`）占比超阈值 | [iam.md](./iam.md) |
| `iam.cache.invalidate_failed` | P1 | IAM 快照缓存删除失败，撤销可能延迟 | [iam.md](./iam.md) |
| `iam.authz.deny_rate_high` | P2 | 鉴权拒绝率突增，疑似误授权或客户端异常 | [iam.md](./iam.md) |
| `iam.shadow.mismatch` | P2 | shadow 判定与静态表不一致，enforce 前必须归零 | [iam.md](./iam.md) |
| `iam.breakglass.active` | P1 | 存在有效破玻璃授权（`iam_breakglass_active > 0`） | [iam.md](./iam.md) |
| `iam.outbox.backlog` | P1 | 平台 Outbox 中 IAM 主题积压（`iam_outbox_pending > 1000` 或 `iam_outbox_lag_seconds > 60`），通常为 `cmd/worker` 未运行或审计出口不可用，审计延迟且可能丢失 | [iam.md](./iam.md) |
| `async.outbox.backlog` | P1 | 通用 Outbox 积压（pending > 1000 或 lag > 300s），审计/通知延迟 | [async-dlq.md](./async-dlq.md) |
| `async.dlq.depth` | P2 | 存在未重放的死信事件（`async_dlq_depth > 0`） | [async-dlq.md](./async-dlq.md) |
| `async.outbox.dead` | P2 | Outbox 出现新的死信/毒消息 | [async-dlq.md](./async-dlq.md) |
| `async.broker.publish_error` | P2 | NATS JetStream 发布失败率升高（`async.driver=nats`） | [async-dlq.md](./async-dlq.md) |
| `async.broker.consume_dead` | P2 | broker 消费者转入 DLQ | [async-dlq.md](./async-dlq.md) |
| `db.pool.wait_high` | P2 | DB 连接等待耗时升高（池容量不足或慢查询） | [db-connection-pool.md](./db-connection-pool.md) |
| `slo.availability.burn_fast` | P1 | 可用性错误预算快速燃烧（5m+1h > 14.4x） | [slo-error-budget.md](./slo-error-budget.md) |
| `slo.availability.burn_slow` | P2 | 可用性错误预算持续燃烧（1h+6h > 6x） | [slo-error-budget.md](./slo-error-budget.md) |
| `slo.latency.p99_high` | P2 | P99 时延持续超过 1s（SLO） | [slo-error-budget.md](./slo-error-budget.md) |
| `backup.failed` | P2 | 数据库备份 CronJob 连续失败或校验不通过，RPO 有风险 | [backup-restore.md](./backup-restore.md) |

P3 示例（鼓励登记，CI 不强制链接）：

| 告警 ID | 级别 | 说明 | Runbook |
|---------|------|------|---------|
| `disk.usage_warn` | P3 | 节点磁盘预警 | （按需补充） |
