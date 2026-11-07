# Runbook：通知投递故障（notification-delivery）

> 关联告警：`notify_send_total{result="failed"}` 突增、`notify_delivery_active` 持续增长、`notify_rate_limited_total` 突增（见 `docs/runbooks/ALERTS.md`）
> 关联域：`internal/notification`（ENT-10）；架构见 `docs/adr/0008-notification-event-driven.md`
> 最后更新：2026-11-04

## 症状

- 指标 `notify_send_total{result="failed"}` 在 5 分钟内显著上升；
- 指标 `notify_delivery_active`（pending + retrying）持续增长不回落；
- `notify_rate_limited_total` 突增（伴随业务方反馈通知延迟或丢失）；
- `async_dlq` 出现 `notification.requested` 主题的死信积压；
- 业务方反馈：邀请邮件未收到 / 租户停用通知未送达。

## 影响

- 受影响场景的通知延迟或丢失（邀请、租户状态变更、运营触达）；
- 事务类场景（验证码除外）不可退订通知同样受影响，但不阻塞业务写入——
  通知是尽力而为能力，渠道故障**不得**导致 `/readyz` 失败或业务事务失败；
- 长时间故障会让 DLQ 积压，重放窗口受事件保留期约束。

## 排查

1. **定位渠道**：`notify_send_total{result="failed"}` 的 `channel` / `provider` / `semantic` 标签——
   `auth_failed`（凭据过期）、`timeout`（网络 / 供应商故障）、
   `provider_error`（5xx / 内容拒绝）、`channel_unavailable`（熔断 Open）。
2. **确认熔断状态**：`notify-<provider>` 熔断器是否 Open（Grafana 熔断面板）；
   Open 说明供应商持续故障，主备应已切换（`degraded` 标签可确认降级）。
3. **检查编排积压**：`async_outbox` 中 `notification.requested` 的 pending 数量
   与最老事件年龄（`TopicStatReader` 指标）；worker 是否存活、Relay 是否报错。
4. **检查频控**：`notify_rate_limited_total` 是否与营销放量 / 疑似轰炸对应；
   频控键不含收件人信息，需结合业务侧放量记录判断。
5. **抽样死信**：`SELECT last_error, failed_at FROM async_dlq WHERE topic='notification.requested' ORDER BY failed_at DESC LIMIT 10`；
   `last_error` 已脱敏，含语义码与供应商原始码。

## 缓解与恢复

- **供应商凭据失效（auth_failed）**：轮换凭据（走密钥托管注入），重启 worker；
  凭据属渠道驱动配置，不热加载。
- **供应商故障（timeout / provider_error / 熔断 Open）**：
  主备路由自动降级无需人工干预；确认备用供应商健康后等待恢复，
  或在路由配置中临时提升备用供应商为主（配置变更需发布 worker）。
- **编排积压（delivery_active 高）**：扩容 worker 副本（Relay 多实例安全，
  ClaimBatch 条件更新认领）；确认 `async.outbox` 退避参数未被调大。
- **DLQ 积压恢复**：修复根因后按 `docs/runbooks/async-dlq.md` 重放死信；
  通知事件重放会被幂等键吸收或作为补偿重新投递（ADR-0008 §4）。
- **频控误伤（rate_limited 突增且为误判）**：调整频控配额（配置发布），
  收件人维度限流 1/min、5/h、10/day 不得放宽（防轰炸基线）。

## 升级

- 15 分钟内无法定位渠道侧根因 → 升级至平台 Owner；
- 事务类通知（邀请 / 安全告知）丢失影响客户操作 → 按 P2 起报，
  影响登录验证码（identity 旁路）→ 按 P1 起报；
- 涉及疑似短信轰炸 / 邮件投诉激增 → 同步安全 Owner（黑名单与频控联合处置）。

## 相关链接

- `docs/adr/0008-notification-event-driven.md`（编排与补偿语义）
- `docs/archive/plans/notification-center-plan.md`（能力清单与验收标准）
- `docs/runbooks/async-dlq.md`（死信重放操作）
- `docs/runbooks/identity-sms-balance.md`（验证码渠道余额）
- 指标：`notify_send_total` / `notify_delivery_active` / `notify_rate_limited_total` / `notify_request_total`
