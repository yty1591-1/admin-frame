# ENT-01 身份闭环：现状评估与实施拆解（计划）

## Context

`production-readiness-gaps.md` 将 `ENT-01` 列为 P0 上线阻断项：平台只有 JWT 验签，没有账号域、登录 / 刷新 / 登出 / 撤销 / MFA，令牌泄露只能等过期。`ENT-02`（多实例语义）与 `ENT-03`（通用异步基础设施）已关闭，主线 A 只剩 `ENT-01` 与 `ENT-04`。

本轮只产出**设计与任务拆解文档**，不写实现代码；后续按文档中的阶段独立 MR 落地。

已确认的四项决策：

| 决策点 | 结论 |
|---|---|
| 实施范围 | 全量 `T1`～`T10`：账号域、三端登录 / 刷新 / 登出 / 撤销、A 端 TOTP MFA、A 端 SSO/OIDC 预留、C 端短信 + OAuth |
| 撤销机制 | 账号级 `token_version`（JWT `ver` 声明）+ 单会话 `jti` 黑名单 |
| 刷新策略 | `identity_session` 持久化刷新令牌哈希，一次性轮换 + 重放检测；Redis 仅做撤销 / 版本快查 |
| 交付物 | 新增 `docs/identity-authn.md`，并回写 `production-readiness-implementation-plan.md` 的 `ENT-01` 章节 |

## 现状评估（已核实）

