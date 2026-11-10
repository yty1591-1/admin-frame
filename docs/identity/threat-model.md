# 身份域威胁建模（OIDC / SAML / SCIM / M2M）

| 字段 | 内容 |
|---|---|
| 文档用途 | `S6-1` 交付物之一：威胁建模与评审输入 |
| 覆盖范围 | 企业身份接入四通道：OIDC（§7.3）、SAML 2.0（§8.2.1）、SCIM 2.0（§21.1）、M2M（§21.2），以及复用的令牌 / 撤销 / 审计机制 |
| 不在范围 | 密码登录与 MFA 的既有设计（见 [`identity-authn.md`](../identity-authn.md) §5、§7.1～§7.2），仅引用其缓解；基础设施层（Redis / MySQL / K8s）由各自规范覆盖 |
| 当前状态 | **草案（待评审）**：威胁清单已按现有实现逐条对齐，评审记录见 §6，签字栏留空 |
| 依据 | [`identity-authn.md`](../identity-authn.md)、[`SECURITY_STANDARDS.md`](../SECURITY_STANDARDS.md)、[`RUNBOOK_STANDARDS.md`](../RUNBOOK_STANDARDS.md) |

> 说明：本表只收录**已实现的缓解**。凡实现未覆盖的场景，一律写入 §4 残留风险，不得在 §3 中标注「已缓解」。

## 1. 资产与信任边界

| 资产 | 位置 | 泄露 / 篡改后果 |
|---|---|---|
| 三端 JWT 签名密钥环 | Secret 挂载 / 进程内 | 可伪造任意端、任意身份的访问令牌 |
| SP 私钥（SAML 解密 / 签名） | `identity_saml_sp_private_key(_next)` | 可解密他人断言、伪造 SP 签名 |
| `identity_account` 主体密文与 HMAC 索引 | MySQL | PII 泄露；索引可被用于撞库比对 |
| SCIM 令牌（`scim_` 前缀） | 库内仅 SHA-256 | 可推送 / 停用 A 端账号与组授权 |
| M2M `client_secret` | 库内仅 SHA-256 + 指纹 | 可冒充服务账号取令牌 |
| 撤销状态（`authn:ver:*`、`authn:client_ver:*`） | Redis | 失效后停用 / 轮换不能及时生效 |
| 审计流水（`async_outbox`） | MySQL | 无法追溯越权与批量变更 |

信任边界：

1. 浏览器 ↔ C 端前端：URL 与 `localStorage` 均不可信，令牌不得进入地址栏；
2. 企业 IdP ↔ 本平台：IdP 返回的断言、id_token、SCIM 请求体全部按不可信输入处理；
3. A 端管理面 ↔ 协议面：`/admin/v1/*`（人工）与 `/admin/scim/v2/*`（机器）鉴权与限流独立；
4. 服务 ↔ 服务：机器身份只代表服务，不得代表用户。

## 2. 威胁主体

| 主体 | 能力假设 |
|---|---|
| 外部未认证攻击者 | 可枚举端点、构造任意请求、重放抓到的断言 / 授权码 |
| 持有低权令牌的用户 | 可尝试越权、跨端复用、访问协议端点 |
| 被攻陷的 IdP 或 SCIM 客户端 | 可用合法令牌推送恶意组 / 角色映射 |
| 内部误操作 | 错误配置默认值、误发令牌、误删组 |

## 3. 威胁清单与缓解

### 3.1 OIDC

