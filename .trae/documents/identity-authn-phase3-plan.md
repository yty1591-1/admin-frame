# ENT-01 阶段 3（A 端 MFA）实施计划

## 一、背景与目标

阶段 1（账号域）与阶段 2（令牌闭环）已完成并提交（commit `a7b8c34`）。当前 A/B 端密码登录在凭据校验通过后**直接签发令牌**，`MFAEnrollment` 聚合与内存 / MySQL 仓储虽已存在，但没有 TOTP 算法、绑定 / 校验 / 解绑用例、`mfa_ticket` 与 MFA 端点。

本阶段对应 `docs/identity-authn.md` §18 的「阶段 3 MFA」（任务 `T8`），出口标准为 `AC-ENT-01-4`：**A 端未完成 MFA 不得获得管理令牌**；同时补齐 TOTP 绑定 / 校验 / 解绑、票据一次性消费、审计事件、配置与 OpenAPI 契约。

约束沿用项目既有约定：最小改动、领域层不依赖框架、失败默认安全（`fail_closed` / `50300`）、身份域默认关闭、本阶段不 commit。

## 二、关键设计决策

1. **MFA 用例挂在 `AuthService` 上**，而不是新建独立服务：登录二阶段必须复用已有的 `createSession`、`LoginGuard`、审计与令牌签发逻辑，避免复制会话创建代码。
2. **`pkg/totp` 仅依赖标准库**：HMAC-SHA1、30 秒步长、6 位、允许 ±1 窗口；密钥 20 字节随机数，Base32 展示。
3. **密钥加密复用 `pkg/crypto` 的 AES-256-GCM**：`NewAES256GCM` + `Seal/Open`，AAD 绑定 `account_id`，避免密文跨账号挪用；数据库只存密文。
4. **`mfa_ticket` 使用现有 JWT signer 签发**：`aud=admin`、`scope=mfa`、`exp=5m`、独立 `jti`；**不写 `ver` / `sid` 声明**，因此即使被当作 Bearer 使用，也会被阶段 2 的撤销检查器（无 `ver`）拒绝。
5. **票据一次性消费**：新增应用层端口 `MFATicketStore.Consume(jti, ttl) (bool, error)`，Redis 实现用原始客户端的 `SETNX`；依赖故障按 `fail_closed` 返回 `50300`。内存实现仅用于单测 / 本地。
6. **绑定确认与两步登录共用 `POST /mfa/verify`**：该路由使用「条件鉴权」——`Authorization` 存在时先走 `AuthMW`（绑定确认），不存在时按 `mfa_ticket` 走两步登录；两条路径在 Handler 内互斥，票据优先。
7. **`identity.mfa.admin_required` 语义**：
   - `true` 且账号未绑定（无已确认绑定）→ 密码正确也返回 `40100`（稳定文案 `mfa binding required`），不签发令牌；
   - 账号已绑定 → 返回 `200 {"mfa_required": true, "mfa_ticket": "..."}`，仅 `mfa/verify` 可完成登录；
   - `false`（非生产默认）→ 已绑定账号仍走两步登录，未绑定账号按原逻辑直接登录。
8. **解绑需要有效动态码**；解绑成功后删除绑定、递增 `token_version` 并撤销全部会话，强制重新登录（fail-safe）。
9. **恢复码**：绑定确认时生成 10 个一次性恢复码，只返回一次，库内只存 SHA-256；校验成功即从集合中移除并回写。

## 三、文件级改动

### 1. 新增 `pkg/totp`

- `pkg/totp/doc.go`：包说明（标准库实现、窗口与算法约束）。
- `pkg/totp/totp.go`：
  - `GenerateSecret() ([]byte, error)`：20 字节 `crypto/rand`。
  - `EncodeSecret(secret []byte) string`：无填充 Base32。
  - `Validate(secret []byte, code string, now time.Time) bool`：HMAC-SHA1 / 30s / 6 位 / ±1 窗口，常量时间比较。
  - `ProvisioningURI(issuer, account, secretBase32 string) string`：`otpauth://totp/...`。
