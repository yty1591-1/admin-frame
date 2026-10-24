# SLO 与错误预算处置

> 告警 ID：`slo.availability.burn_fast`（P1）、`slo.availability.burn_slow`（P2）、`slo.latency.p99_high`（P2）。
> SLO 基线见 [`deploy/observability/README.md`](../../deploy/observability/README.md) §3.2。

## 症状

- `SLOAvailabilityBurnFast`：5m 与 1h 窗口错误率均 > 1.44%（14.4x 燃烧率）。
- `SLOAvailabilityBurnSlow`：1h 与 6h 窗口错误率均 > 0.6%（6x 燃烧率）。
- `SLOLatencyP99High`：P99 持续 > 1s。
- 看板 `Admin Frame / RED 总览` 中 5xx 比率或 P99 超阈值。

## 影响

- 可用性 SLO 99.9% 对应每月约 43 分钟预算；快速燃烧 1 小时约消耗 2% 月度预算。
- 可能影响 A/B/C 全部端；若集中在单端，优先按端隔离（Ingress 摘流）。
- 预算耗尽后进入发布冻结，仅允许修复类变更。

## 排查

1. 看板：`red-overview` 按 `service` 拆分 5xx 与 P99。
2. 告警：确认是否同时触发 `API5xxRateHigh`、`DBPoolExhaustion`、`AsyncOutboxBacklog`。
3. 日志：按 `trace_id` 串联 AccessLog → 下游调用；查 5xx 的 route 与状态码。
4. 依赖：DB 连接池等待、Redis 错误率、NATS 发布失败、OTel 仅影响追踪不影响请求。
5. 发布：对照最近一次 `helm history` 与镜像版本，判断是否新版本引入。

## 缓解与恢复

| 场景 | 立即动作 |
|---|---|
| 单端 5xx | Ingress 摘除该端或回滚上一 revision |
| DB 池耗尽 | 按 [`db-connection-pool.md`](./db-connection-pool.md) 扩容/杀慢查询 |
| Outbox/worker 阻塞 | 按 [`async-dlq.md`](./async-dlq.md) 先恢复 worker |
| 下游故障 | 开启降级/限流（`rate_limit.enabled=true`），避免雪崩 |
| 时延升高 | 检查慢 SQL 与 GC，必要时临时扩容副本 |

恢复后确认 5m/1h 燃烧率回到 < 1x，并在事件记录中登记预算消耗。

### 预算策略

| 消耗 | 措施 |
|---|---|
| < 50% | 正常发布 |
| 50%–80% | 冻结高风险变更，优先修复可靠性问题 |
| > 80% | 仅允许修复/回滚；SLO Owner 审批例外 |
| 100% | 全面冻结，复盘后恢复 |

## 升级

- P1 燃烧 15 分钟未收敛 → L2（端 Owner）。
- 多端同时燃烧、预算 > 80% 或涉及数据风险 → L3（SRE/架构 Oncall）。
- SLO 目标本身需调整时走 ADR，业务 Owner 确认后更新记录规则。

## 相关链接

- 规则：[`slo.yaml`](../../deploy/observability/prometheus/rules/slo.yaml)、[`api.yaml`](../../deploy/observability/prometheus/rules/api.yaml)
- 看板：[`red-overview.json`](../../deploy/observability/grafana/dashboards/red-overview.json)
- 处置：[`api-5xx-spike.md`](./api-5xx-spike.md)、[`db-connection-pool.md`](./db-connection-pool.md)、[`async-dlq.md`](./async-dlq.md)
- 规范：[`docs/OBSERVABILITY_STANDARDS.md`](../OBSERVABILITY_STANDARDS.md)
