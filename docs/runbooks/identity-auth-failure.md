# 身份域认证故障运行手册

| 字段 | 内容 |
|------|------|
| 告警 ID | `identity.token.reuse` / `identity.lockout.spike` / `identity.revocation.store_error` / `identity.revocation.cache_write_error` / `identity.refresh.failure_rate` / `identity.refresh.latency` / `identity.login.failure_rate` / `identity.mfa.latency` / `identity.mfa.recovery_used` / `identity.step_up.latency` / `identity.oauth.latency` / `identity.session.revoke_failure` / `identity.outbox.backlog` / `identity.outbox.lag` / `identity.sms.breaker_open` / `identity.cleanup.failure` / `identity.cleanup.backlog` / `identity.cleanup.not_running` |
| 级别 | P1（`token.reuse` / `lockout.spike` / `revocation.store_error`）；其余 P2 |
| 服务 / 端 | admin-api、biz-api、c-api、worker（`identity` 域 + `cmd/worker` 清理任务） |
| 最后更新 | 2026-11-02 |

> **目标**：在 5 分钟内判断是依赖故障、攻击行为还是配置错误，并执行可回滚的处置。

## 症状

- 触发条件（任一告警成立即进入本手册）：
  - `identity_revocation_store_errors_total` 突增（`revocation.store_error`，P1）；
  - `identity_token_reuse_total` 5 分钟内增长（`token.reuse`，P1）；
  - 5 分钟内账号锁定 > 5 次（`lockout.spike`，P1）；
  - 登录 / 刷新失败率超阈值（`login.failure_rate` > 30%、`refresh.failure_rate` > 10%）；
  - 刷新 / MFA / step-up P95 > 500ms，OAuth 回调 P95 > 3s；
  - `identity_session_revoke_total{result="failure"}` 增长（`session.revoke_failure`）；
  - `identity_outbox_pending > 0` 或 `identity_outbox_lag_seconds > 300`（审计积压）；
  - `identity_sms_breaker_open == 1`（短信通道熔断）；
  - `identity_cleanup_runs_total` 上报失败 / 达批次上限 / 指标缺失（`absent`）。
- 用户可见表现：
  - 已认证请求返回 **401**（令牌被判无效）或 **503**（撤销存储不可用，fail_closed）；
  - 登录、刷新、MFA 校验变慢或直接失败；
  - 账号被异常锁定，或收到「设备已下线」提示（令牌重放触发整链撤销）。

## 影响

- 影响端与范围：
  - `revocation.store_error` 影响**全部已认证请求**（A/B/C 三端），是可用性最高风险项；
  - `token.reuse` / `session.revoke_failure` 影响单个账号及其设备链，属账号安全事件；
  - `outbox.*` 影响审计时效与合规，不影响接口可用性；
  - `cleanup.*` 影响 `identity_session` 归档与表容量，长期不处理会拖慢会话查询。
- 数据正确性风险：**无直接数据损坏**；但撤销失败期间旧令牌可能继续可用（安全风险），审计积压可能导致审计事件延迟投递而非丢失。

## 排查

1. 快速分诊（按顺序执行）：

   ```text
   1. 看 /readyz：identity_db 是否失败（Redis 已从 readiness 移除）
   2. 看 identity_revocation_store_errors_total{op=...} 是否突增
   3. 看 identity_refresh_total{result="failure"} 与 identity_token_reuse_total
   4. 看 identity_login_total{result="failure",reason=...} 的 reason 分布
   5. 看 AccessLog 中 5xx 的 trace_id，并检索 "internal request failure"
   ```

2. 按现象定位（最可能原因 → 处置方向）：

   | 现象 | 最可能原因 | 处置 |
   |---|---|---|
   | 全站 401/503，`revocation.store_error` 突增 | Redis 故障 | 见「缓解与恢复 · Redis 故障」 |
   | `identity.token.reuse` > 0 | 刷新令牌被重放 | 见「缓解与恢复 · 令牌重放」 |
   | `identity.lockout.spike` | 撞库 / 锁定阈值过低 | 查看 reason 分布；必要时临时上调 `identity.lockout.max_failures` 或封禁来源 IP |
   | `identity.refresh.failure_rate` 高 | 会话过期 / 撤销 / 客户端时钟 | 检查 `identity_session` 状态与设备绝对到期；确认客户端时间同步 |
   | `identity.mfa.recovery_used` | 用户丢失 TOTP 设备 | 通知账号所有者；核查操作来源；必要时重置 MFA |
   | `identity.session.revoke_failure` | DB 事务失败 / Redis 快照失败 | 检查 DB 连接与慢查询；确认事务回滚原因；撤销失败时旧令牌可能继续有效，需重试 |
   | 管理员密码/MFA 被异常重置 | 特权操作滥用 | 见「缓解与恢复 · 管理员账号接管」 |
   | `identity.outbox.*` | worker 未运行 / 审计出口不可用 | 检查 `cmd/worker` 存活与 `async_outbox` 表；参见 [async-dlq.md](./async-dlq.md) |
   | `identity.cleanup.*` | worker 清理未启动 / 批次上限 | 见「缓解与恢复 · 清理积压」 |