- `pkg/totp/totp_test.go`：RFC 6238 / HOTP 已知向量、±1 窗口边界、非法输入、URI 转义。

### 2. 令牌层 `internal/identity/app/token.go`

- 新增 `TokenParser` 接口（`Parse(string) (jwt.Claims, error)`）与 `WithTokenParser` 选项；`*jwt.Manager` 直接满足。
- 新增常量 `MFATicketTTL = 5 * time.Minute`。
- 新增 `IssueMFATicket(accountID string) (MFATicket, error)`：
  - `MFATicket{Ticket, JTI, ExpiresAt}`；
  - `Extra: {"scope":"mfa","channel":"admin"}`，不含 `ver` / `sid`。
- 新增 `ParseMFATicket(raw string) (jwt.Claims, error)`：
  - 校验 `sub`、`jti`、`scope=mfa`、`channel=admin`；
  - 失败统一 `40100`；未注入 parser 时返回内部错误（装配失败，不静默放行）。

### 3. 领域 / 应用层 MFA 用例

- `internal/identity/domain/mfa.go`：补充恢复码一次性消费与幂等小方法（如 `UseRecoveryCode(hash, now)`、`HasRecoveryCode(hash)`），保持领域层无基础设施依赖。
- 新增 `internal/identity/app/mfa.go`（方法挂在 `AuthService`）：
  - `AuthServiceConfig` 增加可选字段：`MFA domain.MFAEnrollmentRepository`、`MFACipher *crypto.AEAD`、`Tickets MFATicketStore`、`MFAIssuer string`、`MFAAdminRequired bool`。
  - `SetupMFA(ctx, actor)`：仅 `admin` 端；账号必须 active；已有**已确认**绑定返回 `40900`；生成密钥 + 恢复码，加密后保存未确认绑定，返回 `secret`、`otpauth_uri`、`recovery_codes`。
  - `VerifyMFA(ctx, input)`：
    - 票据模式：解析票据 → 加载账号并复查状态 → 限流（`channel + jti + IP`）→ 一次性消费 `jti` → 校验 TOTP / 恢复码 → `RecordSuccess` → `createSession` → 审计 `auth.login`；
    - 绑定模式：`actorFrom` 的账号 → 加载未确认绑定 → 校验动态码 → `Confirm` → 审计 `auth.mfa.bound`；
    - 失败：记录 `LoginGuard.RecordFailure(reason=mfa_failed)`、审计 `auth.mfa.failed`，返回统一 `40100`；票据重放同样计入失败。
    - 未配置 `MFACipher` / `Tickets` 时返回 `50300`（fail_closed），不跳过校验。
  - `DisableMFA(ctx, actor, code)`：仅 `admin`；校验 TOTP / 恢复码 → 删除绑定 → `BumpTokenVersion` + `RevokeByAccount` + 撤销缓存刷新 → 审计 `auth.mfa.disabled`。
  - 私有辅助：`loadConfirmedEnrollment`、`decryptSecret`、`verifyCodeOrRecovery`、`revokeAllSessions`。
- `internal/identity/app/auth_service.go`：
  - `LoginResult` 增加 `MFARequired bool`、`MFATicket string`。
  - `Login` 在 `RecordSuccess` 与 `createSession` 之前插入 A 端 MFA 判定：
    - 有已确认绑定 → 签发票据并返回 `MFARequired=true`（不创建会话、不记成功）；
    - `admin_required` 且无绑定 → 审计 `auth.login_failed`（`mfa_pending`）并返回 `40100`；
    - 其余保持原逻辑。
- 新增 `internal/identity/app/mfa_ticket.go`：`MFATicket`、`MFATicketStore` 端口、票据声明常量与错误映射。

### 4. 基础设施

