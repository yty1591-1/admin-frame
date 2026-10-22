# 告警目录（ALERTS）

> 每个 **P1/P2** 必须指向仓库内 runbook。新增告警时同步更新本表（RB-03/05）。

| 告警 ID | 级别 | 说明 | Runbook |
|---------|------|------|---------|
| `api.5xx.rate_high` | P1 | 任一 API 进程 5xx 比率持续升高 | [api-5xx-spike.md](./api-5xx-spike.md) |
| `db.pool.exhaustion` | P1 | 数据库连接池耗尽或获取超时飙升 | [db-connection-pool.md](./db-connection-pool.md) |
| `release.health_failed` | P1 | 发版后 readiness 失败或错误激增需回滚 | [release-rollback.md](./release-rollback.md) |
| `api.latency.p99_high` | P2 | 端侧 P99 延迟超过 SLO | [api-5xx-spike.md](./api-5xx-spike.md) |
| `worker.queue_lag` | P2 | worker 队列积压超过阈值 | [api-5xx-spike.md](./api-5xx-spike.md) |
| `iam.authz.unavailable` | P1 | 鉴权依赖不可用（`result=unavailable`）占比超阈值 | [iam.md](./iam.md) |
| `iam.cache.invalidate_failed` | P1 | IAM 快照缓存删除失败，撤销可能延迟 | [iam.md](./iam.md) |
| `iam.authz.deny_rate_high` | P2 | 鉴权拒绝率突增，疑似误授权或客户端异常 | [iam.md](./iam.md) |
| `iam.shadow.mismatch` | P2 | shadow 判定与静态表不一致，enforce 前必须归零 | [iam.md](./iam.md) |
| `iam.breakglass.active` | P1 | 存在有效破玻璃授权（`iam_breakglass_active > 0`） | [iam.md](./iam.md) |
| `iam.outbox.backlog` | P1 | Outbox 积压（`iam_outbox_pending > 1000` 或 `iam_outbox_lag_seconds > 60`），审计延迟且可能丢失 | [iam.md](./iam.md) |

P3 示例（鼓励登记，CI 不强制链接）：

| 告警 ID | 级别 | 说明 | Runbook |
|---------|------|------|---------|
| `disk.usage_warn` | P3 | 节点磁盘预警 | （按需补充） |