3. 日志与追踪：按 `trace_id` 串联 AccessLog 与 `internal request failure`；日志中主体标识已脱敏，禁止在工单里粘贴原始凭据或令牌。

## 缓解与恢复

### Redis 故障

> 前置要求：Redis 必须为主从 / 哨兵 / 集群形态并跨可用区部署。jti 黑名单没有数据库事实源，
> Redis 不可用时已认证请求会按 fail_closed 返回 503；这是安全优先的显式取舍（REL-01 / REL-04）。

1. 不要立即重启全部 API Pod；readiness 只绑定 DB，Pod 不会被批量摘流。
2. 账号 / 会话撤销快照自动回源 DB；连续 3 次失败后熔断 10s，冷却期内**跳过 Redis 读写**并直接回源 DB。
3. 熔断期内 jti 校验直接快速失败（503），不再逐请求等待 3s 读超时，避免故障放大为请求雪崩。
4. 若 Redis 长时间不可用，评估临时开启 `identity.revocation.failure_mode=fail_open` 的**安全代价**；生产默认禁止。
5. Redis 恢复后确认 `identity_revocation_store_errors_total` 停止增长，并抽查撤销快照键；下一次冷却结束会自动重新探测 Redis。

### 令牌重放

1. 确认 `auth.token.reused` 审计事件与 `identity_token_reuse_total`。
2. 对应 family 应已被整链撤销；检查 `identity_session` / `identity_session_family` 状态。
3. 通知账号所有者修改密码并重新登录；如涉及管理员，执行 `logout-all` 并检查后续登录审计。
4. 复盘客户端是否并发刷新同一刷新令牌。

### 管理员账号接管

1. 检索 `auth.password.reset`、`auth.mfa.reset`、`auth.account.deleted`、`auth.token.revoked`。
2. 核对操作人、目标账号、reason、trace_id 与 step-up MFA 审计。
3. 立即禁用可疑管理员、撤销其全部会话、轮换其密码与 MFA。
4. 检查 IAM 绑定是否被同步修改；必要时触发 breakglass 流程（见 [iam.md](./iam.md)）。

### 清理积压

1. 看 `identity_cleanup_runs_total{result="failure|skipped"}`：`failure` 表示本轮执行失败，`skipped` 表示未获得分布式锁（另一副本在执行）。
2. 看 `identity_cleanup_rows_total{kind=...}` 与 `identity_cleanup_duration_seconds` 评估吞吐。
3. `identity_cleanup_batch_limit_hit_total` 增长表示单轮达到批次上限仍未清空：提高 `identity.retention.batch_size` / `max_batches_per_run` 或缩短 `cleanup_interval`。
4. 确认 000008 索引已执行：`SHOW INDEX FROM identity_session` 应包含 `idx_identity_session_updated`。
5. 清理任务通过 MySQL `GET_LOCK('identity:cleanup')` 互斥，多副本 worker 只会有一个执行；进程崩溃后连接断开自动释放。
6. 避免在业务高峰执行大批量删除；可分时段观察 DB 锁等待。

### 恢复确认

- 相关指标回落到基线并保持 10 分钟；
- 登录 / 刷新 / MFA 各做一次冒烟（成功路径 + 一次预期失败路径）；
- 涉及批量或配置变更时按 [release-rollback.md](./release-rollback.md) 评估回滚；
- 事后把时间线、根因与验证证据补进事故复盘。

## 升级

- P1（`token.reuse` / `lockout.spike` / `revocation.store_error`）：**15 分钟内**未收敛 → identity 域 Owner + 安全 Reviewer；仍无收敛 → SRE 值班长。
- P2：30 分钟内未收敛或影响面扩大（跨端 / 涉及审计合规）→ 按 P1 升级。
- 涉及疑似账号接管、批量盗刷、凭据泄露：**立即**升级安全负责人，不等指标收敛。
- 升级联系：见 [README.md](./README.md) 值班入口。

## 相关链接

- 告警目录：[ALERTS.md](./ALERTS.md)
- 身份域设计：[identity-authn.md](../identity-authn.md)
- SLO 与容量基线：[identity-slo.md](../identity-slo.md)
- 短信通道：[identity-sms-balance.md](./identity-sms-balance.md)
- IAM / 破玻璃：[iam.md](./iam.md)
- 审计积压与死信：[async-dlq.md](./async-dlq.md)
- 发版回滚：[release-rollback.md](./release-rollback.md)