- `internal/identity/infrastructure/cache/mfa_ticket_redis.go`：基于 `pkg/redis.Client.Redis().SetNX`，键 `authn:mfa_ticket:<jti>`，TTL 为票据剩余寿命，统一加 KeyPrefix。
- `internal/identity/infrastructure/cache/mfa_ticket_memory.go`：互斥 map + 过期回收，仅单测 / 本地。
- `internal/identity/infrastructure/persistence`：现有 `memory_mfa.go` / `mysql_mfa.go` 已满足端口，无需改表；仅补测试覆盖恢复码回写。

### 5. 配置 `internal/platform/config.go`

- 新增 `IdentityMFAConfig`：
  ```yaml
  identity:
    mfa:
      admin_required: false
      issuer: AdminFrame
      secret_key: ""
  ```
- `applyIdentityDefaults`：`Issuer` 空值填 `AdminFrame`；`AdminRequired` 保持 false。
- `validateIdentity` 增加：
  - 生产环境（`isProdLikeEnv`）强制 `mfa.admin_required=true`；
  - `mfa.admin_required=true` 时 `secret_key` 必填；
  - `secret_key` 非空时须能解码为 32 字节（接受标准 Base64 / RawURL Base64 / Hex）；
  - `issuer` 非空。
- `LogValue` 只输出 `admin_required`、`issuer` 与 `secret_key_configured`，禁止输出密钥原文。

### 6. 装配 `cmd/internal/identityruntime/runtime.go`

- `buildRepositories` 返回值增加 `domain.MFAEnrollmentRepository`（内存 / MySQL 二选一）。
- 解析 `identity.mfa.secret_key` 构造 `*crypto.AEAD`；为空且 `admin_required=false` 时传 nil。
- 构造 `cache.NewRedisMFATicketStore(deps.Redis)`（无 Redis 时返回明确装配错误，因为 `revocation.driver=redis` 已要求 Redis）。
- 将 MFA 依赖注入 `app.AuthServiceConfig`；`server` 启动时 `mfa.admin_required=true` 但密钥缺失必须直接失败，不允许带病启动。
- `/readyz` 复用现有 `identity_redis` 检查，无需新增探针。

### 7. 传输层

- `internal/identity/transport/rest/handler.go`：
  - `loginResponse` 扩展 `MFARequired` / `MFATicket`（`omitempty`），`Login` 根据 `LoginResult` 返回挑战或令牌。
  - 新增 `SetupMFA`、`VerifyMFA`、`DisableMFA` Handler 与 DTO：
    - `POST /admin/v1/auth/mfa/setup`：Bearer，响应 `{secret, otpauth_uri, recovery_codes}`；
    - `POST /admin/v1/auth/mfa/verify`：`{mfa_ticket?, code}`，登录成功返回令牌对，绑定确认返回 `204`；
    - `POST /admin/v1/auth/mfa/disable`：Bearer + `{code}`，返回 `204`。
  - 错误统一走 `httpx.FromError`；响应不包含密钥密文、恢复码哈希、票据原文以外的敏感值。
- `internal/identity/transport/admin/router.go`：
  - 注册 setup / disable（`authMW`）与 verify（条件鉴权：有 `Authorization` 走 `authMW`，否则放行到 Handler 内票据校验）。
  - `biz` 端不注册任何 MFA 路由。

### 8. 审计

- `internal/shared/audit/actions.go`：新增 `ActionAuthMFABound = "auth.mfa.bound"`、`ActionAuthMFAFailed = "auth.mfa.failed"`、`ActionAuthMFADisabled = "auth.mfa.disabled"`，并加入 `KnownActions()`。
- `docs/audit/EVENTS.md`：登记三个事件（MFA 绑定完成、MFA 校验失败计入锁定、MFA 解绑），字段只含 `actor_id` / `channel` / `reason` / `trace_id`。
- 若仓库已有 action 单测，同步扩充；无则在 `internal/shared/audit` 补表驱动用例固定 `KnownActions()`。

### 9. OpenAPI

