# 身份域与令牌生命周期（Identity & Authentication）

> **状态**：设计生效（v0.1，2026-10-23）；`ENT-01` 阶段 1（账号域）～阶段 5（收口）已全部完成
> **定位**：定义账号域、凭据、会话、令牌签发与撤销、A 端 MFA、三端认证契约；解决「只有验签、没有登录」的上线阻断问题
> **适用范围**：`internal/identity/`、`internal/security/`、`internal/shared/{auth,audit,apperr}`、`internal/platform/`、`pkg/{jwt,totp,password,crypto,redis,sms}`、`cmd/`、`migrations/`、`api/openapi/`、`scripts/ci/`、`docs/`
> **关联**：[`production-readiness-gaps.md`](./production-readiness-gaps.md) `ENT-01`、[`production-readiness-implementation-plan.md`](./production-readiness-implementation-plan.md) `ENT-01`、[`ARCHITECTURE.md`](./ARCHITECTURE.md) §10、[`rbac-iam.md`](./rbac-iam.md)、[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md) `SEC-04`、[`API_STANDARDS.md`](./API_STANDARDS.md)、[`CONFIG_STANDARDS.md`](./CONFIG_STANDARDS.md)、[`DATA_STANDARDS.md`](./DATA_STANDARDS.md)、[`AUDIT_STANDARDS.md`](./AUDIT_STANDARDS.md)、[`TESTING_STANDARDS.md`](./TESTING_STANDARDS.md)、[`RELIABILITY_STANDARDS.md`](./RELIABILITY_STANDARDS.md)、[`docs/audit/EVENTS.md`](./audit/EVENTS.md)
> **最后更新**：2026-11-05（首个管理员改由 `000005` 角色种子 + A 端启动引导开通；移除 `cmd/identity-seed`）
> **变更方式**：调整领域模型、撤销语义、表结构或验收标准须同步更新 [`production-readiness-implementation-plan.md`](./production-readiness-implementation-plan.md) 与 [`production-readiness-gaps.md`](./production-readiness-gaps.md)，经架构 Owner 与安全 Reviewer 评审后合入

---

## 目录

