# 身份域认证故障运行手册

| 字段 | 内容 |
|------|------|
| 告警 ID | `identity.token.reuse` / `identity.lockout.spike` / `identity.revocation.store_error` / `identity.revocation.cache_write_error` / `identity.refresh.failure_rate` / `identity.refresh.latency` / `identity.login.failure_rate` / `identity.mfa.latency` / `identity.mfa.recovery_used` / `identity.step_up.latency` / `identity.oauth.latency` / `identity.oauth.failure_rate` / `identity.oidc.jwks_stale` / `identity.oidc.jwks_refresh_failing` / `identity.saml.acs_failure_rate` / `identity.session.revoke_failure` / `identity.outbox.backlog` / `identity.outbox.lag` / `identity.sms.breaker_open` / `identity.cleanup.failure` / `identity.cleanup.backlog` / `identity.cleanup.not_running` |
| 级别 | P1（`token.reuse` / `lockout.spike` / `revocation.store_error` / `oauth.failure_rate` / `oidc.jwks_stale` / `saml.acs_failure_rate`）；其余 P2 |
| 服务 / 端 | admin-api、biz-api、c-api、worker（`identity` 域 + `cmd/worker` 清理任务） |
| 最后更新 | 2026-11-08（补「SSO 故障（OIDC / SAML）」：失败率与 JWKS 告警的判定分流、按 reason 处置、禁止动作） |

> **目标**：在 5 分钟内判断是依赖故障、攻击行为还是配置错误，并执行可回滚的处置。

## 症状

- 触发条件（任一告警成立即进入本手册）：
  - `identity_revocation_store_errors_total` 突增（`revocation.store_error`，P1）；
  - `identity_token_reuse_total` 5 分钟内增长（`token.reuse`，P1）；
  - 5 分钟内账号锁定 > 5 次（`lockout.spike`，P1）；
  - 登录 / 刷新失败率超阈值（`login.failure_rate` > 30%、`refresh.failure_rate` > 10%）；
  - 刷新 / MFA / step-up P95 > 500ms，OAuth 回调 P95 > 3s；
  - OIDC 回调失败率 > 50%（`oauth.failure_rate`，P1）或 SAML ACS 校验失败率 > 50%（`saml.acs_failure_rate`，P1）；
  - OIDC 公钥缓存 1 小时未刷新（`oidc.jwks_stale`，P1）或公钥集刷新失败（`oidc.jwks_refresh_failing`，P2）；
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
  - `oauth.failure_rate` / `saml.acs_failure_rate` / `oidc.jwks_stale` 影响**走 SSO 的登录入口**（企业管理端与 C 端企业 SSO）；账号密码 + MFA 通道不受影响，因此失败面取决于该 provider 的接入比例；
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
   | `identity.oauth.failure_rate` / `identity.saml.acs_failure_rate` | IdP 签名 key 轮换、证书过期、时钟偏移、issuer / audience 配错 | 见「缓解与恢复 · SSO 故障（OIDC / SAML）」 |
   | `identity.oidc.jwks_stale` / `identity.oidc.jwks_refresh_failing` | JWKS 端点不可达、证书信任链失效、网络策略拦截 | 见「缓解与恢复 · SSO 故障（OIDC / SAML）」 |
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

#### fail-closed 处置（判定阈值与恢复顺序）

**先明确一个事实：当前没有本地降级开关。** 进程内没有「跳过撤销校验」的开关，现场只有三条路：

| 路径 | 手段 | 代价 / 限制 |
|---|---|---|
| 修依赖 | 恢复 Redis | 唯一不影响安全语义的路径 |
| 放大宽限 | `identity.revocation.token_grace`（默认 30s，硬上限 60s，负值禁用） | 改配置需发布；上限在 `validate` 里强制，不能无限放大 |
| 安全降级 | `identity.revocation.failure_mode=fail_open` | prod / staging 被 `validate` 强制 `fail_closed`；开启需安全负责人书面同意，并补审计与复盘 |

不存在「只让某个副本放行」的开关，因此**不要用重启单个 Pod 的方式试图绕过**：重启只会丢掉本地宽限与 L1 缓存，使 503 面变大。

先分清 503 的来源，三者处置方向不同：

| 判据 | 阈值 | 判定 |
|---|---|---|
| `identity_revocation_store_errors_total` | 5 分钟内持续增长且不回落 | 撤销存储不可用 → 本节 |
| `/readyz` 的 `identity_db` | 失败 | DB 故障 → 按 DB 连接池手册，不是本节 |
| 50300 响应占比 | 5 分钟内 > 5% | 已影响用户 → 按 P1 升级 |

故障期的代码行为（据此判断「是否已熔断」，不要凭现象猜）：

- 连续 **3 次**存储失败后进入 **10s 冷却**（`internal/identity/app/revocation.go` 的 `revocationStoreBreakerThreshold` / `revocationStoreBreakerCooldown`）；
- 冷却期内**账号 / 会话快照直接回源 DB**，不再等 Redis 读超时（DB 是唯一事实源）；
- **jti 黑名单没有 DB 事实源**：冷却期内若该 jti 在宽限窗口内被验证过未撤销则放行，否则按 `failure_mode` 返回 **50300**。

所以 Redis 抖动期间的正确预期是「**部分请求 503、活跃会话仍可用**」。若出现全量 503，说明宽限与回源都被打穿，立即按 P1 升级，不要继续观察。

判断 Redis 是否恢复，用冷却周期做口径：**每个 10s 冷却周期结束后仍产生新错误，连续 3 个周期（约 30s）→ 判为未恢复**；若某周期内错误停止增长，说明 Redis 已回来，转入恢复确认。

