# API 5xx / 延迟飙升

| 字段 | 内容 |
|------|------|
| 告警 ID | `api.5xx.rate_high` / `api.latency.p99_high` / `worker.queue_lag` |
| 级别 | P1 / P2 |
| 服务 / 端 | admin-api / biz-api / c-api / worker |
| 最后更新 | 2026-10-13 |

## 症状

- 5xx 比率或 P99 延迟超过告警阈值，或 worker 积压告警。
- 用户侧：下单失败、列表超时、运营后台报错。

## 影响

- 按 `service` / `channel` 确认是单端还是多端。
- 支付/写路径失败可能伴随幂等冲突或状态不一致，需额外核对订单。

## 排查

1. 确认最近发布：对比 `/version` 的 `commit` / `build_time` 与发布单。
2. 看 RED 指标：按端拆分错误码与依赖错误（DB / 下游）。
3. 日志：`level=error` + `trace_id`；注意脱敏，禁止翻找 Token。
4. `/readyz`：若失败，先看依赖（DB 池、MQ）。
5. 限流：是否大量 `42900`（误伤或攻击）。

## 缓解与恢复

1. 若与发版强相关：执行 [release-rollback.md](./release-rollback.md)。
2. 依赖过载：扩容该端副本或降低非关键流量；开启已验证的降级开关（须可观测）。
3. 投毒/刷接口：加强限流或临时封禁来源（走安全流程）。
4. 恢复后跑该端冒烟（健康检查 + 关键写读）。

## 升级

- P1：15 分钟无收敛 → 端 Owner（L2）→ 多端同时故障升 L3 SRE。
- 数据疑似写坏：立即升 L2，禁止盲目重放写流量。

## 相关链接

- [ALERTS.md](./ALERTS.md)
- [RELIABILITY_STANDARDS.md](../RELIABILITY_STANDARDS.md)
- [OBSERVABILITY_STANDARDS.md](../OBSERVABILITY_STANDARDS.md)
- [RELEASE_STANDARDS.md](../RELEASE_STANDARDS.md)
