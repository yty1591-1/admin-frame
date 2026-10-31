# 身份域认证故障运行手册

> 适用：`identity-device` 相关告警（`identity.token.reuse`、`identity.lockout.spike`、`identity.revocation.store_error`、`identity.refresh.failure_rate`、`identity.login.failure_rate`、`identity.mfa.recovery_used`、`identity.session.revoke_failure`）。
>
> 目标：在 5 分钟内判断是依赖故障、攻击行为还是配置错误，并执行可回滚的处置。

## 1. 快速分诊

```text
1. 看 /readyz：identity_db 是否失败（Redis 已从 readiness 移除）
2. 看 identity_revocation_store_errors_total{op=...} 是否突增
3. 看 identity_refresh_total{result="failure"} 与 identity_token_reuse_total
4. 看 identity_login_total{result="failure",reason=...} 的 reason 分布
5. 看 AccessLog 中 5xx 的 trace_id，并检索 “internal request failure”
```

| 现象 | 最可能原因 | 处置 |
|---|---|---|
| 全站 401/503，`revocation.store_error` 突增 | Redis 故障 | 确认 Redis 健康；账号/会话快照会自动回退 DB；jti 黑名单失败按 fail_closed 拒绝；恢复 Redis 后自动恢复 |
| `identity.token.reuse` > 0 | 刷新令牌被重放 | 确认该设备链已撤销；通知账号所有者；检查客户端并发刷新逻辑 |
| `identity.lockout.spike` | 撞库 / 锁定阈值过低 | 查看 reason 分布；必要时临时上调 `identity.lockout.max_failures` 或封禁来源 IP |
| `identity.refresh.failure_rate` 高 | 会话过期 / 撤销 / 客户端时钟 | 检查 `identity_session` 状态与设备绝对到期；确认客户端时间同步 |
| `identity.mfa.recovery_used` | 用户丢失 TOTP 设备 | 通知账号所有者；核查操作来源；必要时重置 MFA |
| `identity.session.revoke_failure` | DB 事务失败 / Redis 快照失败 | 检查 DB 连接与慢查询；确认事务回滚原因；撤销失败时旧令牌可能继续有效，需重试 |
| 管理员密码/MFA 被异常重置 | 特权操作滥用 | 检索审计 `auth.password.reset` / `auth.mfa.reset`；核对 step-up MFA 与操作人 |

## 2. Redis 故障处置

1. 不要立即重启全部 API Pod；readiness 已只绑定 DB，Pod 不会被批量摘流。
2. 账号 / 会话撤销快照会自动回源 DB；jti 黑名单仍 fail_closed，登出后的 Access Token 会持续被拒绝直到 Redis 恢复。
3. 若 Redis 长时间不可用，评估临时开启 `identity.revocation.failure_mode=fail_open` 的**安全代价**；生产默认禁止。
4. Redis 恢复后确认 `identity_revocation_store_errors_total` 停止增长，并抽查撤销快照键。

## 3. 令牌重放处置

1. 确认 `auth.token.reused` 审计事件与 `identity_token_reuse_total`。
2. 对应 family 应已被整链撤销；检查 `identity_session` / `identity_session_family` 状态。
3. 通知账号所有者修改密码并重新登录；如涉及管理员，执行 `logout-all` 并检查后续登录审计。
4. 复盘客户端是否并发刷新同一刷新令牌。

## 4. 管理员账号接管排查

1. 检索 `auth.password.reset`、`auth.mfa.reset`、`auth.account.deleted`、`auth.token.revoked`。
2. 核对操作人、目标账号、reason、trace_id 与 step-up MFA 审计。
3. 立即禁用可疑管理员、撤销其全部会话、轮换其密码与 MFA。
4. 检查 IAM 绑定是否被同步修改；必要时触发 breakglass 流程。

## 5. 清理积压处置

1. 查看 `identity cleanup completed` 日志中的 deleted 数量。
2. 若长期等于批大小，说明未追平：提高 `identity.retention.batch_size` / `max_batches_per_run` 或缩短 `cleanup_interval`。
3. 确认 000008 索引已执行：`SHOW INDEX FROM identity_session` 应包含 `idx_identity_session_updated`。
4. 避免在业务高峰执行大批量删除；可分时段观察 DB 锁等待。