1. [结论与范围](#一结论与范围)
2. [设计前缺口（已闭环）](#二设计前缺口已闭环)
3. [领域模型](#三领域模型)
4. [令牌模型与密钥轮换](#四令牌模型与密钥轮换)
5. [登录、刷新与登出](#五登录刷新与登出)
6. [撤销与会话失效](#六撤销与会话失效)
7. [A 端 MFA 与 SSO](#七a-端-mfa-与-sso)
8. [C 端登录](#八c-端登录)
9. [数据模型与迁移](#九数据模型与迁移)
10. [配置规划](#十配置规划)
11. [审计事件](#十一审计事件)
12. [接口契约](#十二接口契约)
13. [平台接入与分层](#十三平台接入与分层)
14. [任务拆解](#十四任务拆解)
15. [验收标准与用例](#十五验收标准与用例)
16. [门禁](#十六门禁)
17. [风险与回滚](#十七风险与回滚)
18. [实施阶段与状态](#十八实施阶段与状态)
19. [多租户边界与扩展模型](#十九多租户边界与扩展模型biz-09)
20. [错误码与本地化](#二十错误码与本地化biz-05)

---

## 一、结论与范围

### 1.1 结论

平台当前只具备**令牌校验侧**能力：`internal/security` 可验证 HS256 / RS256、`iss` / `aud` / `exp`，但没有任何账号实体、登录端点或会话失效手段。令牌一旦签发，在过期前无法撤销，无法接入真实用户。

`ENT-01` 要补齐的是「身份域是平台底座的一部分」，不是业务功能：

- 账号、凭据、会话、MFA、登录审计由 `internal/identity` 承载；
- 角色 / 权限 / 数据范围仍由 IAM 承载，两张表不互相复制；
- 令牌只表达「你是谁」，不表达「你能做什么」（避免撤权滞后）。

### 1.2 范围

| 纳入 | 说明 |
|---|---|
| 账号域 | `Account`、`Credential`、`Session`、`MFAEnrollment`、`LoginAttempt` 与状态机 |
| 三端令牌 | Access JWT + 一次性 Refresh Token；三端 issuer / audience / 密钥隔离 |
| 生命周期 | 登录、刷新、登出、单会话撤销、全端撤销、账号禁用 / 锁定 |
| 撤销 | 账号级 `token_version` + 会话级 `authn:sid` 快照 + 单会话 `jti` 黑名单 |
| A 端 | TOTP MFA、会话管理；OIDC / SSO 预留接口与开关 |
| C 端 | 短信验证码登录、OAuth 授权码登录；防枚举、防刷 |
| 审计 | 登录、登出、刷新、重放、撤销、MFA、锁定事件的 Outbox 落库 |

| 不纳入 | 原因 |
|---|---|
| 自建完整 IdP、用户目录同步 | 优先复用组织 OIDC；身份域只做账号映射与会话策略 |
| 角色 / 权限判定 | 归 IAM；令牌不固化角色 |
| 账号自助注册的风控体系 | 仅提供可开关的 C 端自动建号与基础限流 |
| 短信 / OAuth 供应商选型 | 复用 `pkg/sms` 与标准 OAuth2 客户端，供应商由配置决定 |

---

## 二、设计前缺口（已闭环）

设计启动时仅有可复用原语（JWT 验签、密码哈希、Redis 缓存/锁、Outbox、审计事件），账号实体、会话表、令牌签发编排、撤销检查与三端认证端点均缺失。上述缺口已由 §三～§十三 落地，当前实现状态见 §十八。

迁移编号按领域合并：身份表 `000001`，IAM 结构 `000002` / 种子 `000003`，平台 async `000004`，MFA bootstrap `000005`。

---

## 三、领域模型

包路径：`internal/identity/{domain,app,infrastructure,transport}`。领域层不依赖框架、数据库与 `internal/platform`。

### 3.1 聚合与实体

| 实体 | 关键字段 | 不变量 |
|---|---|---|
| `Account` | `ID`、`Channel`、`Subject`、`DisplayName`、`Status`、`TokenVersion`、`MerchantID`、`PasswordUpdatedAt`、`DeletedAt` | `(channel, subject)` 全局唯一；`token_version ≥ 1` 且只增不减；禁用账号任何登录路径均拒绝；`DisplayName` 仅用于展示（≤64 rune、禁控制字符、空串回退 `subject`），改名不递增 `token_version` |
| `Credential` | `AccountID`、`Type`、`Hash`、`Algorithm`、`UpdatedAt` | 同一账号同一类型仅一条有效凭据；只存哈希，不存明文 |
| `Session` | `ID`、`AccountID`、`FamilyID`、`RefreshHash`、`Status`、`TokenVersion`、`ExpiresAt`、`RevokedAt`、`RevokedReason` | `RefreshHash` 全局唯一；`rotated` 会话不可再次使用；同一 `FamilyID` 共享撤销量 |
| `MFAEnrollment` | `AccountID`、`SecretCiphertext`、`ConfirmedAt`、`LastUsedStep`、`Version`、`RecoveryCodeHashes` | 未确认的绑定不得用于登录；密钥必须加密存储；同一 TOTP 时间步只能消费一次 |
| `LoginAttempt` | `Channel`、`AccountRef`、`Outcome`、`Reason`、`IPHash`、`UAHash`、`OccurredAt` | 不存明文手机号 / 邮箱 / 密码；保留期遵循审计策略 |

`AccountRef` 为 `subject` 的规范化摘要，用于失败计数与审计关联，禁止直接落明文；生产格式为 `<hash_key_id>:<hex HMAC-SHA256>`（见 §9.5，`IDN-P1-06`），未配置密钥的本地 / 测试环境回退为旧 64 位无前缀摘要。

### 3.2 账号状态机

```text
pending ──激活──► active ──失败阈值──► locked ──到期/解锁──► active
   │                 │                    │
   └──拒绝/清理──► disabled ◄──管理员停用─┘
```

| 迁移 | 触发 | 副作用 |
|---|---|---|
| `pending → active` | 首次密码 / 验证码 / OAuth 登录成功且策略允许 | 初始化 `token_version=1` |
| `active / pending → locked` | 窗口内失败次数达到 `identity.lockout.max_failures` | 写 `auth.account.locked`（仅本次迁移发一次）；拒绝签发令牌 |
| `locked → active` | 锁定到期（下次失败计数前或登录成功时归一）或管理员解锁 | 下次登录重新计数；窗口内失败仍可再次锁定 |
| `active / locked → disabled` | 管理员停用 | `token_version++`、撤销全部会话、写 `auth.account.disabled` |
| `disabled → active` | 管理员恢复 | 不恢复旧会话；用户必须重新登录 |

**状态迁移必须有表驱动用例**：合法迁移成功、非法迁移返回稳定错误、每次迁移产生预期审计事件。

**失败计数规则**（`IDN-P0-03`）：

- 失败计数优先使用 Redis `INCR + EXPIRE`（键 `authn:fail:<channel>:login_fail:<account_ref>`，跨副本共享）；Redis 不可用时回退 `identity_login_attempt` 表计数，保持锁定能力。
- 锁定到期后状态仍为 `locked`：下一次失败计数前必须先归一为 `active`，再按窗口重新计数；窗口内失败仍达到阈值时重新锁定，禁止「到期后永久放行」。
- `pending` 账号允许登录，也必须允许被锁定，避免未激活账号被无限试密码。
- 版本冲突按并发收敛处理：重读后以最新状态为准，返回 `40100`，不得因锁冲突返回 `500`。
- `auth.account.locked` 只在真正发生 `active/pending → locked` 迁移时写一次；已锁定账号的后续失败只写 `auth.login_failed(reason=locked)`。

### 3.3 会话状态机

```text
active ──刷新──► rotated
  │                 │
  ├──登出/撤销──► revoked
  └──到期──────► expired
```

- 刷新只允许 `active → rotated`，并在同一事务内创建新会话（同一 `FamilyID`）。
- 已 `rotated` 的刷新令牌再次出现，视为重放：整个 `FamilyID` 置 `revoked`，可配置是否同时 `token_version++`。
- `revoked` / `expired` 会话永不复活。

### 3.4 领域错误

领域层返回稳定哨兵错误，由应用层映射为 `internal/shared/apperr`：

| 场景 | 语义 | 对外码 |
|---|---|---|
| 账号不存在 / 密码错误 / 验证码错误 | 统一「认证失败」，不区分原因 | `40100` |
| 账号锁定 / 禁用 | 认证失败，审计记录真实原因 | `40100` |
| MFA 未完成 | 认证未完成，返回 `mfa_required` | `40100` |
| 刷新令牌重放 | 会话撤销 | `40100` |
| 登录 / 发送验证码超限 | 限流 | `42900` |
| 撤销存储不可用（`fail_closed`） | 依赖不可用 | `50300` |

---

## 四、令牌模型与密钥轮换

### 4.1 Access Token

| 声明 | 来源 | 说明 |
|---|---|---|
| `sub` | `Account.ID` | 稳定主体 ID，不随渠道变化 |
| `iss` | `auth.jwt.issuer` | 身份域统一 issuer；三端通过 audience / 密钥隔离 |
| `aud` | `auth.jwt.audience` | 必须只包含本端 channel，跨端令牌 401 |
| `iat` / `exp` | 签发时间 / `identity.access_ttl` | 默认 15 分钟 |
| `jti` | 雪花 ID 或随机 ULID | 单会话撤销与重放检测的键 |
| `ver` | `Account.TokenVersion` | 账号级撤销；校验时与 Redis / DB 比较 |
| `channel` | 进程 channel | 与 `aud` 双重校验 |
| `merchant_id` | `Account.MerchantID` | 仅 B 端携带 |
| `roles` | **禁止签发** | 角色由 IAM 快照解析，避免撤权滞后 |

签发约束：

- `exp - iat` 必须等于配置值，禁止由调用方任意指定。
- 同一账号同一时刻可存在多个 `jti`（多设备）；单会话撤销只影响对应 `jti`。
- 令牌原文禁止进入日志、Trace、指标标签与审计 `attrs`。

### 4.2 Refresh Token

- 32 字节密码学随机数，Base64URL 编码，不携带业务语义，不使用 JWT。
- 出参只出现一次；库内仅存 SHA-256 哈希，按哈希比较，避免时序侧信道。
- 前缀区分端别（如 `ar_` / `br_` / `cr_`），便于日志排查时识别类型而不泄露内容。
- 默认有效期 168 小时（`identity.refresh_ttl`）；支持 `session.idle_timeout` 空闲上限。
- 设备另受绝对生命上限 `identity.session.absolute_ttl`（默认 720h）约束：每次刷新只能在该上限内滑动，超限后刷新返回 `40100` 并撤销整条设备链。

### 4.3 密钥环与 `kid`

现状 `pkg/jwt` 不支持 `kid` 头。`T4` 需要扩展：

```yaml
auth:
  mode: jwt
  jwt:
    alg: RS256
    issuer: https://identity.example.com
    audience: [admin]
    active_kid: "2026-10"
    leeway: 30s
    keys:
      - kid: "2026-10"
        public_key_file: /etc/admin-frame/jwt-2026-10.pub.pem
        private_key_file: /etc/admin-frame/jwt-2026-10.key.pem  # 仅签发进程挂载
      - kid: "2026-07"
        public_key_file: /etc/admin-frame/jwt-2026-07.pub.pem
```

规则：

- 签发只在 header 写入 `active_kid`；校验按 `kid` 选中密钥，未知 `kid` 直接失败（不遍历猜测）。
- 轮换期同时保留新旧公钥；旧私钥卸载后，旧令牌自然过期即完成切换。
- HS256 保留 `secret` / `secrets` 兼容路径，但生产推荐 RS256；同一进程不得同时使用两种算法签发。
- `internal/security.JWTConfig` 增加 `ActiveKID`、`Keys`，并保持旧字段可用（弃用标注）；`Validate` 校验 `active_kid` 必须存在于 `keys`，签发进程必须能读取私钥。

### 4.4 三端隔离

| 端 | audience | issuer | 密钥 |
|---|---|---|---|
| A 端 | `admin` | 身份域统一 issuer | 独立密钥对 |
| B 端 | `biz` | 同上 | 独立密钥对 |
| C 端 | `c` | 同上 | 独立密钥对 |

- 任一端的密钥泄露不得影响其他端。
- 校验端同时检查 `aud` 与 `channel` 声明；两者任一不匹配均 `40100`。
- 禁止用 A 端令牌访问 B/C 端（`AC-ENT-01-3`）。

---

## 五、登录、刷新与登出

### 5.1 登录（密码，A/B 端）

```mermaid
sequenceDiagram
    participant C as Client
    participant H as Auth Transport
    participant A as App Service
    participant DB as MySQL
    participant R as Redis
    participant O as Outbox

    C->>H: POST /{channel}/v1/auth/login {subject, password}
    H->>A: Login(channel, subject, password)
    A->>DB: 读 Account + Credential
    A->>A: 校验状态、锁定、Argon2id
    alt 失败
        A->>DB: 写 LoginAttempt(failure)
        A->>O: auth.login_failed
        A-->>H: 统一 401
    else 成功且需 MFA
        A->>DB: 创建 mfa 临时凭证（或签名 mfa_ticket）
        A-->>H: 200 {mfa_required: true, mfa_ticket}
    else 成功
        A->>DB: 写 Session（refresh 哈希）
        A->>O: auth.login
        A-->>H: 200 {access_token, refresh_token, expires_in}
    end
```

要点：

- 账号不存在与密码错误返回同一文案、同一状态码、近似耗时，防枚举。
- 时序对齐（`IDN-P2-04`）：账号不存在、无密码凭据、`locked` / `disabled` 分支在返回统一 `40100` 前，都会对启动时预计算的占位哈希执行一次真实校验（`PasswordService.VerifyDummy`），使耗时与真实密码校验一致；残余差异仅剩登录尝试 / 审计写入与网络噪声。
- 失败计数按 `(channel, account_ref)` 累计；达到阈值转 `locked`，写审计。
- 登录成功后若 `NeedsRehash` 为真，在事务内透明升级密码哈希。
- 登录失败不创建会话；成功但需 MFA 时也不创建正式会话，只返回短期票据。

### 5.2 刷新

```mermaid
sequenceDiagram
    participant C as Client
    participant A as App Service
    participant DB as MySQL
    participant R as Redis

    C->>A: POST /{channel}/v1/auth/refresh {refresh_token}
    A->>DB: 按哈希查 Session
    alt 会话 active 且未过期
        A->>DB: 事务：旧会话 rotated + 新会话 active（同 family）
        A->>R: 写新 jti 白名单（可选）与版本快照
        A->>A: 签发新 Access + Refresh
        A-->>C: 200
    else 会话已 rotated（重放）
        A->>DB: 事务：撤销整个 family
        A->>R: 写入被撤销会话的 jti 黑名单
        A->>A: 审计 auth.token.reused
        A-->>C: 401
    else 会话 revoked / expired
        A-->>C: 401
    end
```

约束（`AC-ENT-01-2`）：

1. 刷新令牌**一次性使用**：每次刷新都轮换，旧令牌立即失效。
2. 并发刷新使用数据库行锁或乐观锁；只有一个请求成功，其余按重放处理。
3. 重放触发**整条会话链**撤销，并记录 `auth.token.reused`，而不是只拒绝当前请求。
4. 刷新不改变 `token_version`；仅登出全部 / 禁用 / 改密才递增。
5. 令牌代过期时间取 `min(now + refresh_ttl, 设备绝对到期时间)`；达到设备绝对上限后拒绝刷新并撤销该设备。

### 5.3 登出

- 默认 `logout`：撤销当前会话（`status=revoked`），写 `authn:sid:<session_id>` 使该会话的 Access Token ≤60s 失效，并把当前 Access 的 `jti` 写入 `authn:revoked:<jti>`（TTL 为令牌剩余寿命）。
- `logout_all`：递增 `token_version`、撤销该账号全部会话，写 `authn:ver:<account_id>`。
- 登出幂等：重复登出返回 204，不产生额外审计噪声（或记 `auth.logout` 一次）。

### 5.4 会话边界

- 单账号活跃会话数受 `identity.session.max_per_account` 限制；超限时按最久未使用淘汰并记审计。
- 设备受绝对生命上限 `identity.session.absolute_ttl` 约束；登录 / 刷新响应与会话列表返回 `absolute_expires_at`，刷新不会延长该值。
- 会话列表只返回 `session_id`、创建时间、最后使用时间、绝对到期时间、设备画像（系统 / 浏览器 / 设备形态 / 脱敏 IP）、状态，不返回令牌，也不返回 IP / UA 原文或摘要。
- 管理员撤销会话只需 `session_id`，不接触令牌原文。

---

## 六、撤销与会话失效

### 6.1 Redis 键设计

| 键 | 值 | TTL | 用途 |
|---|---|---|---|
| `authn:ver:<account_id>` | `token_version\|status\|fetched_at` | `identity.revocation.state_ttl`（默认 `min(access_ttl+1m, 60s)`，硬上限 60s） | 账号级撤销；校验令牌 `ver` 与账号状态，`fetched_at` 用于陈旧度判定 |
| `authn:sid:<session_id>` | `status\|revoked_at\|expires_at\|fetched_at` | `identity.revocation.state_ttl`（≤60s） | 会话级撤销；Access Token 的 `sid` 命中且为 `revoked`/`expired` 时拒绝 |
| `authn:revoked:<jti>` | `1` | 令牌剩余寿命 | 单会话撤销 |
| `authn:fail:<channel>:login_fail:<account_ref>` | 失败次数 | `identity.lockout.window` | 登录失败窗口计数；跨副本共享，Redis 不可用时回退登录尝试表 |

- 所有键统一带环境前缀（复用 `pkg/redis` 的 `KeyPrefix`），跨环境互不影响。
- 写入使用 `SET ... EX`；撤销检查只读，不延长 TTL。
- 版本读取未命中时回源数据库并回填短 TTL；禁止因未命中直接放行（必须先回源）。
- **数据库是唯一事实源，Redis 只做加速层**：命中快照后若 `now - fetched_at` 超过 `state_ttl`（生产上限 60s），必须回源数据库并回填；回源按 `account_id` 合并（singleflight），避免失效瞬间打爆 DB。
- **变更路径统一失效**：禁用、改密、删除、MFA 解绑、`logout_all` 等所有 `token_version` 变更在事务提交后必须经失效器刷新快照；`SET` 失败时回退 `DEL`，使下一次校验必然回源。缓存写入失败只告警（`identity_revocation_cache_write_error_total{op}`），不得把已提交的业务结果改成失败。
- 旧格式快照（缺失 `fetched_at`）一律视为陈旧，首次命中即回源升级格式。

### 6.2 校验链路

`internal/security` 定义消费侧接口（`S-07`）：

```go
// RevocationChecker 由身份域实现，security 只依赖该接口。
type RevocationChecker interface {
    Check(ctx context.Context, claims jwt.Claims) error
}
```

- `NewJWTAuth` 增加 `WithRevocationChecker` 选项；在签名 / `iss` / `aud` / `exp` 通过后执行。
- 返回 `apperr.ErrUnavailable` 时中间件输出 `50300`；返回 `ErrUnauthorized` 时输出 `40100`。
- 校验顺序：签名 → 时间 → issuer / audience / channel → 账号状态 → `token_version` → 会话状态（`sid`）→ `jti` 黑名单。
- 会话状态：`sid` 命中 `authn:sid` 且为 `revoked` / `expired`（或已过 `expires_at`）时拒绝；`rotated` 放行，保证刷新成功后仍在途的旧 Access Token 不被误杀。
- 会话快照与账号快照共享陈旧度上限：命中后超过 `state_ttl` 必须回源 `identity_session`，回源失败按 `failure_mode` 处理；会话不存在视为确定性拒绝。
- 撤销检查结果不得使用长 TTL 进程内缓存；如需缓存，上限 5 秒且必须可主动失效。

### 6.3 失效时限

| 场景 | 生效路径 | 目标时限 |
|---|---|---|
| 单会话登出 | `authn:revoked:<jti>` + `authn:sid` | 下一次请求立即生效 |
| 管理员 / 用户撤销其它设备 | DB `status=revoked` + `authn:sid` | ≤ 60s |
| 账号禁用 | DB `token_version++` + `authn:ver` + 撤销全部会话 | ≤ 60s（`AC-ENT-01-1`） |
| 修改密码 / 登出全部 / MFA 解绑 | 同账号禁用（事务提交后统一失效快照） | ≤ 60s |
| 快照写入失败 | 回退 `DEL`，下一次校验回源 DB；仍失败由 `state_ttl` 过期兜底 | ≤ 60s |
| 刷新令牌重放 | 撤销 family + 写 `authn:sid`（含已 rotated 的历代会话） | ≤ 60s（已签发 Access Token 同步失效） |
| 角色 / 权限变更 | IAM 快照失效（不属本域） | 既有 IAM 语义 |

### 6.4 故障策略

- `identity.revocation.failure_mode=fail_closed`：Redis 不可用返回 `50300`，禁止放行；生产强制。
- `fail_open` 仅允许非生产环境，且需在配置评审中说明；启动时 `Validate` 拒绝生产使用。
- 数据库不可用同样失败关闭；禁止「读不到账号就当作有效」。

### 6.5 监控与告警（IDN-P1-03）

- 撤销 / 回源失败必须留痕：`refreshInTx` 在整链撤销失败时返回 `50300` 并回滚事务，禁止只提交 `auth.token.reused` 而未真正撤销。
- 日志为结构化 Error 级，字段只允许 `trace_id`、`channel`、`account_ref` / `family_ref`（摘要）、`reason`、`error_class`；禁止密码、令牌原文、验证码、TOTP 密钥、恢复码与手机号 / 邮箱原文。
- 指标由 `internal/identity/observability` 统一注册，标签只使用低基数枚举值。

| 指标 | 类型 | 关键标签 | 告警建议 |
|---|---|---|---|
| `identity_login_total` | counter | `channel`、`result`、`reason` | 失败率突增（5m 相对基线） |
| `identity_login_duration_seconds` | histogram | `channel` | p99 异常（依赖或 Argon2 排队） |
| `identity_lockout_total` | counter | `channel` | 突增视为暴力破解 / 恶意锁定 |
| `identity_mfa_verify_total` | counter | `result` | 失败率突增 |
| `identity_mfa_recovery_used_total` | counter | — | 任何非零都通知账号所有者并检查恢复码剩余量 |
| `identity_refresh_total` | counter | `result` | 失败率突增 |
| `identity_token_reuse_total` | counter | — | 任何非零立即告警 |
| `identity_session_revoke_total` | counter | `reason`、`result` | 异常批量撤销；`result=failure` 立即告警 |
| `identity_revocation_store_errors_total` | counter | `op` | 任何非零告警（已失败关闭） |
| `identity_revocation_cache_write_error_total` | counter | `op` | 任何非零告警（依赖 ≤60s TTL 兜底） |
| `identity_sms_send_total` | counter | `result` | 失败率 / 发送量突增 |
| `identity_oauth_callback_total` | counter | `provider`、`result` | 失败率突增 |

- 建议告警：`rate(identity_token_reuse_total[5m]) > 0`、`rate(identity_revocation_store_errors_total[5m]) > 0`、`rate(identity_session_revoke_total{result="failure"}[5m]) > 0`、`rate(identity_mfa_recovery_used_total[5m]) > 0`、登录失败率超过基线、锁定量突增、短信失败率或发送量突增。

---

## 七、A 端 MFA 与 SSO

> **适用端别**：MFA 仅 A 端实现。B 端配置 `identity.mfa.admin_required=true` 会在启动校验阶段失败；B / C 端运行时固定按 `false` 处理，不装配 MFA 密文与票据，也不下发强制绑定挑战。启动日志会打印实际生效值，供运维核对。

### 7.1 TOTP

- 新增 `pkg/totp`：HMAC-SHA1、30 秒步长、6 位、允许 ±1 个窗口；仅依赖标准库，不引入第三方。
- 密钥生成 20 字节随机数，Base32 展示；数据库存 **AES-GCM 密文**，密钥来自 `identity.mfa.secret_key`（非生产）或 ENT-07 密钥托管（生产）。
- **TOTP 防重放（IDN-P1-05）**：`identity_mfa.last_used_step` 记录已消费时间步；校验成功时用 `version` 乐观锁 CAS 推进，同一时间步第二次使用按重放拒绝。
- **恢复码（IDN-P1-05）**：10 字节（80 bit）随机数、按 4 位分组展示；存储为 `SHA-256("recovery|<accountID>|<code>")`（账号加盐）；消费使用 `JSON_REMOVE + JSON_SEARCH` 原子删除，禁止“读整表 → 删除 → 写回”。使用后写 `auth.mfa.recovery_used`（含剩余数量）。
- **密钥轮换**：`identity.mfa.keys[]` + `active_key_id` 组成密钥环，密文头携带 key id；旧格式（单 `secret_key`）仍可解密，并在登录成功后自动重加密为 active key。轮换期间必须保留 `secret_key` 作为 legacy 解密回退，确认所有绑定都重加密后再移除。
- 绑定流程：`mfa/setup` 返回密钥与 `otpauth://` URI → `mfa/verify` 校验首个动态码 → 标记 `confirmed_at` → 写 `auth.mfa.bound`。
- 并发：`identity_mfa.version` 作为乐观锁，`Confirm` / 消费时间步 / 解绑与校验冲突时以“已删除”或“已消费”为准，禁止 upsert 复活。

### 7.2 两步登录

- 密码校验通过且账号已绑定 MFA：返回 `200 {"mfa_required": true, "mfa_ticket": "..."}`。
- `mfa_ticket` 是短期 JWT：`aud=admin`、`scope=mfa`、`exp=5m`、`jti` 独立；只允许调用 `mfa/verify`。
- `mfa/verify` 校验票据与动态码后签发正式 Access / Refresh。
- `identity.mfa.admin_required=true` 时：
  - 未绑定 MFA 的 A 端账号登录返回 `200 {"mfa_binding_required": true, "mfa_setup_ticket": "..."}`，不签发管理令牌，由客户端就地引导绑定（§7.2.1）；
  - 已绑定但两步未完成，绝不签发管理令牌（`AC-ENT-01-4`）。
- `mfa_ticket` 重放：一次性消费（Redis `SETNX`），失败次数计入锁定。
- `mfa_setup_ticket` 重放：返回 `40100` 但**不计入失败次数**（避免恶意锁定），仅写 `auth.mfa.ticket_replay`。
- MFA 校验同时使用票据 jti + 账号级双维度限流，防止轮换票据刷码。

### 7.2.1 强制绑定 MFA 的契约与首个管理员 bootstrap

> **状态**：已定稿并落地（D1=①、D2=①、D3=①、D4=角色种子迁移 + 启动引导、D5 按建议实现）。

**登录响应（`POST /admin/v1/auth/login` → `200`）**

```json
{
  "mfa_binding_required": true,
  "mfa_setup_ticket": "<JWT>"
}
```

- 票据声明：`aud=admin`、`scope=mfa_setup`、`sub=<account_id>`、`exp ≤ 10m`、独立 `jti`；`jti` 在 Redis 一次性消费，键与 `mfa_ticket` 隔离。
- 作用域：**只**允许调用 `POST /admin/v1/auth/mfa/setup` 与 `POST /admin/v1/auth/mfa/verify`；携带该票据访问任何其他端点一律 `40100`。
- 校验顺序：每次使用都重新读取账号状态，禁用 / 锁定立即失效，不依赖票据内的快照。
- 限流沿用 `mfa/verify` 的票据维度分桶（`channel + jti + IP`，绑定流程使用独立前缀 `mfa_setup`）；绑定失败计入锁定并写 `auth.mfa.failed`；成功写 `auth.mfa.bound`。
- `setup` 重复调用：覆盖未确认绑定（旧的未确认 enrollment 作废），已确认绑定仍返回 `40900`。
- 成功绑定：返回 `TokenPair`（与两步登录同形），客户端无需二次输密码。
- 审计：`auth.login_failed` 的 `reason` 对「需绑定」继续使用 `mfa_pending`，不新增 reason。

状态机：

```text
未绑定 --密码通过--> setup_required(mfa_setup_ticket)
setup_required --mfa/setup--> pending（密钥与恢复码仅本次可见）
pending --mfa/verify 首码正确--> bound + TokenPair
pending --mfa/verify 失败 N 次--> locked（按锁定策略）
setup_required / pending --票据过期、重放、账号禁用--> 40100，客户端回登录页
```

**契约与 OpenAPI（已合入）**

- 新增 `MFABindingRequired`：`required: [mfa_binding_required, mfa_setup_ticket]`、`additionalProperties: false`、`mfa_binding_required` 枚举 `[true]`；登录 `200` 的 `data.oneOf` 增加该 schema。
- `MFAVerifyRequest` 新增 `mfa_setup_ticket`（与 `mfa_ticket` 互斥，同时出现返回 `40000`）。
- `mfa/verify` 支持无 `Authorization` 的强制绑定流程：成功返回 `TokenPair`；携带 `Authorization` 时仍返回 `204`。
- 新增 `MFASetupRequest`（`additionalProperties: false`，可选 `mfa_setup_ticket`）；`mfa/setup` 请求体可选。
- `mfa_setup_ticket` 禁止写入日志 / Trace。

**首个管理员 bootstrap（迁移 `000005_seed_bootstrap_admin` 角色种子 + A 端启动引导）**

- `000005_seed_bootstrap_admin` 只创建 `iam_role(system_admin)`、逐项权限授权、`all` 数据范围与存量订正；
  首个管理员（`identity_account`、`identity_credential`、`iam_role_binding`、`auth.account.created` 审计）
  由 A 端 `admin-api` 启动时 `identityruntime` 调用 `bootstrap.EnsureAdmin` 在同一事务内幂等创建。
- 迁移脚本不含明文登录主体：账号主体由启动引导用应用密钥计算 HMAC 索引并 AES-256-GCM 加密写入，
  因此迁移可以一次跑完，不需要中途停下等待回填，也不需要独立 CLI。
- 只在账号不存在时创建；已有环境只校验凭据与绑定，不覆盖密码、不改变已确认状态。
- 初始口令是仓库内公开的演练值，因此**依赖强制改密**在首次登录时替换；首次登录顺序为
  「强制改密 → MFA 强制绑定 → 获得管理令牌」，运维全程不接触 TOTP 密钥。
- 登录名固定为系统默认 `admin@example.com`；角色固定 `system_admin`，
  逐项授予生效的 A 端权限并排除 `iam:breakglass:approve`，不写 `*`。
- 引导仅限 A 端且数据库持久化时执行：B/C 端进程与内存库跳过，避免创建跨端账号。
- 回滚 `000005_seed_bootstrap_admin.down.sql` 只删除 `system_admin` 角色及其授权 / 数据范围 / 绑定；
  账号与凭据由启动引导创建，不随迁移回滚删除。

**验收（已并入 §15）**

- `AC-ENT-01-6`：未绑定账号在 `admin_required=true` 下可凭登录返回的 `mfa_setup_ticket` 完成绑定并直接获得令牌，全程无需人工改库。
- `AC-ENT-01-7`：首个管理员由 A 端 `admin-api` 启动引导幂等开通（账号已存在时只校验授权），
  开通审计落 Outbox，口令与 TOTP 密钥不进入日志。
- `mfa_setup_ticket` 不能访问任何业务接口；过期 / 重放返回 `40100` 且不产生绑定副作用。

### 7.3 OIDC（已实现，SAML / SCIM / M2M 待交付）

- `identity.oauth.providers[]` 的每个 provider 必须显式 `oidc_enabled: true` 才启用 OIDC；默认关闭。
  - 开启时 `issuer` 与 `jwks_url` 必填且必须成对出现，`userinfo_url` 可省略；
  - 关闭时若仍填写 `issuer` / `jwks_url`，启动期校验直接失败，避免隐式启用或配置漂移。
- 流程固定：state + nonce + PKCE（S256）。nonce 由服务生成并写入一次性 state（TTL 10 分钟），回调时回传校验；缺失或与 id_token 不一致统一返回 `40100`。
- id_token 校验：仅接受 `RS256`；校验签名（JWKS，支持 `kid` 未知时限频强制刷新以完成密钥轮换）、`iss`、`aud`（默认 `client_id`）、`exp`/`iat`（默认 30s 时钟偏差）与 `nonce`。
- 主体取自 id_token 的 `sub`；OIDC 模式下不允许回退到 userinfo，避免绕过签名校验。
- 配置项：`audience`（默认 `client_id`）、`jwks_ttl`（默认 15m，范围 `[1m, 24h]`）、`jwks_min_refresh_interval`（默认 1m）、`id_token_leeway`（默认 30s）。
- 可观测性：
  - `identity_oauth_callback_total{provider,result,reason}`：`reason=ok|oauth_invalid|oauth_not_configured|unavailable|rate_limited|internal`；
  - `identity_oauth_jwks_refresh_total{provider,result}`：公钥集刷新成功 / 失败次数；
  - `identity_oauth_jwks_cache_age_seconds{provider}`：距上次成功拉取的秒数，持续增长表示 IdP 不可达或刷新失败。
- 密钥轮换与故障处置（Runbook）：
  - IdP 先在 JWKS 中并行发布新 `kid`，再切换 id_token 签名密钥；旧公钥在缓存 `jwks_ttl` 内仍可验证。
  - 未知 `kid` 会在 `jwks_min_refresh_interval` 限频内强制刷新一次，用于快速收敛轮换。
  - 刷新失败时保留上一份可用公钥：已缓存的 `kid` 继续验证，未知 `kid` 失败关闭（`40100` / `50300`），不降级到 userinfo。
  - `identity_oauth_jwks_cache_age_seconds` 持续超过 `jwks_ttl` 时，检查到 IdP 的网络、证书信任链与 JWKS 端点；恢复后下一次校验自动刷新。
- 验收用例：`TestOIDCEndToEndAuthorizationCodeFlow`（授权码 + PKCE + nonce 全链路、授权码重放与 nonce 不匹配拒绝）、`TestJWKSRetainsCacheWhenRefreshFails`（故障期保留旧公钥、未知 kid 失败关闭）、`TestOIDCRequiresExplicitFlag`（显式开关）。
- 账号映射：`(provider, provider_subject) → account_id`；找不到时按 `identity.oauth.auto_provision` 决定拒绝或建号。
- 仍待交付：SAML 2.0、SCIM 目录同步、M2M 客户端凭证与密钥轮换；这些能力未实现前不得在配置或文档中声明已启用。

---

## 八、C 端登录

### 8.1 短信验证码

- `POST /c/v1/auth/sms/send`：6 位数字验证码，Redis 存哈希，TTL 5 分钟。
- 手机号必须为 E.164（`^\+[1-9]\d{2,14}$`）；账号定位使用 `subject = "phone:" + sha256(手机号)`（hex），库内不落明文手机号。
- 短信驱动 `identity.sms.driver=memory|aliyun`：`memory` 仅本地 / 测试；生产禁止 `memory`，阿里云凭证只从 `ALIYUN_SMS_ACCESS_KEY_ID` / `ALIYUN_SMS_ACCESS_KEY_SECRET` 读取。
- 限制：
  - 单手机号 60 秒内只能发送一次；
  - 单手机号每日上限、单 IP 每小时上限（`identity.sms.*`）；
  - 验证码最多尝试 5 次，超限作废并记审计；
  - 响应恒定（无论手机号是否存在），防枚举。
- 供应商熔断与余额告警（`IDN-P2-05`）：连续可重试故障达到 `identity.sms.breaker_failures`（默认 5）后打开熔断，`breaker_timeout`（默认 60s）内不再触网；模板 / 签名 / 号码等永久拒绝不计入熔断。厂商返回余额不足（如 `isv.AMOUNT_NOT_ENOUGH`）时写 Error 日志并递增 `identity_sms_provider_alerts_total{kind="insufficient_balance"}`。
- 未注册手机号不自动建号：登录端点验证码校验通过但账号不存在时返回 `40100`（与验证码错误一致）。
- `POST /c/v1/auth/sms/register`：验证码通过后在同一事务内创建账号（`subject = "phone:" + sha256(手机号)`）、手机号凭据与 `auth.account.created` 审计，并直接签发令牌；已注册手机号与验证码错误统一 `40100`，并发开户由 `(channel, subject)` 唯一键兜底。
- 日志与审计禁止出现完整手机号；只记录不可逆摘要。

### 8.2 OAuth 授权码

- `POST /c/v1/auth/oauth/{provider}/authorize`：服务端生成一次性 state 与 PKCE verifier（S256），返回授权地址；state 存 Redis，TTL 10 分钟，一次性消费。
- `POST /c/v1/auth/oauth/{provider}/callback`：code + state → 换取令牌；OIDC 模式（`oidc_enabled: true`）下校验 id_token 的 nonce / 签名 / issuer / audience 并取 `sub`，其余模式读取 userinfo（`subject_field`，默认 `sub`）→ 映射账号。
- provider 未配置时不注册路由；state 无效、provider 不匹配、授权码被拒与未映射身份统一返回 `40100`，IdP 故障返回 `50300`。
- 账号映射表 `identity_oauth_identity`，`UNIQUE(provider, provider_subject)`。
- 自动建号受 `identity.oauth.auto_provision` 控制，默认关闭；建号主体为 `subject = "oauth:<provider>:" + sha256(provider_subject)`，避免第三方主体含 PII 或超长。

### 8.3 限流与防刷

| 端点 | 分桶键 | 默认 |
|---|---|---|
| 登录 | 账号级 `channel:login:<account_ref>` + IP 级 `channel:login::<ip_hash>` | 双维度各 1 rps / burst 5，任一超限即拒绝 |
| 发送验证码 | 手机号级 + IP 级 + 全局日预算 | 每 60s 1 次；单手机号日限、单 IP 时限、`daily_budget` |
| 刷新 | `channel + session 摘要` | 5 rps / burst 10 |
| MFA 校验 | `channel + account + IP` | 1 rps / burst 5 |
| OAuth authorize / callback | 全局 `authorize:global` / `callback:global` + IP 级 | 1 rps / burst 5，Redis `fail_closed`（`IDN-P2-05`） |

- 认证端点不挂全局 `AuthMW`，但必须挂独立限流中间件；限流命中返回 `42900` 并带 `Retry-After`。
- 登录限流有 Redis 时使用 `pkg/ratelimit.NewRedisKeyed` 跨副本共享配额，`identity.login_rate_limit.failure_mode` 生产强制 `fail_closed`；无 Redis 时退化为进程内实现（仅单副本）。
- OAuth 同样使用 `pkg/ratelimit.NewRedisKeyed`（键前缀 `identity:oauth:`），authorize 与 callback 各自检查全局 + 单 IP，防止刷爆 state 键空间。
- 短信发送失败会回滚重发窗口（`ResetResend`），避免用户没收到验证码却被锁在重发间隔内。
- 短信供应商熔断（`IDN-P2-05`）：`pkg/circuitbreaker` 包裹发送调用，打开期间直接 50300；状态迁移写 Warn 日志（provider / from / to）。
- 余额告警：`identity_sms_provider_alerts_total{kind,provider}` 任何非零都应配置告警规则并通知运维；告警日志只含 kind / provider / 厂商码，不含手机号与正文。
- Argon2 并发保护（`IDN-P2-05`）：`identity.password.max_concurrent`（默认 `2×CPU`）限制同时校验数量，`wait_timeout` 内拿不到槽位返回 `50300`，且不计入登录失败、不触发锁定。

### 8.4 客户端 IP 与可信代理（IDN-P1-04）

- 统一由 `pkg/httpx.ClientIPResolver` 解析客户端 IP：只有 `http.trusted_proxies`（CIDR 列表）内的请求才解析 `X-Forwarded-For`，并从右向左跳过可信代理取第一个不可信地址。
- 未配置可信代理或来源不可信时忽略 XFF，直接使用 `RemoteAddr` 并去掉端口；IPv6 统一为规范文本，保证同一地址的不同连接共享限流键。
- identity 的 A/B/C 端 Handler 与平台级 `rate_limit` 共用同一解析器，禁止各自实现 XFF 解析。

---

## 九、数据模型与迁移

### 9.1 迁移

- 文件：`migrations/000001_create_identity_tables.{up,down}.sql`。
- 编号说明：迁移已按领域合并为 `000001`～`000004`：`000001` 身份域、`000002` IAM 结构（含菜单）、`000003` IAM 种子、`000004` 平台异步；后续变更从 `000005` 顺延。
- 摘要列（`account_ref` / `ip_hash` / `ua_hash`）宽度为 128，容纳 `<key_id>:<hex>` 格式（`IDN-P1-06`）；已随基线合并进 `000001`。
- 只由 `cmd/migrate` 或部署 Job 执行；API / worker 启动只校验版本，不执行迁移（`DATA_STANDARDS`）。

### 9.2 表结构

| 表 | 主键 | 关键唯一键 / 索引 | 说明 |
|---|---|---|---|
| `identity_account` | `id BIGINT` | `UNIQUE(channel, subject)`；`INDEX(channel, status)` | 账号状态与 `token_version` |
| `identity_credential` | `id BIGINT` | `UNIQUE(account_id, credential_type)` | 密码 / 手机号绑定；只存哈希 |
| `identity_session` | `id BIGINT` | `UNIQUE(refresh_token_hash)`；`INDEX(account_id, status)`；`INDEX(family_id)` | 刷新会话链 |
| `identity_mfa` | `account_id BIGINT` | 主键即账号；`version` 乐观锁 | TOTP 密文、恢复码哈希、`last_used_step` |
| `identity_login_attempt` | `id BIGINT` | `INDEX(channel, account_ref, occurred_at)` | 失败计数与审计关联 |
| `identity_oauth_identity` | `id BIGINT` | `UNIQUE(provider, provider_subject)` | 外部身份映射 |

通用约束：

- 时间列统一 `DATETIME(3)`，应用写入 UTC；`created_at` / `updated_at` 必填。
- 字符集 / 排序规则与现有表一致：`utf8mb4` / `utf8mb4_0900_ai_ci`。
- 主键使用雪花 ID（`pkg/id`），禁止自增。
- 软删使用 `deleted_at`；唯一键须保证软删后标识不可复用（账号 `subject` 不复用）。
- 外键仅用于阻止悬空会话与凭据（`RESTRICT`）；会话清理走独立保留期任务，不在 API 进程执行。
- 密码哈希、刷新哈希长度预留 ≥ 255；TOTP 密文使用 `VARBINARY` 或 `TEXT`。
- `identity_account.must_change_password`（`000010` 迁移）标记管理员重置密码后尚未改密；为 1 时登录只下发 `password_change_ticket`。

### 9.3 账号生命周期与强制改密（IDN-P0-02）

- A 端运营接口分两类权限：查询用 `identity:account:read`，处置用 `identity:account:write`；未注入权限中间件时失败关闭（403）。
- 列表按创建时间倒序分页，支持 `status` 与 `keyword`（主体子串）；软删账号照常返回并带 `deleted_at`。
- 管理员重置密码返回一次性临时口令，同时置 `must_change_password`、递增 `token_version` 并撤销全部会话；临时口令不写日志、不入审计。
- 用户凭临时口令登录只得到 `200 {"password_change_required": true, "password_change_ticket": "..."}`；凭票据调用 `POST /admin/v1/auth/password/change` 完成改密后才签发令牌（若已绑定 MFA，则改密后仍需完成两步校验）。
- 软删账号保留 `subject` 唯一键，恢复不恢复旧会话，`token_version` 不递减。
- C 端换绑手机号会同时更新 `subject` 与 `phone` 凭据、递增 `token_version` 并撤销全部会话；自助注销为软删。

### 9.4 保留与清理

- `identity_login_attempt` 保留期与审计策略一致，默认 180 天，由独立任务清理。
- `identity_session` 过期记录保留 30 天后清理；清理任务不得删除 `revoked` 状态未满保留期的记录。
- 不在 API 进程内做批量删除；清理逻辑由 worker 或独立 Job 执行。

### 9.5 PII 摘要与域隔离（IDN-P1-06）

所有低熵标识统一使用带密钥摘要，实现位于 `pkg/crypto/hasher.go` 与 `internal/identity/app/hashing.go`。

- 算法：`HMAC-SHA256(key, domain || "|" || value)`；`value` 在调用侧规范化：账号 / 手机号主体去首尾空白并小写，客户端 IP / UA 仅去首尾空白；`account-ref` 的输入为 `<channel>|<subject>`，`sms-code` 为 `<phoneRef>|<code>`。
- 存储格式：`<key_id>:<hex>`；`key_id` 取自 `identity.hash_key_id`（默认 `v1`），仅 `[A-Za-z0-9_-]` 且 ≤ 16 字节。
- 域（domain）清单：`account-ref`、`client-ip`、`client-ua`、`phone-subject`、`sms-code`、`recovery-code`、`oauth-subject`；跨域不可复用。
- 短信码：`HMAC-SHA256(key, "sms-code|" + phoneRef + "|" + code)`，校验使用常量时间比较；Redis / 备份泄露后无 key 无法离线枚举。
- 轮换：`identity.hash_key` 切换并把旧 key 放入 `previous_hash_keys`，写入始终使用 active key，读取遍历 active + 历史 key 双读；确认历史数据自然淘汰（最长不超过登录尝试 / 会话保留期）后再移除旧 key。
- 兼容：未配置 `hash_key`（local / test）时回退旧无盐 SHA-256；prod / staging 启动校验强制要求 `hash_key`，且 `identity_account.subject` 中的 `phone:<digest>` 只在新建 / 换绑时按新格式写入，存量账号保留旧值（不可反推）。

---

## 十、配置规划

### 10.1 配置结构

```yaml
auth:
  mode: jwt
  expected_channel: admin
  jwt:
    alg: RS256
    issuer: https://identity.example.com
    audience: [admin]
    active_kid: "2026-10"
    leeway: 30s
    keys:
      - kid: "2026-10"
        public_key_file: /etc/admin-frame/jwt-2026-10.pub.pem
        private_key_file: /etc/admin-frame/jwt-2026-10.key.pem

identity:
  enabled: true
  access_ttl: 15m
  refresh_ttl: 168h
  session:
    idle_timeout: 72h
    absolute_ttl: 720h         # 设备绝对生命上限，必须大于 refresh_ttl
    max_per_account: 10        # 单账号活跃设备上限，按 family 淘汰生效
  retention:
    session_days: 30           # 生产 >= 7
    login_attempt_days: 180    # 生产 >= 180
    cleanup_interval: 1h       # >= 1m
  revocation:
    driver: redis              # redis 为生产唯一实现
    failure_mode: fail_closed  # fail_closed|fail_open
    state_ttl: 30s             # 账号快照最大陈旧度，默认 min(access_ttl+1m, 60s)
  password:
    min_length: 12             # >= 8
    max_length: 128            # <= 4096
    require_complexity: true   # 至少三类字符；显式 false 才关闭
    max_concurrent: 0          # Argon2 并发上限；0=2×CPU
    wait_timeout: 2s           # 获取槽位等待上限 [100ms, 30s]，超时 50300
  lockout:
    max_failures: 5
    window: 15m
    duration: 15m
  mfa:
    admin_required: true
    issuer: AdminFrame
    secret_key: ""             # 旧格式回退密钥；仅非生产，生产走 ENT-07 密钥托管
    active_key_id: ""          # keys 非空时必填
    keys: []                   # [{id, key}] 密钥环；key 为 32 字节，生产走环境变量
  login_rate_limit:
    rps: 1
    burst: 5
    failure_mode: fail_closed  # Redis 不可用时拒绝（生产强制）
  hash_key: ""                 # PII / 短信码摘要主密钥，32 字节；prod/staging 必填，走 ADMIN_FRAME_IDENTITY_HASH_KEY
  hash_key_id: v1              # 摘要前缀，仅 [A-Za-z0-9_-]，≤16 字节
  previous_hash_keys: []       # 轮换期历史密钥 [{id, key}]，双读用
  sms:
    enabled: false
    driver: memory             # memory（本地 / 测试）| aliyun（生产必须）
    template_id: auth_login_code
    sign_name: ""
    locale: zh-CN
    code_ttl: 5m
    max_attempts: 5
    resend_interval: 60s
    daily_per_phone: 10
    hourly_per_ip: 30
    daily_budget: 0            # 0=不限
    breaker_failures: 5        # 连续可重试故障阈值
    breaker_timeout: 60s       # 熔断打开时长 [1s, 10m]
  oauth:
    auto_provision: false
    rate_limit:                # authorize / callback 全局限流（IDN-P2-05）
      rps: 1
      burst: 5
      failure_mode: fail_closed
    providers: []              # [{name, client_id, client_secret_env, auth_url, token_url, userinfo_url, redirect_uri, scopes, subject_field,
                               #   oidc_enabled(默认 false), issuer, jwks_url, audience, jwks_ttl, jwks_min_refresh_interval, id_token_leeway}]
```

### 10.2 校验规则

| 场景 | 规则 |
|---|---|
| 枚举 | `revocation.driver` 仅 `redis`；`failure_mode` 仅 `fail_closed` / `fail_open` |
| 范围 | `access_ttl ∈ [5m, 1h]`；`refresh_ttl > access_ttl`；`idle_timeout ∈ (0, refresh_ttl]`；`absolute_ttl > refresh_ttl`；`max_failures ≥ 1`；`max_per_account ≥ 1`（按 family 淘汰生效） |
| 密码策略 | `password.min_length ∈ [8, max_length]`；`max_length ≤ 4096`；未配置时取 12～128 且 `require_complexity=true`；显式 `false` 才关闭复杂度 |
| 密码并发 | `password.max_concurrent ≥ 0`（0=2×CPU）；`password.wait_timeout ∈ [100ms, 30s]`，0 取默认 2s；超出等待返回 50300 |
| OAuth 限流 | `oauth.rate_limit.rps ≥ 0`、`burst ≥ 0`（0 取默认 1 / 5）；`failure_mode` 仅 `fail_closed` / `fail_open`，prod/staging 强制 `fail_closed` |
| OIDC 开关 | `providers[].oidc_enabled` 默认 `false`；开启时 `issuer` 与 `jwks_url` 必填且成对；关闭时填写 `issuer` / `jwks_url` 视为配置错误 |
| OIDC 缓存 | `jwks_ttl ∈ [1m, 24h]`（默认 15m）；`jwks_min_refresh_interval ∈ [5s, jwks_ttl]`（默认 1m）；`id_token_leeway ∈ [0, 5m]`（默认 30s） |
| 短信熔断 | `sms.breaker_failures ≥ 0`（0=5）；`sms.breaker_timeout ∈ [1s, 10m]`，0 取默认 60s |
| 持久化 | prod/staging 且 `identity.enabled=true` 时 `db.driver` 不得为 `memory`；运行时 `db.driver` 非 memory 但无数据库连接直接启动失败，禁止回退内存仓储 |
| 审计出口 | prod/staging 且 `identity.enabled=true` 时 `async.enabled=true`（审计写入 Outbox）；启动日志输出 `identity audit sink`（`outbox` / `slog`） |
| 撤销陈旧度 | `revocation.state_ttl ∈ [1s, 60s]`；未配置时取 `min(access_ttl+1m, 60s)`，禁止超过 60s |
| 必填 | `identity.enabled=true` 时 `auth.mode` 必须为 `jwt`，且 `auth.jwt.active_kid` 存在 |
| 生产强制 | `auth.mode=jwt`；`identity.enabled=true`；`revocation.driver=redis`；`revocation.failure_mode=fail_closed`；A 端 `mfa.admin_required=true` |
| 限流 | `login_rate_limit.failure_mode` 仅 `fail_closed` / `fail_open`，prod/staging 强制 `fail_closed`；`sms.daily_budget ≥ 0` |
| 可信代理 | `http.trusted_proxies` 每项必须是合法 CIDR；为空表示不解析 XFF |
| 生产禁止 | `auth.mode=demo`；`failure_mode=fail_open`；`sms.enabled=false` 且 `oauth.providers` 为空（C 端无可用登录方式） |
| MFA 端别 | 仅 `channel=admin` 支持；`channel=biz` 设置 `mfa.admin_required=true` 启动失败；`channel=c` 忽略该配置 |
| 密钥 | `secret_key` 不得写入示例配置；`admin_required=true` 时 `secret_key` 或 `keys` 至少一个；密钥须为 32 字节（raw / hex / base64 / base64url）；`keys` 非空时 `active_key_id` 必填且必须命中；生产由环境变量注入，ENT-07 接入密钥托管后再切换 |
| 摘要密钥 | prod/staging **强制** `identity.hash_key`（32 字节，同编码规则）；`hash_key_id` 仅 `[A-Za-z0-9_-]` 且 ≤ 16 字节，缺省 `v1`；`previous_hash_keys[].id` 不得重复且不得与 active 相同；配置历史密钥但缺主密钥启动失败；示例配置中的 `hash_key` 必须为空 |
| 脱敏 | `LogValue` 输出 `secret_key` 为掩码；provider `client_secret` 只输出是否配置 |

环境变量前缀统一 `ADMIN_FRAME_IDENTITY_<SECTION>_<KEY>`（如 `ADMIN_FRAME_IDENTITY_REVOCATION_FAILURE_MODE`）。

---

## 十一、审计事件

### 11.1 事件目录

复用 `internal/shared/audit` 的 `Event` 与 `Outcome`；事件名沿用现有 `auth.` 前缀。

| 事件 | 触发 | 关键字段 | 说明 |
|---|---|---|---|
| `auth.login` | 登录成功 | `actor_id`、`channel`、`merchant_id`、`trace_id` | 不记录令牌 |
| `auth.login_failed` | 登录失败；MFA 校验遇到 locked / disabled | 同上 + `reason`（`bad_credential` / `locked` / `disabled` / `mfa_pending`） | 文案对外统一；MFA 分支只在拒绝前写一条 |
| `auth.account.created` | A 端启动引导 / 短信自助开户 / OAuth 自动建号 | `actor_type`（启动引导与 OAuth 为 `system`）、`attrs.provider`（仅 provider 名）、`attrs.subject_index`（不写明文主体） | 与账号写入同事务；不记录外部主体原文 |
| `auth.logout` | 登出 / 会话撤销 | `actor_id`、`session_id` 摘要 | 幂等 |
| `auth.token.refresh` | 刷新成功 | `actor_id`、`family_id` 摘要 | 不记录令牌 |
| `auth.token.reused` | 刷新令牌重放 | 同上 + `reason=reuse_detected` | 触发整链撤销 |
| `auth.token.revoked` | 单会话 / 全端撤销 | `actor_id`、`session_id` 摘要 | 含管理员操作 |
| `auth.mfa.bound` | MFA 绑定完成 | `actor_id` | 不记录密钥 / 恢复码 |
| `auth.mfa.failed` | MFA 校验失败 | `actor_id`、`reason` | 计入锁定 |
| `auth.mfa.recovery_used` | 恢复码消费成功 | `actor_id`、`attrs.remaining` | 不记录码值；剩余数量用于告警 |
| `auth.mfa.disabled` | MFA 解绑 | `actor_id` | 同时递增 `token_version` 并撤销全部会话 |
| `auth.account.locked` | 达到失败阈值 | `actor_id`、`channel` | 由系统触发 |
| `auth.account.disabled` | 管理员停用账号 | 操作人 + 目标账号 | 同时撤销会话 |

### 11.2 落库与一致性

- 审计写入与账号 / 会话写入必须**同一事务**：复用 IAM 的 UnitOfWork + `async_outbox` 模式（`topic=audit.event`）；OAuth 自动建号的账号、外部身份映射与 `auth.account.created` 必须原子提交。
- 内存模式（`db.driver=memory`）允许退化为直接 `SlogEmitter`，仅限 local / test。
- 生产链路固定为 `identity.audit` Outbox → worker `AuditEventHandler`；在审计库 / SIEM 接入前，worker 输出结构化 `audit_event` 日志，必须配置日志采集与保留 ≥180 天（风险例外，需在部署清单中登记）。启动日志打印 `identity audit sink`，便于核对。
- 审计失败时认证关键写入必须回滚（失败关闭）；禁止「登录成功但审计丢失」。
- 事件不得包含令牌、密码、验证码、完整手机号、TOTP 密钥、恢复码。
- 同步更新 [`docs/audit/EVENTS.md`](./audit/EVENTS.md) 与 `internal/shared/audit/actions.go`；新增 action 必须进入 `KnownActions()` 并由单测固定。

---

## 十二、接口契约

### 12.1 端点清单

| 端 | 方法与路径 | 认证 | 说明 |
|---|---|---|---|
| A/B/C | `POST /{端}/v1/auth/login` | 无 | 密码（A/B）或手机号 + 验证码（C） |
| A/B/C | `POST /{端}/v1/auth/refresh` | 无（凭刷新令牌） | 一次性轮换 |
| A/B/C | `POST /{端}/v1/auth/logout` | Bearer | 撤销当前会话 |
| A/B | `POST /{端}/v1/auth/logout-all` | Bearer | 递增 `token_version` |
| A | `POST /admin/v1/auth/mfa/setup` | Bearer | 生成 TOTP 密钥 |
| A | `POST /admin/v1/auth/mfa/verify` | 无（凭 `mfa_ticket`）/ Bearer（绑定确认） | 完成两步登录或绑定确认 |
| A | `POST /admin/v1/auth/mfa/disable` | Bearer | 解绑（需二次验证） |
| A | `GET /admin/v1/auth/sessions` | Bearer | 列出本账号会话 |
| A | `DELETE /admin/v1/auth/sessions/{id}` | Bearer | 撤销指定会话 |
| A | `POST /admin/v1/auth/sessions/revoke-all` | Bearer | 撤销全部会话 |
| A | `POST /admin/v1/auth/password/change` | Bearer / `password_change_ticket` | 自助改密或强制改密（递增 `token_version`、撤销全部会话） |
| A | `GET /admin/v1/auth/accounts` | Bearer + `identity:account:read` | 账号列表（分页、状态 / 关键词筛选，含软删） |
| A | `GET /admin/v1/auth/accounts/{id}` | Bearer + `identity:account:read` | 账号详情（凭据与 MFA 状态） |
| A | `GET /admin/v1/auth/accounts/{id}/sessions` | Bearer + `identity:account:read` | 目标账号会话列表 |
| A | `POST /admin/v1/auth/accounts/{id}/disable` | Bearer + `identity:account:write` | 停用账号并撤销全部会话 |
| A | `POST /admin/v1/auth/accounts/{id}/enable` | Bearer + `identity:account:write` | 恢复停用账号（不恢复旧会话） |
| A | `POST /admin/v1/auth/accounts/{id}/lock` | Bearer + `identity:account:write` | 主动锁定（可设时长）并撤销全部会话 |
| A | `POST /admin/v1/auth/accounts/{id}/unlock` | Bearer + `identity:account:write` | 管理员解锁 |
| A | `POST /admin/v1/auth/accounts/{id}/mfa/reset` | Bearer + `identity:account:credential` | 重置 MFA（丢失设备兜底） |
| A | `POST /admin/v1/auth/accounts/{id}/password/reset` | Bearer + `identity:account:credential` | 重置密码，返回一次性临时口令并强制改密 |
| A | `DELETE /admin/v1/auth/accounts/{id}` | Bearer + `identity:account:delete` | 软删账号 |
| A | `POST /admin/v1/auth/accounts/{id}/restore` | Bearer + `identity:account:write` | 恢复软删账号 |
| A | `DELETE /admin/v1/auth/accounts/{id}/sessions/{sid}` | Bearer + `identity:account:session` | 撤销目标账号指定会话 |
| A | `POST /admin/v1/auth/accounts/{id}/sessions/revoke-all` | Bearer + `identity:account:session` | 撤销目标账号全部会话 |
| C | `POST /c/v1/auth/sms/send` | 无 | 发送验证码 |
| C | `POST /c/v1/auth/sms/register` | 无 | 验证码注册开户并登录（方案 A） |
| C | `POST /c/v1/auth/phone/rebind` | Bearer | 换绑手机号并撤销全部会话 |
| C | `DELETE /c/v1/auth/account` | Bearer | 用户自助注销（软删） |
| C | `POST /c/v1/auth/oauth/{provider}/authorize` | 无 | 发起授权，返回 state 与授权地址 |
| C | `POST /c/v1/auth/oauth/{provider}/callback` | 无 | 授权码回调 |

### 12.2 请求 / 响应约束

- 登录请求只接受 `subject`（A/B）或 `phone`（C）与凭据；不接受 `channel`、`roles`、`merchant_id` 由客户端指定。
- `merchant_id` 由账号绑定关系推导，B 端令牌不得跨商户。
- 登录响应：`access_token`、`refresh_token`、`token_type=Bearer`、`expires_in`；需 MFA 时只返回 `mfa_required` 与 `mfa_ticket`。
- 错误信封沿用 `httpx` 统一格式；登录失败统一 `40100`，不区分账号不存在 / 密码错误。
- 强制绑定 MFA：登录返回 `200 {"mfa_binding_required": true, "mfa_setup_ticket": "..."}`；该票据只能调用 `mfa/setup` 与 `mfa/verify`，绑定成功后直接返回 `TokenPair`（§7.2.1）。
- 认证端点必须声明 `Idempotency-Key` 可选策略（写接口），刷新与登出按幂等语义处理。
- 禁止缓存（`IDN-P2-03`）：所有认证、账号与 `/me` 端点由路由层统一追加 `Cache-Control: no-store, no-cache, must-revalidate, private`、`Pragma: no-cache`、`Expires: 0`、`Vary: Authorization`（`pkg/httpx.NoStoreMiddleware`）；登录 / 刷新 / MFA setup 的敏感载荷禁止经浏览器或共享缓存复用。

### 12.3 OpenAPI

- `api/openapi/{admin,biz,c}.yaml` 新增 `auth` tag 与上述 path；组件 schema 放 `components.yaml` 复用。
- `info.version` 提 minor；破坏性调整提 major。
- 错误码至少覆盖 `40000`、`40100`、`42900`、`50300`。
- `check-openapi` 必须通过；新增 `check-authn` 校验三端均存在认证路径。

---

## 十三、平台接入与分层

### 13.1 目录与依赖

```text
internal/identity/
├── domain/          # 聚合、状态机、仓储接口、领域错误
├── app/             # 登录 / 刷新 / 登出 / 撤销 / MFA 用例，事务与审计编排
├── infrastructure/
│   ├── persistence/ # MySQL / 内存仓储
│   ├── cache/       # Redis 撤销与验证码存储
│   └── oauth/       # OAuth / OIDC 适配（预留）
└── transport/
    ├── admin/       # /admin/v1/auth/*
    ├── biz/         # /biz/v1/auth/*
    ├── c/           # /c/v1/auth/*
    └── rest/        # 通道无关 Handler / DTO
```

- `identity` 禁止 import `internal/platform`；`transport` 只依赖 `internal/shared` 与 `pkg/*`。
- `platform` 不得 import `internal/identity`；通过 `cmd` 注入接口。
- 端差异只体现在 `cmd` 装配与 `transport`，领域模型三端共享。

### 13.2 装配顺序

现状 `platform.Run` 先构造 `AuthMW`、后构造 `NewAuthorizer`，撤销检查无法注入。调整为：

```text
配置 → 日志 / 指标 / 追踪 / 存储 / DB / Redis
→ 构造 Deps（不含 AuthMW）
→ NewSessionValidator(deps)（cmd 构造身份运行时，返回撤销检查函数）
→ NewAuthMiddleware(cfg.Auth, channel, env, WithRevocationChecker(...))
→ NewAuthorizer / Register（复用同一身份运行时注册路由）
→ Pipeline → ListenAndServe
```

- `platform.Options` 新增 `NewSessionValidator func(*Deps) (SessionValidator, error)`；`SessionValidator` 为平台侧最小接口，避免反向依赖身份域。
- `cmd/{admin,biz,c}-api` 用 `sync.Once` 构造身份运行时，`NewSessionValidator` 与 `Register` 共享实例（与 IAM 装配方式一致）。
- `auth.Principal` 增加 `SessionID`、`TokenVersion`、`TokenExpiresAt`；`principalFromClaims` 读取 `jti` / `ver`。
- `roles` 声明仅保留兼容读取；签发端不再写入。IAM `shadow` / `enforce` 模式以快照为唯一权限来源。
- 健康检查追加 `identity_db` / `identity_redis`；MS 模式（内存）不注册假检查。

### 13.3 与 IAM 的边界

| 关注点 | 身份域 | IAM |
|---|---|---|
| 账号是否存在、是否可登录 | 负责 | 不感知 |
| 角色 / 权限 / 数据范围 | 不负责 | 负责 |
| 令牌中的 `sub` | 提供 | 作为快照查询键 |
| 禁用 / 锁定 | 负责并撤销会话 | 不负责 |
| 审计事件 | 认证类事件 | 授权 / 角色类事件 |

---

## 十四、任务拆解

| 序号 | 任务 | 产出 | 变更路径 | 验收要点 | 阶段 |
|---|---|---|---|---|---|
| T1 | 身份域建模 | `Account`、`Credential`、`Session`、`MFAEnrollment`、`LoginAttempt` 与状态机 | `internal/identity/domain` | 状态迁移表驱动用例；禁用 / 锁定不可登录 | 1 |
| T2 | 持久化 | 六张表 + 内存 / MySQL 仓储 | `internal/identity/infrastructure/persistence`、`migrations/000001_*` | up/down 可执行；唯一键与索引符合 §9；API 不执行迁移 | 1 |
| T3 | 凭据与策略 | Argon2id、密码强度、失败锁定、登录限流 | `internal/identity/app`、`pkg/password`、`pkg/ratelimit` | 5 次失败锁定；统一文案；审计事件 | 1 |
| T4 | 令牌签发 | Access JWT + 一次性 Refresh；`kid` 密钥环 | `pkg/jwt`、`internal/security`、`internal/identity/app` | 三端隔离；`kid` 轮换；未知 `kid` 拒绝 | 2 |
| T5 | 撤销 | `token_version` + `jti` 黑名单 + `RevocationChecker` | `pkg/redis`、`internal/security`、`internal/platform` | 禁用 / 登出 ≤ 60s；Redis 故障 `50300` | 2 |
| T6 | 认证端点 | 三端 `login` / `refresh` / `logout`；A 端 `sessions` | `internal/identity/transport/*`、`cmd/*/register.go` | OpenAPI 三端契约；刷新重放触发全链撤销 | 2 / 4 |
| T7 | C 端登录 | 短信验证码 + OAuth 回调；防刷 | `internal/identity/infrastructure/cache`、`pkg/sms`、`transport/c` | 防枚举、防刷；验证码 5 分钟过期 | 4 |
| T8 | A 端 MFA / SSO | TOTP、`mfa_ticket`、绑定 / 解绑 / 恢复码；OIDC 预留 | `pkg/totp`、`internal/identity/app`、`transport/admin` | 未完成 MFA 不得获得管理令牌 | 3 |
| T9 | 审计接入 | `auth.*` 事件目录、字段、Outbox 事务 | `internal/shared/audit`、`docs/audit/EVENTS.md`、`internal/identity/app` | 登录 / 登出 / 失败 / 锁定 / MFA / 重放均有事件 | 5 |
| T10 | 与 IAM 边界 | 令牌不携带角色；Principal 增补字段 | `internal/shared/auth`、`internal/security`、`cmd` | IAM 快照为唯一权限来源；撤权即时 | 2 |

**依赖关系**：T1 → T2 → T3 → T4 → T5 → T6；T6 完成后 T8、T7 可并行；T9 与 T10 随 T4/T6 一起合入，T9 在阶段 5 收口。

---

## 十五、验收标准与用例

| AC | 要求 | 用例位置 | 验证命令 |
|---|---|---|---|
| `AC-ENT-01-1` | 账号禁用或登出后 ≤ 60s，旧访问令牌被拒绝 | `internal/identity/app/*_test.go`、`tests/integration/identity/revocation_test.go` | `go test ./internal/identity/... ./internal/security/...`；`make test-integration` |
| `AC-ENT-01-2` | 刷新令牌一次性使用；重放触发撤销并记审计 | `internal/identity/app/refresh_test.go`、`tests/integration/identity/refresh_reuse_test.go` | 同上 |
| `AC-ENT-01-3` | 三端 issuer / audience / 密钥隔离，跨端令牌互不通过 | `internal/security/jwt_keys_test.go`、`internal/identity/app/token_test.go` | `go test ./internal/security/...` |
| `AC-ENT-01-4` | 登录失败限流与锁定生效；A 端未完成 MFA 不得获得管理令牌 | `internal/identity/app/lockout_test.go`、`internal/identity/app/mfa_test.go` | 同上 |
| `AC-ENT-01-5` | 登录、登出、失败、锁定、MFA 绑定均产生审计事件 | `internal/identity/app/audit_test.go`、`docs/audit/EVENTS.md` | `bash scripts/ci/check-audit.sh` |
| `AC-ENT-01-6` | `admin_required=true` 下未绑定账号可凭 `mfa_setup_ticket` 完成绑定并直接获得令牌 | `internal/identity/app/mfa_test.go` | `go test ./internal/identity/...` |
| `AC-ENT-01-7` | 首个管理员由迁移 `000005` 创建角色 / 授权、由 A 端启动引导幂等开通账号（已存在时只校验授权），口令与 TOTP 密钥不入日志 / 命令行 | [`000005_seed_bootstrap_admin.up.sql`](../migrations/000005_seed_bootstrap_admin.up.sql) | `bash scripts/ci/check-migrations.sh` |

补充用例要求：

- 每个状态机迁移至少一个表驱动用例，包含非法迁移。
- 所有认证失败用例断言：状态码一致、文案一致、无令牌泄露。
- 集成用例必须使用真实 MySQL + Redis（`TEST_MYSQL_DSN` / `TEST_REDIS_ADDR`），未配置时显式跳过。
- 并发刷新用例：10 个并发请求仅 1 个成功，其余按重放处理并撤销会话链。

---

## 十六、门禁

新增 `scripts/ci/check-authn.sh`，接入 `Makefile` 的 `check-authn` 与 `ci` target：

| 检查 | 规则 |
|---|---|
| 生产配置 | `env=prod` 时禁止 `auth.mode=demo`；强制 `identity.enabled=true`、`revocation.driver=redis`、`revocation.failure_mode=fail_closed`、A 端 `mfa.admin_required=true`、`db.driver != memory`、`async.enabled=true` |
| 契约 | `api/openapi/{admin,biz,c}.yaml` 均存在 `login` / `refresh` / `logout` path |
| 撤销路径 | `internal/security` 存在 `RevocationChecker` 引用，且有测试文件引用 |
| 迁移 | `000001_create_identity_tables.{up,down}.sql` 成对存在，编号不重复 |
| 审计 | 新增 action 在 `KnownActions()` 与 `EVENTS.md` 同时存在 |
| 密钥 | 示例配置不含明文 `secret_key` / `client_secret` / `hash_key`；`pkg/crypto/hasher.go` 存在已知向量测试 |
| 安全响应头 | 三端认证路由统一挂 `pkg/httpx.NoStoreMiddleware`；`transport/*/router_test.go` 断言 login / mfa/setup / me 带 `no-store` |
| 时序对齐 | 登录的账号不存在 / 无凭据 / locked / disabled 分支均调用 `PasswordService.VerifyDummy`；`TestLoginDummyVerifyAlignment` 固定该行为 |

门禁必须验证「违规即失败」：在 PR 中提供临时违规样例的失败输出。

---

## 十七、风险与回滚

| 风险 | 影响 | 缓解 | 回滚 |
|---|---|---|---|
| 令牌语义切换导致全端 401 | 用户无法访问 | 先影子校验、按 channel 灰度；新旧密钥并存 | 切回 `auth.mode=demo`（仅非生产）或回退签发逻辑与密钥环 |
| Redis 不可用导致登录 / 鉴权不可用 | 全站不可用 | 连接池隔离、超时、`failure_mode` 可配置 | 安全审批后临时 `fail_open`；恢复后立即切回 |
| 刷新并发导致会话被误撤销 | 用户被登出 | 行锁 / 乐观锁 + 单请求成功语义 | 降低并发窗口、保留审计重放记录 |
| 短信 / OAuth 供应商不稳定 | C 端无法登录 | 超时 + 熔断 + 降级提示；双登录方式 | 关闭该登录方式开关 |
| TOTP 密钥泄露 | A 端账号被接管 | AES-GCM 加密、密钥托管、恢复码一次性 | 强制重置 MFA 并撤销全部会话 |
| PII 摘要密钥泄露 / 轮换失误 | 旧摘要可被枚举或双读中断 | `hash_key` 仅环境变量注入；轮换保留 `previous_hash_keys` 双读，待历史数据过期后再移除 | 回退到上一 key 并恢复 `previous_hash_keys` 配置；无需回滚数据 |
| 账号迁移遗漏 | 部分用户无法登录 | 导入脚本 + 核对报告 + 只增不删 | 回滚迁移并恢复备份 |
| 审计与业务写入不同事务 | 审计丢失 | 复用 UnitOfWork + `async_outbox`；失败关闭 | 修复事务边界后重放 Outbox |

回滚原则：所有新能力默认关闭；开关关闭后行为回到「只有验签」，不影响已有 IAM 与业务接口。

---

## 十八、实施阶段与状态

| 阶段 | 内容 | 对应任务 | 出口标准 |
|---|---|---|---|
| 0 契约 | 已完成（本文档、OpenAPI 骨架、配置字段与 `Validate`、迁移草案、审计 action 目录） | T1/T2/T9 前置 | 文档评审通过；`check-openapi` 通过 |
| 1 账号域 | domain + 仓储 + 密码策略 + 锁定 + 迁移 + 单测 | T1/T2/T3 | 状态机与仓储用例全绿 |
| 2 令牌闭环 | `pkg/jwt` kid / 密钥环、签发、登录 / 刷新 / 登出、撤销 checker、A/B 端点 | T4/T5/T6/T10 | `AC-ENT-01-1/2/3/5` 通过 |
| 3 MFA | TOTP、`mfa_ticket`、`mfa_setup_ticket` 强制绑定、`000005` 角色种子 + 启动引导、会话管理 | T8 | `AC-ENT-01-4/6/7` 通过 |
| 4 C 端 | 短信验证码、防刷、OAuth 回调、OIDC 预留 | T6（C）/T7 | C 端登录与限流用例通过 |
| 5 收口 | 审计回写、`check-authn`、全量门禁、真实依赖集成测试、状态回写 | T9/T10 | `make ci` + `make test-integration` 全绿 |

状态：`待做` / `进行中` / `已完成`。每个阶段独立 MR，遵循「一条 ENT 一个主 MR + 契约先合」的合入策略；阶段完成后在本文件与 [`production-readiness-implementation-plan.md`](./production-readiness-implementation-plan.md) 同步状态。

| 阶段 | 状态 |
|---|---|
| 0 契约 | 已完成 |
| 1 账号域 | 已完成 |
| 2 令牌闭环 | 已完成 |
| 3 MFA | 已完成 |
| 4 C 端 | 已完成 |
| 5 收口 | 已完成 |

---

## 十九、多租户边界与扩展模型（BIZ-09）

### 19.1 当前边界（明确不支持多租户 C 端）

- **A 端**：单平台运营方，`merchant_id` 恒为空串；账号、会话、MFA、审计均为平台级。
- **B 端**：以 `merchant_id`（规范化小写，见 BIZ-06）标识租户；登录、会话、审计事件写入该商户。
  仓储按 `(channel, merchant_id, subject_index)` 查找主体；B 端令牌的 `merchant_id` 只来自账号绑定，
  不接受请求参数覆盖。
- **C 端**：**全局单租户**。C 端账号（手机号 / OAuth 主体）不属于任何商户，
  `merchant_id` 恒为空串，会话与撤销状态为全平台共享；当前**不支持**「同一手机号在不同商户下
  拥有独立 C 端账号」。
- **唯一键**：`identity_account` 的唯一键为 `(channel, merchant_id, subject_index)`。
  B 端同一主体可在不同商户下各自注册（`subject_index` 已包含商户维度）；
  C 端 `merchant_id` 恒为空串，因此 C 端主体在平台内**全局唯一**，不能按商户重复注册。
  未来若需要多商户 C 端，必须给 C 端账号引入非空 `merchant_id` 并同步调整令牌与缓存键（见 19.2）。

### 19.2 未来扩展模型（多商户 C 端）

若产品需要「一个平台、多商户、C 端用户在商户内独立注册」，按以下顺序演进：

1. **契约**：C 端注册 / 登录 / `/c/v1/me` 增加 `merchant_id`（由 App / 小程序入口或域名固定，
   禁止用户输入切换）；令牌 claims 增加 `merchant_id`，跨商户访问直接拒绝。
2. **数据**：`identity_account` 对 C 端允许非空 `merchant_id`；唯一键改为
   `(channel, merchant_id, subject_index)`；`subject_index` 已包含商户维度（BIZ-06 规范化后）。
3. **仓储**：`FindBySubject`、`AccountListFilter`、会话 / 撤销键（`authn:sub` / `authn:sid`）
   全部带上 `merchant_id`，避免跨租户命中缓存。
4. **治理**：新增「主体跨商户迁移」工具（绑定 / 解绑 + 重新签发令牌），不直接改库；
   审计事件写入 `merchant_id`，保留期与导出按商户隔离。
5. **回滚**：单租户与多租户不兼容，必须按新 major 契约发布；旧令牌在切换窗口内只读，
   强制重新登录后失效。

### 19.3 明确不做

- 不做「一个 C 端账号关联多个商户」的共享身份模型（资产与订单归属无法在身份域裁决）。
- 不在请求头 / Cookie 中接受租户切换；租户必须由部署入口或令牌声明推导。
- 不允许管理员跨商户查询 C 端账号（A 端运维接口只按 `channel` 维度，不暴露商户 C 端数据）。

---

## 二十、错误码与本地化（BIZ-05）

### 20.1 契约：稳定错误码优先，文案不参与判定

- 所有错误响应统一信封 `{code, message, trace_id}`；**`code` 是稳定契约**，前端 / SDK
  必须按 `code` 做分支与本地化，不得解析 `message` 文本。
- 身份域保留的稳定码：`40000` 参数错误、`40100` 未认证 / 凭据错误、`40300` 无权限、
  `40900` 冲突（重复绑定 / 最后凭据 / 版本冲突）、`42900` 限流、`50000` 内部错误、
  `50300` 依赖不可用。
- `message` 当前为英文占位，仅用于日志与排障；不承诺稳定，不随 `Accept-Language` 变化。
  前端本地化以 `code` + 上下文（端点、字段）为准，避免服务端维护多语言文案表。

### 20.2 短信配额的时区

- `identity.sms.daily_per_phone`、`hourly_per_ip` 与 `daily_budget` 的日窗口按
  `identity.sms.quota_timezone` 计算（IANA 名称，默认 `UTC`）。
- 该配置影响 Redis 计数键的日期后缀；跨时区切换会开启新的日窗口，切换前应选择
  业务低峰，并在变更记录中说明。
- 小时窗口仍按 UTC 小时桶；如需按本地小时，需扩展限流键并在测试中覆盖。