| 维度 | 现状 | 证据 |
|---|---|---|
| 令牌校验 | 只有校验侧，支持 HS256/RS256、iss/aud/exp/leeway、多密钥轮换校验 | [jwt.go](file:///d:/MyDocument/admin-frame/internal/security/jwt.go)、[manager.go](file:///d:/MyDocument/admin-frame/pkg/jwt/manager.go) |
| 令牌签发 | `pkg/jwt.Manager.Sign` 可用，但全仓无签发端点、无 `kid` 头 | [manager.go](file:///d:/MyDocument/admin-frame/pkg/jwt/manager.go#L181-L204) |
| 账号域 | 不存在 `identity` / `account` 包；Principal 只有 SubjectID/MerchantID/Roles | [principal.go](file:///d:/MyDocument/admin-frame/internal/shared/auth/principal.go#L30-L36) |
| 认证端点 | 三端 `register.go` 均未注册 `/auth/*`；OpenAPI 三端无认证契约 | [admin/register.go](file:///d:/MyDocument/admin-frame/cmd/admin-api/register.go) |
| 可复用原语 | `pkg/password`（Argon2id + NeedsRehash）、`pkg/redis`、`pkg/sms`、`pkg/outbox`、`AsyncRuntime`、`audit`、`apperr` | `pkg/*`、[async.go](file:///d:/MyDocument/admin-frame/internal/platform/async.go) |
| 装配点 | `AuthMW` 在 `Run` 中先于 `NewAuthorizer` 构造，撤销检查需新增注入挂点 | [bootstrap.go](file:///d:/MyDocument/admin-frame/internal/platform/bootstrap.go#L150-L197) |
| 迁移编号 | `000001`～`000010` 已占用（`000009/000010` 属 ENT-03）；计划文档写的 `000009_create_identity_tables` 已失效 | `migrations/` |
| 门禁 | 无 `check-authn.sh`，`make ci` 未包含该门禁 | [Makefile](file:///d:/MyDocument/admin-frame/Makefile)、`scripts/ci/` |
| 配置 | 无 `identity.*`；`auth.jwt` 只有验签参数、无签发私钥与 `active_kid` | [config.go](file:///d:/MyDocument/admin-frame/internal/platform/config.go#L375-L398) |

## 交付物一：`docs/identity-authn.md`

按项目 kebab-case 命名与硬约束专文风格编写，含目录、状态、变更方式、关联文档。章节与要点：

1. **定位与边界**
   - 身份域只管「你是谁」：账号、凭据、会话、MFA、登录审计。
   - IAM 只管「你能做什么」：角色 / 权限 / 数据范围；令牌不固化角色（T10）。
   - 不 import `internal/platform`；平台通过 `platform.Deps` 在 cmd 层注入。
2. **现状与差距**：引用上文评估表，附复核命令。
3. **领域模型**（`internal/identity/domain`）
   - `Account`：`Channel` + `Subject` 唯一，状态机 `pending → active → locked → disabled`，`TokenVersion`。
   - `Credential`：密码（Argon2id，复用 `pkg/password`）、C 端手机号绑定、OAuth 身份。
   - `Session`：刷新令牌哈希、`FamilyID`、状态 `active → rotated → revoked / expired`、`TokenVersion` 快照。
   - `MFAEnrollment`：TOTP 密钥密文、确认时间、恢复码哈希。
   - `LoginAttempt`：失败计数、锁定窗口，不存明文手机号 / 密码。
   - 每个状态迁移给出表驱动用例清单。
4. **令牌模型**
   - Access JWT claims：`sub`、`iss`、`aud`、`iat`、`exp`、`jti`、`ver`、`channel`、`merchant_id`；禁止 `roles`。
   - Refresh Token：32 字节随机不透明串，库内只存 SHA-256；前缀区分端别，禁止写入日志。
   - 三端 `issuer` / `audience` / 密钥隔离（SEC-04）；`kid` 轮换。
   - 扩展 `pkg/jwt`：签发支持 `kid` 头与密钥环，校验按 `kid` 选择、未知 `kid` 失败关闭；`security.JWTConfig` 增加 `active_kid` 与 `keys[]`（保留旧 `secret` / `public_key_file` 兼容路径）。
5. **登录 / 刷新 / 登出流程**：正常路径、失败路径、审计事件、限流桶，含时序图（Mermaid）。
   - 刷新：事务内「旧会话置 `rotated` + 新会话写入」；重复使用已轮换令牌 → 撤销整个 `FamilyID` 并记 `auth.token.reused`。
   - 登出：撤销当前会话 + 写 `authn:revoked:<jti>`（TTL = Access 剩余寿命），可选 `logout_all` 递增 `token_version`。
6. **撤销机制**
   - Redis `authn:ver:<account_id>`（TTL ≥ access_ttl + leeway）、`authn:revoked:<jti>`。
   - `internal/security` 定义消费侧接口 `RevocationChecker`（S-07），JWT 中间件在 claims 解析后调用。
   - `failure_mode=fail_closed`：Redis 不可用返回 `50300`，禁止放行；`fail_open` 仅限非生产且需安全审批。
   - 禁用账号 / 改密：DB 递增 `token_version` + 写 Redis，≤ 60s 生效（缓存 TTL 上限）。
7. **A 端 MFA 与 SSO**
   - TOTP：`pkg/totp`（HMAC-SHA1、30s、6 位、±1 窗口），密钥经 `pkg/crypto` AES-GCM 加密，密钥来源本地环境变量、生产走 ENT-07 密钥托管。
   - 两步登录：密码通过 → 返回短期 `mfa_ticket`（`aud=admin`、`scope=mfa`、5 分钟）；`/mfa/verify` 通过后才签发管理令牌（AC-ENT-01-4）。
   - 绑定 / 解绑 / 恢复码；`identity.mfa.admin_required=true` 时未绑定账号拒绝登录并提示绑定路径。
   - OIDC：定义 `OIDCProvider` 接口与 state/nonce/PKCE 流程，先预留实现与开关，不新增外部依赖。
8. **C 端登录**
   - 短信：6 位验证码，Redis 存哈希（TTL 5 分钟）、最大尝试 5 次、重发间隔 60s；按手机号 + IP 双桶限流；响应恒定，防枚举。
   - OAuth：授权码流程，provider 配置齐全才注册路由；`identity_oauth_identity` 维护 `(provider, provider_subject)` 映射。
9. **数据模型与迁移**
   - `000011_create_identity_tables`：`identity_account`、`identity_credential`、`identity_session`、`identity_mfa`、`identity_login_attempt`、`identity_oauth_identity`。
   - 沿用 `DATETIME(3)` UTC、`utf8mb4_0900_ai_ci`、雪花主键、`UNIQUE(channel, subject)`、`UNIQUE(refresh_token_hash)`、`INDEX(account_id, status)`、`INDEX(family_id)`；up/down 成对。
   - 明确 API 进程不执行迁移，只由 `cmd/migrate` 执行。
10. **配置规划**（`identity.*` 全量）
    - `enabled`、`access_ttl`、`refresh_ttl`、`session.idle_timeout`、`session.max_per_account`；
    - `revocation.driver/failure_mode`、`password.*`、`lockout.*`、`mfa.*`、`login_rate_limit.*`、`sms.*`、`oauth.providers[]`。
    - 生产强制项：`auth.mode=jwt`、`identity.enabled=true`、`revocation.driver=redis`、`revocation.failure_mode=fail_closed`、A 端 `mfa.admin_required=true`；C 端至少启用短信或 OAuth 之一。
    - 每项补 `Validate`、`ADMIN_FRAME_IDENTITY_*` 环境变量、示例配置与 `LogValue` 脱敏。
11. **审计事件**
    - 复用现有 `auth.login` / `auth.login_failed`，新增 `auth.logout`、`auth.token.refresh`、`auth.token.reused`、`auth.token.revoked`、`auth.mfa.bound`、`auth.mfa.failed`、`auth.account.locked`、`auth.account.disabled`。
    - 同步 `internal/shared/audit/actions.go` 与 `docs/audit/EVENTS.md`；审计写入与业务写入同一事务（复用 IAM UnitOfWork + `async_outbox` 模式）。
    - 禁止记录令牌、密码、验证码、完整手机号。
12. **接口契约**
    - A/B/C 三端：`POST /{端}/v1/auth/login`、`/refresh`、`/logout`；A 端追加 `/mfa/setup|verify|disable`、`/sessions`、`/sessions/{id}`、`/sessions/revoke-all`；C 端追加 `/sms/send`、`/oauth/{provider}/callback`。
    - 认证端点不挂全局 `AuthMW`，使用独立限流桶；错误码沿用 `40100 / 40000 / 42900 / 50300`，登录失败文案统一。
    - 三端 OpenAPI 补 path、schema、错误码；`info.version` 提 minor。
13. **平台接入点**
    - `platform.Options` 新增 `NewSessionValidator func(*Deps) (func(context.Context, jwt.Claims) error, error)`，在 `NewAuthMiddleware` 之前调用；cmd 用 `sync.Once` 构造身份运行时并复用。
    - `auth.Principal` 增加 `SessionID`、`TokenVersion`、`TokenExpiresAt`；`principalFromClaims` 读取 `jti` / `ver`，`roles` 仅保留兼容读取、签发出端不再写入。
    - 身份运行时向 `deps.Async` 注册审计消费（与 IAM 共用 `audit.event` topic）。
14. **任务拆解**（T1～T10，逐条给出产出、变更路径、验收要点、依赖）。
15. **验收标准映射**：`AC-ENT-01-1`～`5` → 用例文件与命令。
16. **门禁 `check-authn`**：生产配置禁 demo / 强制身份项、三端 OpenAPI 认证路径存在、撤销路径有测试引用、无明文凭据日志。
17. **风险与回滚**：令牌语义切换、Redis 故障、短信 / OIDC 供应商、账号迁移；每项给开关与回退路径。
18. **实施阶段**：见下节。

## 交付物二：`docs/production-readiness-implementation-plan.md` 修订

- `ENT-01` 章节顶部补 `状态：进行中（拆解完成）` 与 `详见 identity-authn.md`。
- 修正 `T2` 的迁移编号：`000009_create_identity_tables` → `000011_create_identity_tables`（`000009/000010` 已被 ENT-03 占用）。
- 配置规划块补 `auth.jwt.active_kid` / `keys[]` 与 `identity.sms` / `identity.oauth` 字段。
- 任务表补「阶段 / 独立 MR」列，T1～T10 与阶段映射一致。
- 「十二、状态跟踪」中 `ENT-01` 行改为「进行中」，并链接新文档。
- 「九、文档回写矩阵」`ENT-01` 行补 `docs/identity-authn.md`。

## 交付物三：`docs/production-readiness-gaps.md` 最小回写

- `ENT-01` 状态行补「拆解完成，详见 identity-authn.md」，不改变缺口结论与验收标准。

## 任务拆解与阶段（写入文档正文）

| 阶段 | MR 内容 | 对应任务 |
|---|---|---|
| 0 契约 | 设计文档、OpenAPI 骨架、配置字段与 `Validate`、迁移 `000011` 草案、审计 action 目录 | T1/T2/T9 前置 |
| 1 账号域 | domain + 内存/MySQL 仓储 + 密码策略 + 锁定 + 迁移 + 单测 | T1/T2/T3 |
| 2 令牌闭环 | `pkg/jwt` kid/密钥环、签发、登录 / 刷新 / 登出、撤销 checker、三端端点、集成用例 AC-1/2/3/5 | T4/T5/T6（A/B） |
| 3 MFA | TOTP、mfa_ticket、绑定 / 解绑 / 恢复码、会话管理、AC-4 | T8 |
| 4 C 端 | 短信验证码、防刷、OAuth 回调、OIDC 预留 | T6（C）/T7 |
| 5 收口 | 审计回写、`check-authn`、全量门禁、真实 MySQL+Redis 集成测试、文档状态回写 | T9/T10 |

## 验证方式（文档阶段）

1. `git status` 确认只新增 / 修改文档，无代码改动。
2. 交叉引用检查：新文档与 `production-readiness-gaps.md`、`production-readiness-implementation-plan.md`、`SECURITY_STANDARDS.md`、`API_STANDARDS.md`、`CONFIG_STANDARDS.md`、`audit/EVENTS.md` 的双向链接可达。
3. 迁移编号唯一性：`ls migrations/` 确认 `000011` 未占用，文中不再出现 `000009_create_identity_tables`。
4. 验收映射完整性：`AC-ENT-01-1`～`5` 在文档中均有对应用例与命令。
5. 无需运行 `make ci`（仅文档变更），但保留实施阶段的验收命令清单。

## 不做的事（本轮）

- 不新增任何 Go 代码、迁移 SQL、OpenAPI path 或配置字段。
- 不修改 `ENT-02` / `ENT-03` 已关闭内容。
- 不引入新第三方依赖；TOTP 用标准库实现，OIDC 仅预留接口。
