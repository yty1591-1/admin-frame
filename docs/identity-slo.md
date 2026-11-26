# 身份域 SLO 与容量基线

> **状态**：已落地（MNT-04 口径生效）
> **最后更新**：2026-11-10（补 M2M / SCIM 的 SLI、容量与压测口径，对齐 `identity.m2m.*` 三条告警）
> **变更方式**：SLO 目标、容量公式或告警口径变更须同步 `internal/identity/observability` 指标与 `docs/runbooks/`，经 SRE Review 后合入

本文给出身份域（`internal/identity`）的生产 SLO、容量公式与压测方法。指标来自 `internal/identity/observability` 的 `identity_*` 与平台 `http_server_*`；自 2026-11（MNT-04）起刷新 / MFA / step-up / OAuth 均已注册时延直方图，Redis 读口径见 §2.2。

## 1. SLO

| SLI | 目标 | 测量方式 | 告警 |
|---|---|---|---|
| 登录成功率 | ≥ 99.5%（排除 429/503 后的业务失败） | `1 - failure/(success+failure+challenge)` | `IdentityLoginFailureRateHigh` |
| 登录 P95 | ≤ 300 ms | `identity_login_duration_seconds` p95 | API P99 告警联动 |
| 登录 P99 | ≤ 800 ms | p99 | API P99 告警联动 |
| 刷新成功率 | ≥ 99.9% | `identity_refresh_total` | `IdentityRefreshFailureRateHigh` |
| 刷新 P95 | ≤ 500 ms | `identity_refresh_duration_seconds` p95 | `IdentityRefreshLatencyP95High` |
| MFA 校验 P95 | ≤ 500 ms | `identity_mfa_verify_duration_seconds` p95 | `IdentityMFALatencyP95High` |
| step-up P95 | ≤ 500 ms | `identity_step_up_duration_seconds` p95 | `IdentityStepUpLatencyP95High` |
| OAuth 回调 P95 | ≤ 3 s | `identity_oauth_callback_duration_seconds` p95（按 provider） | `IdentityOAuthLatencyP95High` |
| 令牌重放 | 0 次/5m | `identity_token_reuse_total` | `IdentityTokenReuseDetected` |
| 撤销存储错误 | 0 次/5m | `identity_revocation_store_errors_total` | `IdentityRevocationStoreErrors` |
| 审计 Outbox 积压 | pending=0 且 lag ≤ 5m | `identity_outbox_pending` / `identity_outbox_lag_seconds` | `IdentityOutboxBacklog` / `IdentityOutboxLagHigh` |
| 短信通道 | 熔断关闭 | `identity_sms_breaker_open` / `identity_sms_send_total{result=failure}` | `IdentitySMSBreakerOpen` |
| 可用性 | 99.9%/月 | 5xx 比率 + 依赖健康 | `API5xxRateHigh` |
| M2M 签发成功率 | ≥ 99.5%（排除 429/503 后的业务拒绝） | `1 - denied/(issued+denied)`，取自 `identity_m2m_token_total{result}` | `IdentityM2MTokenDeniedSpike` |
| M2M 签发 P95 | ≤ 100 ms | `identity_m2m_token_duration_seconds{result="issued"}` p95 | `IdentityM2MTokenLatencyP95High` |
| M2M secret 年龄 | ≤ 90 天（与 `max_secret_age` 对齐） | `identity_m2m_secret_age_seconds{client}` | `IdentityM2MSecretRotationDue` |
| SCIM 目录同步 | 10k 用户 + 50 组 ≤ 30 min；错误率 < 0.1% | `identity_scim_request_total{result="error"}` + 演练总耗时 | 无专用告警：按演练口径与错误率观测 |

> M2M 与 SCIM 的告警阈值（`denied` 5 分钟增量 > 10、签发 P95 > 100ms）与本节目标同源；
> 两者默认关闭（`enabled=false`），未启用时指标无序列，规则不会误报。

> 登录成功率口径：故意排除限流与依赖不可用造成的失败，避免把攻击流量计入业务 SLO；安全侧另用 token reuse / lockout 告警。

## 2. 容量公式

### 2.1 密码登录

- 单次登录约 10 次 DB/Redis 往返 + 1 次 Argon2（约 19 MiB / 2 次迭代）。
- 每副本并发上限 `C = identity.password.max_concurrent`（默认 2×CPU）。
- 内存预算 ≈ `C × 19 MiB + Go/HTTP 常驻`；HPA 扩容后必须同步核算。
- DB 连接预算：`副本数 × pkg/db MaxOpenConns(默认 25)`，必须小于 MySQL `max_connections` 的 60%。

### 2.2 Redis

- 每个已认证请求固定 **1 次 jti 校验读**（`identity_revocation_redis_calls_total{op="token_load"}`）。
- 账号 / 会话撤销快照优先命中进程内 L1（`identity_revocation_l1_hit_total`）；未命中才读 Redis，
  快照陈旧或 Redis 故障时回源 DB（`identity_revocation_db_refetch_total`）。
- Redis 故障时账号/会话快照回退 DB；jti 校验按 `fail_closed` 拒绝；熔断 10s 内跳过 Redis，避免超时放大。
- 账号快照回源频率 ≈ `活跃账号数 × 副本数 / state_ttl`；`state_ttl` 默认 30s，生产应观察 DB 回源 QPS。
- 容量辅助信号：`identity_active_sessions`（活跃设备数）、`identity_login_qps`（单副本平均登录 QPS），
  已接入 HPA Pods 指标（`values-prod.yaml` 的 `podsMetrics`）。

### 2.3 数据保留

