# Runbooks（值班与事故预案）

本目录受 [`docs/RUNBOOK_STANDARDS.md`](../RUNBOOK_STANDARDS.md) 约束；值班、定级与复盘流程见 [`docs/incident/`](../incident/README.md) 与 [`docs/oncall/ROTATION.md`](../oncall/ROTATION.md)。

## 入口

| 资源 | 说明 |
|------|------|
| [ALERTS.md](./ALERTS.md) | 告警 ID → runbook |
| [_TEMPLATE.md](./_TEMPLATE.md) | 新建模板 |
| [api-5xx-spike.md](./api-5xx-spike.md) | 5xx / 延迟 / 队列积压 |
| [db-connection-pool.md](./db-connection-pool.md) | 连接池耗尽 |
| [release-rollback.md](./release-rollback.md) | 发版回滚（后端） |
| [iam.md](./iam.md) | IAM 鉴权不可用 / 拒绝率异常 / 误授权回滚 |
| [async-dlq.md](./async-dlq.md) | 异步死信积压与重放 |
| [slo-error-budget.md](./slo-error-budget.md) | SLO 燃烧率与错误预算冻结 |
| [backup-restore.md](./backup-restore.md) | 备份失败、全量/PITR 恢复与季度演练 |
| [tracing-pipeline.md](./tracing-pipeline.md) | OTel Collector / Tempo / Jaeger 断链处置 |
| [identity-auth-failure.md](./identity-auth-failure.md) | 身份域认证类告警（令牌重放 / 锁定 / 撤销存储 / 清理） |
| [identity-sms-balance.md](./identity-sms-balance.md) | 短信供应商余额与熔断 |
| [key-rotation.md](./key-rotation.md) | JWT 密钥环与摘要密钥的计划内轮换 |
| [key-compromise.md](./key-compromise.md) | 密钥泄露应急（P1，先止血后轮换） |
| [feature-flag.md](./feature-flag.md) | 特性开关误放量 / 缓存不一致 |
| [notification-delivery.md](./notification-delivery.md) | 通知投递失败 / 回执异常 |
| [tenant-onboarding.md](./tenant-onboarding.md) | 租户开通 |
| [tenant-suspend.md](./tenant-suspend.md) | 租户停用 |

## 事故管理入口（ENT-18）

| 资源 | 用途 |
|------|------|
| [../incident/SEVERITY_MATRIX.md](../incident/SEVERITY_MATRIX.md) | P1/P2/P3 判定条件、确认/通报/升级时限 |
| [../incident/RESPONSE.md](../incident/RESPONSE.md) | 声明 → 缓解 → 关闭流程、IC/OL/Comms/Scribe 角色 |
| [../incident/POSTMORTEM_TEMPLATE.md](../incident/POSTMORTEM_TEMPLATE.md) | 复盘模板（含三段耗时与行动项） |
| [../incident/ACTION_ITEMS.md](../incident/ACTION_ITEMS.md) | 行动项台账（owner / 截止 / 验证证据） |
| [../incident/GAME_DAY.md](../incident/GAME_DAY.md) | 季度演练剧本（G1 金丝雀回滚 / G2 DLQ / G3 撤销存储） |
| [../oncall/ROTATION.md](../oncall/ROTATION.md) | 排班模型、交接清单、呼叫链路验证 |

## 值班与升级（基线）

| 级别 | 角色 | 何时 |
|------|------|------|
| L1 | 当前值班 | 确认告警、执行 runbook |
| L2 | 端/域 Owner | L1 超时未收敛，或需改代码/数据 |
| L3 | SRE / 架构 Oncall | 多端故障、数据丢失风险、安全事件 |

具体排班、交接与呼叫链路见 [../oncall/ROTATION.md](../oncall/ROTATION.md)；定级时限见 [../incident/SEVERITY_MATRIX.md](../incident/SEVERITY_MATRIX.md)。  
响应时限：P1 确认 ≤ 5 分钟、15 分钟无收敛升级；P2 确认 ≤ 30 分钟、60 分钟无收敛升级（`RUNBOOK_STANDARDS` §5）。