| ID | STRIDE | 场景 | 现有缓解 | 验证 |
|---|---|---|---|---|
| `T-OIDC-01` | Spoofing | 授权码被拦截后重放 | 授权码一次性；`state` 存 Redis 一次性消费，TTL 10 分钟 | `TestOIDCEndToEndAuthorizationCodeFlow`（授权码重放拒绝） |
| `T-OIDC-02` | Tampering | CSRF 诱导受害者完成第三方绑定 | `state` + PKCE（S256）强校验；`state` 与 provider 不匹配即 `40100` | 同上 |
| `T-OIDC-03` | Spoofing | id_token 伪造 / 换钥后旧 token 复用 | 仅接受 `RS256`；校验 `iss`/`aud`/`exp`/`iat`/`nonce`，30s 偏差；未知 `kid` 限频强制刷新，失败关闭 | `TestJWKSRetainsCacheWhenRefreshFails` |
| `T-OIDC-04` | Spoofing | 绕过签名校验，回退到 userinfo 取主体 | OIDC 模式下禁止回退，主体只取 id_token `sub` | 设计 §7.3；无回退分支 |
| `T-OIDC-05` | Elevation | 用未登记的 provider 隐式启用 | `oidc_enabled` 显式开关；关闭时残留 `issuer`/`jwks_url` 启动失败 | `TestOIDCRequiresExplicitFlag` |

### 3.2 SAML 2.0

| ID | STRIDE | 场景 | 现有缓解 | 验证 |
|---|---|---|---|---|
| `T-SAML-01` | Spoofing | 断言伪造 / 签名包裹（XSW） | 必须为 `EncryptedAssertion` 且签名覆盖断言本体；校验 `Issuer`、`Audience`、`Destination` | 2026-11-06 kind 全链路 + 篡改断言负向用例 |
| `T-SAML-02` | Replay | 同一断言重放 | 断言 ID 以 `saml:replay:<provider>:<sha256>` 去重，TTL = `assertion_ttl + clock_skew` | 重放负向用例 |
| `T-SAML-03` | Tampering | 断言时间被拉长 / 时钟偏差放大 | `NotBefore`/`NotOnOrAfter` + 断言总生命周期 ≤ 5 分钟，偏差 ≤ 2 分钟 | 设计 §8.2.1 第 3 步 |
| `T-SAML-04` | Spoofing | 跨 IdP 的 `InResponseTo` 串用 | `InResponseTo` 必须等于 state 记录的 AuthnRequest ID；state 一次性 | state 重放负向用例 |
| `T-SAML-05` | Information disclosure | 令牌经浏览器地址栏泄露 | ACS 只回一次性 ticket（60s、单次消费），前端 `exchange` 换令牌 | ticket 二次兑换 / 无效 ticket 负向用例 |
| `T-SAML-06` | Information disclosure | `SAMLResponse` / NameID / 私钥写入日志 | 日志与审计只记 provider 与粗粒度原因；私钥仅密钥托管注入 | 日志断言（kind 演练） |
| `T-SAML-07` | Repudiation | NameID 不稳定导致重复建号，授权错挂 | `name_id_format` 默认 persistent，AuthnRequest 显式携带 | 2026-11-06 NameID 复测（两次登录同一 `sub`） |
| `T-SAML-08` | DoS | 超大 `SAMLResponse` / XML 实体膨胀 | 请求体上限 512KB，仅接受 `application/x-www-form-urlencoded` | 413 / 415 负向用例 |

### 3.3 SCIM 2.0