- `api/openapi/admin.yaml`：
  - `login` 的 `200` 响应改为「令牌对或 MFA 挑战」二选一（`oneOf`）；
  - 新增 `/admin/v1/auth/mfa/setup`、`/mfa/verify`、`/mfa/disable` 三个 path；
  - 错误码覆盖 `40000` / `40100` / `40900` / `42900` / `50300`。
- `api/openapi/components.yaml`：新增 `MFAChallenge`、`MFASetupData`、`MFAVerifyRequest`、`MFADisableRequest` 等 schema，复用现有 `Envelope`。
- 保持 `info.version` 提 minor；`scripts/ci/check-openapi.sh` 必须通过。

### 10. 文档状态

- `docs/identity-authn.md`：§18 阶段 3 标记「已完成」，`AC-ENT-01-4` 标注覆盖用例；§11 补充 `auth.mfa.disabled`。
- `docs/production-readiness-implementation-plan.md`：`ENT-01` 状态更新为「阶段 1/2/3 已完成」。

## 四、测试计划

- `pkg/totp/totp_test.go`：表驱动覆盖 RFC 向量、窗口边界、错误码格式、URI。
- `internal/identity/app/mfa_test.go`（对应 `AC-ENT-01-4`）：
  - 未绑定 + `admin_required=true` 登录 → `40100`，无令牌、无会话；
  - 已绑定登录 → `mfa_required=true` 且无会话；票据校验成功 → 令牌 + 会话；
  - 错误动态码 / 错误恢复码 → `40100`、登录尝试失败、审计 `auth.mfa.failed`；
  - 同一票据二次使用 → `40100`（SETNX 消费失败）；
  - 恢复码成功一次、二次失败；
  - 绑定 setup → verify 确认 → `auth.mfa.bound`；重复 setup / 重复确认 → `40900`；
  - disable 需正确动态码，成功后绑定删除、`token_version` 递增、会话全部撤销；
  - `admin` 之外的端别调用 MFA 用例 → 拒绝；
  - 未配置加密密钥 / 票据存储 → `50300`。
- `internal/identity/app/token_test.go`：票据签发 / 解析、`scope` / `channel` / `exp` 校验、无 `ver` 声明。
- `internal/identity/infrastructure/cache`：Redis 票据存储可用 `miniredis` 或现有 testkit 模式；内存实现表驱动。
- `internal/platform/config_test.go`：MFA 默认值、生产强制、密钥格式校验。
- `internal/shared/audit`：`KnownActions()` 包含新增事件。

## 五、验证命令

```bash
gofmt -l cmd internal pkg
go build ./...
go vet ./...
go test ./pkg/totp/... ./pkg/jwt/... ./pkg/crypto/... \
  ./internal/identity/... ./internal/platform/... ./internal/shared/audit/...
bash scripts/ci/check-openapi.sh
bash scripts/ci/check-audit.sh
```

全部通过后仅保留工作区改动，不创建 commit。

## 六、不在本阶段范围

- OIDC / SSO 具体适配（仅文档预留，阶段 4 / 独立任务）。
- C 端短信 / OAuth 登录（阶段 4）。
- MFA 密钥轮换与密钥托管（ENT-07）；本阶段只支持单个 `secret_key`。
- `identity.session.max_per_account` 的最久未使用淘汰（阶段 2 会话端点已交付；淘汰策略需独立并发方案）。

## 七、风险与回滚

| 风险 | 处置 |
|---|---|
| Redis 不可用导致票据无法一次性消费 | `fail_closed` 返回 `50300`，不放行 |
| 票据被当作 Access Token | 票据无 `ver` / `sid`，撤销检查器直接拒绝；`ParseMFATicket` 强制 `scope=mfa` |
| 密钥配置缺失 | 启动时校验；运行期缺依赖返回 `50300`，不静默跳过 |
| 解绑被恶意会话利用 | 解绑必须提供有效动态码 / 恢复码，成功后强制撤销全部会话 |
| 回滚 | `identity.enabled=false` 即回到「只有验签」；MFA 路由与配置随身份域一起关闭 |
