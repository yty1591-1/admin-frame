# Runbooks（值班与事故预案）

本目录受 [`docs/RUNBOOK_STANDARDS.md`](../RUNBOOK_STANDARDS.md) 约束。

## 入口

| 资源 | 说明 |
|------|------|
| [ALERTS.md](./ALERTS.md) | 告警 ID → runbook |
| [_TEMPLATE.md](./_TEMPLATE.md) | 新建模板 |
| [api-5xx-spike.md](./api-5xx-spike.md) | 5xx / 延迟 / 队列积压 |
| [db-connection-pool.md](./db-connection-pool.md) | 连接池耗尽 |
| [release-rollback.md](./release-rollback.md) | 发版回滚 |
| [iam.md](./iam.md) | IAM 鉴权不可用 / 拒绝率异常 / 误授权回滚 |
| [async-dlq.md](./async-dlq.md) | 异步死信积压与重放 |

## 值班与升级（基线）

| 级别 | 角色 | 何时 |
|------|------|------|
| L1 | 当前值班 | 确认告警、执行 runbook |
| L2 | 端/域 Owner | L1 超时未收敛，或需改代码/数据 |
| L3 | SRE / 架构 Oncall | 多端故障、数据丢失风险、安全事件 |

组织值班表 / 呼叫链路请挂在此处（替换为实际链接）：

- 值班表：（待填：日历或 Pager 链接）
- 升级频道：（待填：IM / 电话树）

响应时限见 RUNBOOK_STANDARDS §5（P1 ≈ 15 min，P2 ≈ 60 min）。