| ID | STRIDE | 场景 | 现有缓解 | 验证 |
|---|---|---|---|---|
| `T-SCIM-01` | Elevation | 用 SCIM 令牌访问业务端点 | 令牌仅 `scim_` 前缀且只挂 `/admin/scim/v2/*`；A 端 JWT 也不能调 SCIM | `transport/scim/router_test.go`（JWT 调 SCIM → 401） |
| `T-SCIM-02` | Elevation | 恶意组映射到高权角色 | `group_role_map` 仅 A 端角色、装配期校验；未映射组只同步成员不授权；角色不存在整请求失败 | `scim_test.go`；`AC-P16-SCIM-5` |
| `T-SCIM-03` | Spoofing | 令牌泄露后长期可用 | 每 provider 最多 2 个 active；退役宽限 168h；管理端可即时吊销 | `AC-P16-SCIM-6` 语义 + 管理端点审计 |
| `T-SCIM-04` | Information disclosure | `userName` / 邮箱 / 成员列表被写入日志或响应 `detail` | 落库仅 HMAC 索引 + AES-GCM 密文；审计记 `external_id_hash`；`detail` 不回显原文 | 设计 §21.1.3 / §21.1.5 |
| `T-SCIM-05` | Tampering | 过滤表达式注入 | 仅支持 `eq`/`and` 与属性白名单，其余 `400 invalidFilter`；不实现通用表达式 | `transport/scim/filter.go` 单测 |
| `T-SCIM-06` | Tampering | 并发写覆盖 / 丢更新 | `ETag` + `If-Match` 乐观锁，不匹配 `412`；无 `If-Match` 时 last-write-wins 且审计 `version_check=none` | 仓储与 `scim_test.go` |
| `T-SCIM-07` | DoS | 大批量停用 / 删除账号，或超大请求体 | 请求体上限 1MB；单令牌 10 rps + 单 IP 20 rps，Redis `fail_closed` | 限流用例 + 413 |
| `T-SCIM-08` | Denial | IdP 误推 `active=false` 造成批量下线 | 停用与 `POST .../disable` 同语义但可审计、可恢复（不复活旧会话）；指标 `result="error"` 与 `group_map_total` 无增长告警 | `AC-P16-SCIM-3` |

### 3.4 M2M

| ID | STRIDE | 场景 | 现有缓解 | 验证 |
|---|---|---|---|---|
| `T-M2M-01` | Spoofing | 暴力枚举 `client_id` 或借响应时间判断存在性 | secret 仅存 SHA-256、恒定时间比对；客户端不存在时执行假比对 | `AC-P16-M2M-1`（时间差异 ≤10%） |
| `T-M2M-02` | Elevation | 申请超出授权范围的 scope 或通配符 | `scope` 必填且 ⊆ `allowed_scopes`，禁止 `*`；越权 `400 invalid_scope` | `m2m_test.go` |
| `T-M2M-03` | Elevation | 用 A 端令牌调用 B/C 端，或反之 | 令牌只签发单一端别，`aud` / `channel` / `kid` 三者必须一致 | `AC-P16-M2M-6` |
| `T-M2M-04` | Elevation | 机器令牌被当作人工身份使用 | `token_use=client`，禁带 `roles`/`merchant_id`；授权只按 scope，不查角色快照 | `m2m_test.go`（声明断言） |
| `T-M2M-05` | Repudiation | 停用 / 轮换后旧令牌仍可用 | 停用、轮换、缩小 scope 均 `token_version++` 并删除 `authn:client_ver` 快照，旧令牌 ≤60s 失效 | 集成用例（版本吊销） |
| `T-M2M-06` | Information disclosure | `client_secret` 或令牌原文进入日志 / 审计 | 仅在创建 / 轮换响应出现一次；审计记 `fingerprint` 与 `jti` 摘要 | `check-authn` 示例配置断言 |
| `T-M2M-07` | DoS | 单一客户端打满签发端点 | 按 `client_id` 5 rps / burst 10，叠加单 IP；`fail_closed` | 限流用例 |

### 3.5 共用机制

| ID | STRIDE | 场景 | 现有缓解 | 验证 |
|---|---|---|---|---|
| `T-COM-01` | Elevation | 撤销存储不可用时停用 / 轮换失效 | 生产强制 `failure_mode=fail_closed`，Redis 不可用即拒绝 | `check-config` + 配置校验 |
| `T-COM-02` | Spoofing | JWT 密钥泄露或轮换不同步 | 三端独立密钥环；`kid` 精确匹配，未知 `kid` 拒绝；轮换见 [`key-rotation.md`](../runbooks/key-rotation.md) | 密钥轮换演练 |
| `T-COM-03` | Repudiation | 机器事件缺 `actor_id` 导致审计被丢弃 | 机器事件以 `client_id` 作 `actor_id` + `actor_type=system`，落 `async_outbox` 后异步投递 | `tests/integration/identity/m2m_test.go` |
| `T-COM-04` | DoS | 时钟偏差导致合法令牌被拒 | 各通道显式偏差上限（OIDC 30s、SAML 2 分钟）；不引入全局放宽 | 配置校验 |

