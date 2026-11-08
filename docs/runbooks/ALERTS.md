# 告警目录（ALERTS）

> 每个 **P1/P2** 必须指向仓库内 runbook。新增告警时同步更新本表（RB-03/05）。

| 告警 ID | 级别 | 说明 | Runbook |
|---------|------|------|---------|
| `api.5xx.rate_high` | P1 | 任一 API 进程 5xx 比率持续升高 | [api-5xx-spike.md](./api-5xx-spike.md) |
| `db.pool.exhaustion` | P1 | 数据库连接池耗尽或获取超时飙升 | [db-connection-pool.md](./db-connection-pool.md) |
| `release.health_failed` | P1 | 发版后 readiness 失败或错误激增需回滚 | [release-rollback.md](./release-rollback.md) |
| `api.latency.p99_high` | P2 | 端侧 P99 延迟超过 SLO | [api-5xx-spike.md](./api-5xx-spike.md) |
| `worker.queue_lag` | P2 | worker 队列积压超过阈值 | [async-dlq.md](./async-dlq.md) |
| `iam.authz.unavailable` | P1 | 鉴权依赖不可用（`result=unavailable`）占比超阈值 | [iam.md](./iam.md) |
| `iam.cache.invalidate_failed` | P1 | IAM 快照缓存删除失败，撤销可能延迟 | [iam.md](./iam.md) |
| `iam.authz.deny_rate_high` | P2 | 鉴权拒绝率突增，疑似误授权或客户端异常 | [iam.md](./iam.md) |
| `iam.shadow.mismatch` | P2 | shadow 判定与静态表不一致，enforce 前必须归零 | [iam.md](./iam.md) |
| `iam.breakglass.active` | P1 | 存在有效破玻璃授权（`iam_breakglass_active > 0`） | [iam.md](./iam.md) |
| `iam.outbox.backlog` | P1 | 平台 Outbox 中 IAM 主题积压（`iam_outbox_pending > 1000` 或 `iam_outbox_lag_seconds > 60`），通常为 `cmd/worker` 未运行或审计出口不可用，审计延迟且可能丢失 | [iam.md](./iam.md) |
| `iam.outbox.dead` | P1 | IAM 审计事件投递进入终态 dead（`increase(iam_outbox_delivery_total{result="dead"}[10m]) > 0`），重试已耗尽、审计事实不可回补 | [async-dlq.md](./async-dlq.md) |
| `iam.outbox.poison` | P1 | IAM 审计事件被判为毒消息（`result="poison"`），重放会再次失败，须先修因 | [async-dlq.md](./async-dlq.md) |
| `iam.certification.sweep_failed` | P2 | 访问复核超期扫描 CronJob 失败（`kube_job_status_failed{job_name=~"admin-frame-certification-sweep.*"} > 0`），超期任务不会被标记 expired，复核证据停更 | [iam.md](./iam.md) |
| `tenant.audit.emit_failed` | P1 | 租户域审计写入失败（`tenant_audit_emit_failed_total` 增长），业务事务已随之回滚 | [async-dlq.md](./async-dlq.md) |
| `tenant.guard.error_rate` | P2 | 租户状态守卫无法判定比例 > 1%（`result="error"`），fail closed 导致商户侧 503/50300 | [tenant-suspend.md](./tenant-suspend.md) |
| `notify.audit.emit_failed` | P1 | 通知域审计写出失败（`notify_audit_emit_failed_total` 增长），拒发与补偿留痕缺失 | [async-dlq.md](./async-dlq.md) |
| `notify.send.failure_rate` | P2 | 通知渠道终态失败率 > 20%（`notify_send_total{result="failed"}`），重试已耗尽 | [notification-delivery.md](./notification-delivery.md) |
| `async.outbox.backlog` | P1 | 通用 Outbox 积压（pending > 1000 或 lag > 300s），审计/通知延迟 | [async-dlq.md](./async-dlq.md) |
| `identity.sms.balance_low` | P1 | 短信供应商余额不足，C 端验证码发送失败（`identity_sms_provider_alerts_total{kind="insufficient_balance"}`） | [identity-sms-balance.md](./identity-sms-balance.md) |
| `identity.token.reuse` | P1 | 检测到刷新令牌重放（`identity_token_reuse_total` 5 分钟内增长），整条设备链已撤销 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.lockout.spike` | P1 | 账号锁定次数异常升高（5 分钟内 > 5 次），疑似撞库或阈值过低 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.revocation.store_error` | P1 | 撤销存储读 / 回源失败，可能触发 fail_closed 全量 503 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.revocation.cache_write_error` | P2 | 撤销快照写入 Redis 失败，最长 60s 后靠回源生效 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.refresh.failure_rate` | P2 | 刷新令牌失败率超过 10%，影响会话续期 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.refresh.latency` | P2 | 刷新令牌 P95 超过 500ms | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.login.failure_rate` | P2 | 登录失败率超过 30%，疑似撞库或依赖故障 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.mfa.latency` | P2 | MFA 校验 P95 超过 500ms | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.mfa.recovery_used` | P2 | 有账号使用 MFA 恢复码，疑似丢失 TOTP 设备 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.step_up.latency` | P2 | 特权操作 step-up P95 超过 500ms | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.oauth.latency` | P2 | OAuth 回调 P95 超过 3s，IdP 变慢或网络抖动 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.oauth.failure_rate` | P1 | OIDC 回调失败率 > 50%（`identity_oauth_callback_total{result="failure"}` 占 10 分钟窗口一半以上且失败速率 > 0.05/s） | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.oidc.jwks_stale` | P1 | OIDC 公钥缓存 1 小时未刷新（`identity_oauth_jwks_cache_age_seconds > 3600`），未知 kid 将失败关闭 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.oidc.jwks_refresh_failing` | P2 | OIDC 公钥集刷新失败（`increase(identity_oauth_jwks_refresh_total{result="failure"}[15m]) > 0`），IdP 轮换后可能全量登录失败 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.saml.acs_failure_rate` | P1 | SAML ACS 断言校验失败率 > 50%（`identity_saml_acs_total{result="failure"}`），证书轮换或算法不匹配 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.session.revoke_failure` | P2 | 设备会话撤销失败（`identity_session_revoke_total{result="failure"}`），旧令牌可能继续可用 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.outbox.backlog` | P2 | identity.audit Outbox 存在积压（`identity_outbox_pending > 0`），审计事件未投递 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.outbox.lag` | P2 | identity.audit 最老事件积压超过 5 分钟（`identity_outbox_lag_seconds > 300`），影响合规时效 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.sms.breaker_open` | P2 | 短信供应商熔断打开（`identity_sms_breaker_open == 1`），验证码通道不可用 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.cleanup.failure` | P2 | 身份域清理任务执行失败，会话表会持续增长 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.cleanup.backlog` | P2 | 单轮清理达到批次上限未清空（`identity_cleanup_batch_limit_hit_total`） | [identity-auth-failure.md](./identity-auth-failure.md) |
| `identity.cleanup.not_running` | P2 | 清理指标缺失（`absent(identity_cleanup_runs_total)`），worker 未暴露指标或清理未启动 | [identity-auth-failure.md](./identity-auth-failure.md) |
| `async.dlq.depth` | P2 | 存在未重放的死信事件（`async_dlq_depth > 0`） | [async-dlq.md](./async-dlq.md) |
| `async.dlq.stalled` | P2 | 死信 30 分钟未减少（`min_over_time(async_dlq_depth[30m]) > 0`），重放疑似无效 | [async-dlq.md](./async-dlq.md) |
| `async.dlq.replay_ineffective` | P2 | 近 30 分钟有重放记录但死信仍 > 0（`async_dlq_replayed_30m > 0`），重放无效 | [async-dlq.md](./async-dlq.md) |
| `async.outbox.dead` | P2 | Outbox 出现新的死信/毒消息 | [async-dlq.md](./async-dlq.md) |
| `async.broker.publish_error` | P2 | NATS JetStream 发布失败率升高（`async.driver=nats`） | [async-dlq.md](./async-dlq.md) |
| `async.broker.consume_dead` | P2 | broker 消费者转入 DLQ | [async-dlq.md](./async-dlq.md) |
| `db.pool.wait_high` | P2 | DB 连接等待耗时升高（池容量不足或慢查询） | [db-connection-pool.md](./db-connection-pool.md) |
| `slo.availability.burn_fast` | P1 | 可用性错误预算快速燃烧（5m+1h > 14.4x） | [slo-error-budget.md](./slo-error-budget.md) |
| `slo.availability.burn_slow` | P2 | 可用性错误预算持续燃烧（1h+6h > 6x） | [slo-error-budget.md](./slo-error-budget.md) |
| `slo.latency.p99_high` | P2 | P99 时延持续超过 1s（SLO） | [slo-error-budget.md](./slo-error-budget.md) |
| `backup.failed` | P2 | 数据库备份 CronJob 连续失败或校验不通过，RPO 有风险 | [backup-restore.md](./backup-restore.md) |

P3 示例（鼓励登记，CI 不强制链接）：

| 告警 ID | 级别 | 说明 | Runbook |
|---------|------|------|---------|
| `disk.usage_warn` | P3 | 节点磁盘预警 | （按需补充） |