**恢复顺序**（按序执行，不要跳步）：

1. 先修 Redis 侧（主从 / 哨兵 / 集群状态、连接数、内存淘汰），不要先动应用；
2. 不重启 API Pod：冷却结束后首个请求会自动重新探测 Redis；
3. 观察 `identity_revocation_store_errors_total` 连续 5 分钟零增长；
4. 抽查撤销键：账号 / 会话快照存在且 TTL 正常，jti 黑名单写入生效；
5. 冒烟四项：登录 → 刷新 → 登出 → **用登出前的令牌请求应为 401**（若返回 200，说明撤销未生效，立即升级 P1）；
6. 复盘时补一项影响面：故障窗口内命中宽限而放行的请求，其「已撤销但暂可用」时长上限等于 `token_grace`（默认 30s，最大 60s）；当前没有专门的宽限命中指标，用故障持续时长与 `identity_revocation_store_errors_total` 曲线估算上界，并写入复盘。


### SSO 故障（OIDC / SAML）

> **先明确一个反直觉事实**：SSO 校验失败走 fail-closed 快速返回（`40100` / `50300`），失败请求比成功更快，
> 所以 `identity.oauth.latency`（P95）在 SSO 全挂时**不会升高，甚至更低**。
> 判断 SSO 是否故障只看 `result` 维度，不要用延迟告警反推。

**第一步：分清是哪一类（三个量，按顺序）**

| 观测 | 查询 | 判定 |
|---|---|---|
| 失败率与主因 | `topk(5, sum by (provider, reason) (rate(identity_oauth_callback_total{result="failure"}[10m])))` | 拿到主因，进第二步分流 |
| 公钥新鲜度 | `identity_oauth_jwks_cache_age_seconds` 对照该 provider 的 `identity.oauth.providers[].jwks_ttl`（默认 15m） | 超过 TTL 表示公钥已无法更新 |
| 刷新是否失败 | `increase(identity_oauth_jwks_refresh_total{result="failure"}[15m])` | > 0 表示拉取 JWKS 失败 |

SAML 侧对称：`identity_saml_authorize_total` 与 `identity_saml_acs_total` 的 `{result="failure", reason=...}`。
两边 reason 词表共用（`internal/identity/app/saml.go` 的 `samlMetricReason` 直接复用 `oauthMetricReason`）。

**第二步：按 reason 处置**

| reason | 含义 | 首个动作 | 禁止动作 |
|---|---|---|---|
| `oauth_not_configured` | 该 provider 未配置，或 `oidc_enabled` 未显式开启 | 核对发布版本与配置项，这是配置问题不是 IdP 问题 | 不要先去动 IdP |
| `oauth_invalid` | `state` 失效 / 重放、`nonce` 不匹配、id_token 签名或 `iss` / `aud` 校验失败 | ① 查 IdP 是否刚轮换签名 key（与公钥年龄对照）；② 核对服务器时钟，偏移超过 `nonce` 窗口会全量失败 | 不要关闭验签，不要放宽 `audience` / `issuer` |
| `unavailable` | JWKS 拉不到，或 IdP 5xx / 超时 | 从 Pod 内直接请求 JWKS 端点，查 DNS、出网策略、CA 信任链 | 不要重启 API Pod（只会丢本地缓存，让失败面更大） |
| `rate_limited` | 回调被限流 | 查限流阈值与来源 IP 分布，判断是否被刷 | 不要直接放开限流 |
| `oauth_failed` | 其他 | 取审计事件 `auth.oauth.callback` 的 reason 原文与 `trace_id` | 不要凭现象猜原因 |

SAML 额外三项（来自 2026-11-06 真实 Keycloak 联调的实测结论，见 [identity-authn.md](../identity-authn.md) §8.2.1）：

- IdP 侧 XML Encryption 必须用 CBC 模式（`AES-256-CBC` 等），**AES-256-GCM 不支持**；Keycloak 26 默认的 XML Encryption 1.1（`rsa-oaep` = RSA-OAEP-256 + `xmlenc#sha256`）需 SP 侧注册，已在 `internal/identity/infrastructure/saml/keys.go` 补上，回滚到旧版本会重新失败；
- IdP 证书轮换后必须同步更新 `identity.saml.idp.certificate`，否则验签全失败且表现为 100% ACS 失败；
- 断言 ID 重放被判失败是**预期行为**（重放防护），不要当故障处理。

**第三步：恢复确认**

1. 失败率回落到基线并保持 10 分钟；
2. 做一次真实 SSO 登录，确认 `identity_oauth_callback_total{result="success"}` 或 `identity_saml_acs_total{result="success"}` 递增；
3. `identity_oauth_jwks_cache_age_seconds` 回到该 provider 的 `jwks_ttl` 以内；
4. 若调整过配置，按 [release-rollback.md](./release-rollback.md) 记录变更点与回滚版本。

**三条禁止动作（任何情况下）**

- 关闭验签或跳过 id_token 校验：等价于允许任意伪造断言登录；
- 放宽 `audience` / `issuer`：会把其他环境或其他租户的令牌放进本系统；
- 顺手把 `identity.revocation.failure_mode` 改成 `fail_open`：这与 SSO 故障无关，只会同时削弱令牌撤销能力。

> **阈值前提**：`identity.oidc.jwks_stale` 取 3600s，前提是该 provider 的 `jwks_ttl` 保持默认或不超过 1h（`validate` 允许配到 24h）。若某 provider 调大过 TTL，把规则阈值改为 `4 × jwks_ttl`，否则会产生稳定误报。



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
