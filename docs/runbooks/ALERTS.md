# 告警目录（ALERTS）

> 每个 **P1/P2** 必须指向仓库内 runbook。新增告警时同步更新本表（RB-03/05）。

| 告警 ID | 级别 | 说明 | Runbook |
|---------|------|------|---------|
| `api.5xx.rate_high` | P1 | 任一 API 进程 5xx 比率持续升高 | [api-5xx-spike.md](./api-5xx-spike.md) |
| `db.pool.exhaustion` | P1 | 数据库连接池耗尽或获取超时飙升 | [db-connection-pool.md](./db-connection-pool.md) |
| `release.health_failed` | P1 | 发版后 readiness 失败或错误激增需回滚 | [release-rollback.md](./release-rollback.md) |
| `api.latency.p99_high` | P2 | 端侧 P99 延迟超过 SLO | [api-5xx-spike.md](./api-5xx-spike.md) |
| `worker.queue_lag` | P2 | worker 队列积压超过阈值 | [api-5xx-spike.md](./api-5xx-spike.md) |

P3 示例（鼓励登记，CI 不强制链接）：

| 告警 ID | 级别 | 说明 | Runbook |
|---------|------|------|---------|
| `disk.usage_warn` | P3 | 节点磁盘预警 | （按需补充） |