## 4. 残留风险（需评审确认接受）

| ID | 描述 | 为何暂不处理 | 补偿控制 | 复评时点 |
|---|---|---|---|---|
| `RR-01` | SCIM 映射到的角色按「IdP 权威」对待：管理员手工授予的同名角色，在成员移出组后会被一并撤销 | 绑定唯一键不含来源列（`(channel, subject_type, subject_id, merchant_id, role_id, active_key)`） | 与 IAM 季度复核配合；解绑原因写 `scim group ...` 可追溯 | ADR 评估来源列后 |
| `RR-02` | 无 `If-Match` 的写入走 last-write-wins，可能覆盖并发修改 | 兼容不支持条件请求的 IdP | 审计 `attrs.version_check=none`；客户端可选择启用 ETag | IdP 能力盘点后 |
| `RR-03` | SAML 静态元数据下，IdP **签名**证书轮换仍需改配置并重部署 | 未实现动态元数据拉取的强制刷新 | 接入清单固化轮换窗口；`metadata_ttl` 缓存 + 未知证书强刷 | S6-6 真实接入后 |
| `RR-04` | `max_secret_age` 超期只告警不失效 | 避免生产断链 | `identity_m2m_secret_age_seconds` 告警 + 审计 `rotation_due` | M6 准入评审 |
| `RR-05` | 审计投递依赖 Outbox Relay，Relay 停摆期间事件只在库内 | 已有 DLQ 与积压告警 | [`async-dlq.md`](../runbooks/async-dlq.md) 处置；`async_outbox` 可按 `trace_id` 回捞 | 季度演练 |
| `RR-06` | 单部署仅支持一个 SCIM `provider` | 表结构按 provider 隔离，但配置为单值 | 多 IdP 需多实例或改配置结构 | 出现第二个企业客户时 |

## 5. 验证索引

| 层 | 用例 / 证据 |
|---|---|
| 单测 | `internal/identity/app/{m2m,scim}_test.go`、`internal/identity/transport/scim/*_test.go` |
| 集成 | `tests/integration/identity/{scim_sync_test.go,m2m_test.go}` |
| 演练 | [`drills`](../incident/drills/) 下 S5-5 门禁演练、kind SAML 全链路与证书轮换记录（`identity-authn.md` §8.2.1） |
| 门禁 | `scripts/ci/check-authn.sh`：开关、路由 404、负向用例、审计动作、指标命名 |
| 运维 | [`identity-federation-onboarding.md`](../runbooks/identity-federation-onboarding.md)、[`identity-auth-failure.md`](../runbooks/identity-auth-failure.md) |

## 6. 评审记录（待填）

| 字段 | 内容 |
|---|---|
| 评审日期 |  |
| 评审人 |  |
| 结论 | 通过 / 有条件通过 / 不通过 |
| 需整改项 |  |
| 残留风险确认 | 是否接受 §4 的 `RR-01`～`RR-06`，逐条勾选 |
| 签字 |  |

通过后的文档动作：本文件状态改为「已评审（日期）」；[`iam-p1-5-7-engineering-plan.md`](../iam-p1-5-7-engineering-plan.md) 的 `S6-1` 行改为「已完成」，并在 §13.2 补安全评审结论。

## 相关链接

- [身份认证设计](../identity-authn.md)
- [安全基线](../SECURITY_STANDARDS.md)
- [M0 立项会签单](../iam/m0-signoff-record-2026-11-10.md)
- [企业身份接入运维手册](../runbooks/identity-federation-onboarding.md)
- [身份认证故障处置](../runbooks/identity-auth-failure.md)
- [审计事件目录](../audit/EVENTS.md)
- [文档地图](../README.md)
