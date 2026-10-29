# 短信供应商余额不足（identity.sms.balance_low）

| 字段 | 内容 |
|------|------|
| 告警 ID | `identity.sms.balance_low` |
| 级别 | P1 |
| 服务 / 端 | c-api（短信验证码登录 / 注册） |
| 最后更新 | 2026-10-29 |

## 症状

- 触发条件：`increase(identity_sms_provider_alerts_total{kind="insufficient_balance"}[5m]) > 0` 持续 1m。
- 日志：`msg="identity sms provider alert"`，字段含 `alert=insufficient_balance`、`provider`、厂商码（如 `isv.AMOUNT_NOT_ENOUGH`）。
- 用户可见表现：`POST /c/v1/auth/sms/send` 返回 `50300`，C 端无法收到验证码；短信登录与注册失败。
- 熔断：连续可重试故障达到 `identity.sms.breaker_failures` 后，`identity-sms-<driver>` 熔断器转 `open`，日志出现 `identity sms circuit breaker`。

## 影响

- 影响端与范围：仅 C 端短信验证码链路；OAuth 登录与 A/B 端密码登录不受影响。
- 数据正确性风险：无。发送失败会删除已写入的验证码并回滚重发窗口，用户不会被误锁。
- 业务影响：新用户无法注册、存量用户无法通过短信登录。

## 排查

1. 确认告警指标：`identity_sms_provider_alerts_total{kind="insufficient_balance"}` 是否持续增长，记录 `provider`。
2. 查看厂商控制台：账户余额 / 欠费状态 / 套餐余量；核对模板与签名审核状态（余额不足与永久拒绝是不同分支）。
3. 查 c-api 日志（禁止记录手机号）：
   - `msg="identity sms provider alert"`：余额不足；
   - `msg="identity sms circuit breaker"`：熔断状态迁移；
   - `identity_sms_send_total{result="failure"}` 的增长速率。
4. 区分故障类型：
   - 余额不足：厂商码包含 `AMOUNT_NOT_ENOUGH` / `INSUFFICIENT_BALANCE` 等；
   - 模板 / 签名 / 号码非法：`sms: rejected`，不触发熔断；
   - 厂商超时 / 5xx：可重试故障，累计后触发熔断。

## 缓解与恢复

1. 立即在厂商控制台充值或切换已配置的备用短信供应商（需同步修改 `identity.sms.driver` 与凭证环境变量）。
2. 充值成功后等待熔断 `identity.sms.breaker_timeout`（默认 60s）自动进入半开探测；观察 `identity_sms_send_total{result="success"}` 恢复。
3. 若需要立即恢复，可重启 c-api 重置进程内熔断状态（仅在确认厂商已恢复时执行）。
4. 禁止通过关闭 `identity.sms.enabled` 掩盖问题；如需临时止损，应在变更记录中注明影响范围。

## 升级

- 余额不足超过 15 分钟且无法充值：升级至业务负责人，评估切换供应商或临时关闭 C 端短信登录入口。
- 熔断打开后 C 端错误率持续 > 5%：升级至值班负责人，按 P1 事故处理。

## 相关链接

- [identity-authn.md §8.1 短信验证码](../identity-authn.md)
- [ALERTS.md](./ALERTS.md)
- [slo-error-budget.md](./slo-error-budget.md)