- 每轮清理上限 = `batch_size × max_batches_per_run`（默认 1000 × 50 = 50,000 行/轮）。
- 日吞吐 ≈ `每轮上限 × (24h / cleanup_interval)`；必须大于日新增 rotated 会话 + 失败登录尝试。
- 清理依赖 000008 索引；未执行索引时吞吐会随表增长退化。

### 2.4 M2M 令牌签发

- 限流按 `client_id` 5 rps / burst 10，叠加单 IP 限制；超限返回 429（`result="denied"`、`reason="rate_limited"`）。
- 单次签发 = 1 次 client 查询 + SHA-256 恒定时间比对 + 1 次 RS256 签名，**不含 Argon2**，
  因此不占 §2.1 的密码并发池，CPU 成本比登录低一个数量级。
- 吞吐瓶颈在 DB 查询与签名，不在 Go 侧；`identity_m2m_token_duration_seconds` 为现成直方图，
  P95 目标 100 ms（本轮实测 9.75 ms，余量约 10 倍）。
- 容量辅助信号：`identity_m2m_token_total{result}`（含 `issued` / `denied` 与 `reason` 维度）、
  `identity_m2m_secret_age_seconds{client}`（每 client 一条，成本可忽略）。

### 2.5 SCIM 目录同步

- 限流：单令牌 10 rps + 单 IP 20 rps，Redis `fail_closed`；批量同步应由 IdP 侧排队重试消化 429。
- 写入放大：每用户 1 个事务（账号 + `scim:<provider>` 身份映射）；每组 `N` 成员 = `N` 次 `externalId`
  解析 + `N` 条成员插入，**组创建是长尾来源**（占总耗时 60% 以上，单次最大约 2.7 s）。
- 基线水位：10k 用户 + 50 组 × 200 成员，单副本 120.4 s（本机 DEV-01）、151.7 s（STG-EQ-01 两副本），
  登记见 [`perf/LOAD_BASELINES.md`](./perf/LOAD_BASELINES.md) §2 `LB-DEV-SCIM-SYNC` / `LB-STG-SCIM-SYNC`。
- 容量核算：按「日增量用户数 × 单条写入成本」折算并留 2 倍余量；**全量重同步**按 §4 演练水位
  评估窗口，不要用增量水位外推。

## 3. 压测方法

1. 在 staging 使用生产同规格 DB/Redis，开启 `identity.enabled=true`、`mfa.admin_required=true`（A 端）。
2. 场景：
   - 密码登录：正确密码 70%、错误密码 20%、不存在账号 10%。
   - 刷新：有效令牌 90%、已轮换令牌 10%（验证重放路径）。
   - MFA：TOTP 成功 / 错误各 50%。
   - M2M 签发：有效 `client_id`/`secret` 80%、错误 secret 15%、越权 `scope` 5%（验证 `denied` 的 `reason` 分布）。
   - SCIM 同步：`tests/load/scim_sync.js`，10k 用户 + 50 组 × 200 成员，8 VU；同批重复执行验证幂等。
3. 采样：每档 10 分钟，记录 `identity_login_duration_seconds`、`identity_refresh_duration_seconds`、
   `identity_mfa_verify_duration_seconds`、`identity_step_up_duration_seconds`、`identity_oauth_callback_duration_seconds`
   的 p50/p95/p99，以及 5xx、DB 连接池等待、Redis RTT；M2M 另记 `identity_m2m_token_duration_seconds`
   与 `identity_m2m_token_total{result,reason}`，SCIM 另记 `identity_scim_request_total{result}` 与总耗时。
   实测读数统一登记到 [`perf/LOAD_BASELINES.md`](./perf/LOAD_BASELINES.md) §2，不在本文重复维护。
4. 通过标准：P95/P99 满足第 1 节 SLO；无连接池等待；Redis 故障注入时账号/会话校验仍可用（jti 按 fail_closed 拒绝）。
5. 故障注入：
   - 停 Redis：验证 DB 回退、熔断与告警；确认 Pod 不被 readiness 批量摘流。
   - 制造登录失败：验证 `IdentityLockoutSpike` 与锁定审计。
   - 重放刷新令牌：验证整链撤销与 `IdentityTokenReuseDetected`。

**执行口径（2026-11-10 补充，来自 STG-EQ-01 演练）**：

- A 端 `mfa.admin_required=true` 时，登录只返回 `MFAChallenge`，刷新令牌在 `mfa/verify` 之后签发；
  因此 `refresh` 场景必须以「已有会话的客户端」建模——按 VU 分片维持轮换中的令牌，
  而不是每次刷新都重新登录（后者把 3 倍请求量与 Argon2 成本算进刷新时延）。
- 每账号独立 TOTP：账号池文件 `ACCOUNTS_FILE`（`subject,totp_secret`）交给执行器；
  同一账号在同一 30 s 时间步内被并发校验会被按重放拒绝（`last_used_step` 单调递增），
  这类响应单独计 `skipped`，不计入失败率。
- 实测读数与偏离项登记在 [`perf/LOAD_BASELINES.md`](./perf/LOAD_BASELINES.md) §4.3。

## 4. 容量评审清单

- [ ] `password.max_concurrent` 与 Pod 内存 limit 匹配
- [ ] 副本数 × DB 连接池 < MySQL 上限 60%
- [ ] `revocation.state_ttl` 下 DB 回源 QPS 可接受
- [ ] 清理日吞吐 ≥ 日新增会话 + 登录尝试
- [ ] 登录/刷新/MFA P95/P99 达标
- [ ] M2M 签发 P95 ≤ 100ms，且 `denied` 的 `reason` 分布无异常集中
- [ ] SCIM 10k 同步总耗时与错误率达标；组创建长尾在窗口内
- [ ] Redis 故障注入通过
