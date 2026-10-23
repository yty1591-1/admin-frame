# 身份域阶段 2（HTTP 端点与令牌签发）实施计划

## Context

阶段 1 已完成身份域建模、内存 / MySQL 仓储、迁移 `000011` 与密码 / 锁定策略（未提交）。
本阶段按 [identity-authn.md](file:///d:/MyDocument/admin-frame/docs/identity-authn.md) §18 完成 T4 / T5 / T6 / T10：

- T4：Access JWT（`kid` 密钥环、三端 aud/channel 隔离）+ 一次性 Refresh（`ar_`/`br_`，库内只存 SHA-256）。
- T5：`token_version` + `jti` 黑名单 + `RevocationChecker`，Redis 故障 `fail_closed` 返回 50300。
- T6：A/B 端 `login` / `refresh` / `logout` / `logout-all`，A 端 `sessions` 列表 / 撤销。
- T10：令牌不携带 `roles`，`Principal` 增补 `SessionID` / `TokenID` / `TokenVersion` / `TokenExpiresAt`。

约束：身份域默认关闭（`identity.enabled=false`），最小改动、不改阶段 1 行为、不提交 commit；C 端登录（阶段 4）与 MFA（阶段 3）不在本阶段。

## 实施步骤

### 1. pkg/jwt：kid 密钥环（T4）

- [manager.go](file:///d:/MyDocument/admin-frame/pkg/jwt/manager.go)
  - `HMACConfig` 增 `KeyID string` 与 `Keys []HMACKey{KID, Secret}`；`RSAConfig` 增 `KeyID string` 与 `Keys []RSAKey{KID, PrivateKey, PublicKey}`。
  - `Manager` 增 `keyID` 与 `keyRing`；`Sign` 在 `Header["kid"]` 写入 `keyID`，并用对应私钥 / 密钥签发。
  - `Parse` 在密钥环模式下按 header `kid` 精确选键；缺失或未知 `kid` 直接失败，不遍历猜测；旧字段（单密钥 + 轮换密钥）行为保持不变。
- [errors.go](file:///d:/MyDocument/admin-frame/pkg/jwt/errors.go)：新增 `ErrUnknownKeyID`。
- 新增 `pkg/jwt/manager_kid_test.go`：签发 header 带 kid、按 kid 解析、未知 kid 拒绝、轮换旧公钥可校验。

### 2. internal/security：JWTConfig 扩展与撤销检查（T4/T5/T10）

- [jwt.go](file:///d:/MyDocument/admin-frame/internal/security/jwt.go)
  - `JWTConfig` 增 `ActiveKID string`、`Keys []JWTKeyConfig{KID, PublicKeyFile, PrivateKeyFile}`（旧 `Secret`/`Secrets`/`PublicKeyFile` 保留，标注兼容）。
  - `Validate`：密钥环非空时要求 `active_kid` 命中且 kid 唯一；密钥环与旧字段互斥；HS256 不支持密钥环。
  - `manager()` 按密钥环构造校验器；新增导出 `SigningManager(cfg)`：仅签发进程使用，要求 active key 具备私钥（RSA）或 secret（HS256）。
  - `NewJWTAuth` 改为 `NewJWTAuth(cfg, expected, opts ...JWTAuthOption)`，新增 `WithRevocationChecker(checker)`；签名 / 时间 / iss / aud / channel 通过后调用 checker，`apperr.ErrUnavailable` → 50300，其余 → 40100。
  - `principalFromClaims` 读取 `jti`、`sid`、`ver`、`exp` 写入 Principal。
- [principal.go](file:///d:/MyDocument/admin-frame/internal/shared/auth/principal.go)：`Principal` 增 `SessionID`、`TokenID`、`TokenVersion int64`、`TokenExpiresAt time.Time`。

### 3. 身份域令牌与撤销（T4/T5）

- [domain/account.go](file:///d:/MyDocument/admin-frame/internal/identity/domain/account.go)：新增 `BumpTokenVersion(now)`（logout-all 使用，不改变状态）。
- [domain/repository.go](file:///d:/MyDocument/admin-frame/internal/identity/domain/repository.go)：`SessionRepository` 增 `RevokeByAccount(ctx, accountID, reason, now) (int, error)`；在 [memory_session.go](file:///d:/MyDocument/admin-frame/internal/identity/infrastructure/persistence/memory_session.go) 与 [mysql_session.go](file:///d:/MyDocument/admin-frame/internal/identity/infrastructure/persistence/mysql_session.go) 实现。
- 新增 `internal/identity/app/token.go`：
  - `TokenService`（signer、channel、accessTTL、refreshTTL、ids、clock）；`Issue(account, sessionID)` 生成 Access JWT（`sub`/`iss`/`aud=[channel]`/`iat`/`exp`/`jti`/`ver`/`channel`/`sid`，B 端附 `merchant_id`，禁止 `roles`）与 Refresh（32 字节随机 + `ar_`/`br_` 前缀）。
  - `HashRefreshToken`（SHA-256 hex）；`GenerateRefreshToken`。
- 新增 `internal/identity/app/revocation.go`：`VersionStore` 端口 + `RevocationChecker`（实现 security 接口）：账号状态 → token_version（未命中回源 DB 并回填 `max(access_ttl+leeway, 1h)`）→ `jti` 黑名单；Redis 故障按 `failure_mode` 返回 `ErrUnavailable`。
- 新增 `internal/identity/infrastructure/cache/revocation_redis.go`（基于 `pkg/redis.Client.Cache()`，键 `authn:ver:<id>` / `authn:revoked:<jti>`）与 `revocation_memory.go`（单测 / 内存模式）。

### 4. 认证用例（T6）

新增 `internal/identity/app/auth_service.go`：

- `Login(ctx, LoginInput{Channel, Subject, Password, IPHash, UAHash})`：
  - 限流（`LoginGuard.CheckRateLimit`，键 = channel + 主体摘要 + IP 摘要）；
  - 账号不存在 / 密码错误统一 `40100`；失败写 `LoginAttempt` 并按阈值锁定；
  - 成功后 `NeedsRehash` 透明升级；`pending → active`；`RecordSuccess`；
  - 创建 Session（refresh 哈希、`family_id`、`expires_at`、`idle_expires_at`），再签发令牌。
- `Refresh(ctx, channel, refreshToken, ip, ua)`：`rotated` → 重放，撤销整 family + 审计 `auth.token.reused` + 401；`active` 乐观锁轮换，冲突按重放处理；刷新不改 `token_version`。
- `Logout(ctx, accountID, sessionID, jti, tokenExp)`：撤销当前会话 + `jti` 黑名单（TTL=剩余寿命），幂等。
- `LogoutAll(ctx, accountID)`：`BumpTokenVersion` + 撤销全部会话 + 写 `authn:ver`。
- `ListSessions` / `RevokeSession`：只返回 `session_id` / 时间 / 状态，不返回令牌；校验会话归属。
- 审计：通过可选 `audit.Emitter` 发出 `auth.login` / `auth.login_failed` / `auth.token.refresh` / `auth.token.reused` / `auth.logout` / `auth.token.revoked`；同事务收口留阶段 5（代码注释说明）。

### 5. HTTP 端点（T6）

- 新增 `internal/identity/transport/rest/{handler.go,dto.go,router.go}`：通道无关 Handler，`httpx` 统一信封；请求只接受 `subject` + `password` / `refresh_token`，不接受 channel/roles/merchant_id。
- 新增 `internal/identity/transport/admin/router.go` 与 `biz/router.go`：
  - `POST /{admin|biz}/v1/auth/{login,refresh,logout,logout-all}`；A 端额外 `GET /admin/v1/auth/sessions`、`DELETE /admin/v1/auth/sessions/{id}`、`POST /admin/v1/auth/sessions/revoke-all`。
  - 登录 / 刷新不挂全局 `AuthMW`，由 `LoginGuard` 限流；登出与会话接口挂 `AuthMW`。
- 设备摘要：`rest` 内对 `RemoteAddr` 与 `User-Agent` 做 SHA-256，禁止落原文。

### 6. 平台配置与装配（T5/T10）

- [config.go](file:///d:/MyDocument/admin-frame/internal/platform/config.go)
  - 新增 `IdentityConfig{Enabled, AccessTTL, RefreshTTL, Session{IdleTimeout}, Revocation{Driver,FailureMode}, Lockout{MaxFailures,Window,Duration}, LoginRateLimit{RPS,Burst}}`，默认关闭；默认 `15m / 168h / 72h / redis / fail_closed / 5,15m,15m / 1,5`。
  - `applyDefaults` / `Validate` / `LogValue`：启用时要求 `auth.mode=jwt`、`revocation.driver=redis`、Redis 已配置；`access_ttl ∈ [5m,1h]`、`refresh_ttl > access_ttl`；生产禁止 `fail_open`。
- [authn.go](file:///d:/MyDocument/admin-frame/internal/platform/authn.go)：`NewAuthMiddleware` 增加可变选项，转发 revocation checker。
- [bootstrap.go](file:///d:/MyDocument/admin-frame/internal/platform/bootstrap.go)：
  - `Options` 增 `NewSessionValidator func(*Deps) (SessionValidator, error)`；`SessionValidator` 接口 `Check(ctx, jwt.Claims) error`。
  - 调整顺序：先构造 `Deps`（`AuthMW` 暂空）→ `NewSessionValidator` → `NewAuthMiddleware(..., WithRevocationChecker(...))` → 赋值 `deps.AuthMW`。
- 新增 `cmd/internal/identityruntime/runtime.go`：`New(deps)` 在 `identity.enabled=false` 时返回 nil；否则构造内存 / MySQL 仓储（`deps.Database.SQL()`）、Redis 撤销存储、`security.SigningManager`、`app.TokenService`、`app.AuthService`、`rest.Handler`，并注册 `identity_db` / `identity_redis` 健康检查。
- [cmd/admin-api/main.go](file:///d:/MyDocument/admin-frame/cmd/admin-api/main.go) 与 [cmd/biz-api/main.go](file:///d:/MyDocument/admin-frame/cmd/biz-api/main.go)：用 `sync.Once` 构造身份运行时，`NewSessionValidator` 与 `Register` 共享；[register.go](file:///d:/MyDocument/admin-frame/cmd/admin-api/register.go) 追加认证路由（A 端含 sessions）。

### 7. 审计与 OpenAPI（T6/T9 前置）

- [actions.go](file:///d:/MyDocument/admin-frame/internal/shared/audit/actions.go)：新增 `auth.logout`、`auth.token.refresh`、`auth.token.reused`、`auth.token.revoked`、`auth.account.locked`，同步 `KnownActions()`、[event_test.go](file:///d:/MyDocument/admin-frame/internal/shared/audit/event_test.go) 基线与 `docs/audit/EVENTS.md`。
- [admin.yaml](file:///d:/MyDocument/admin-frame/api/openapi/admin.yaml) / [biz.yaml](file:///d:/MyDocument/admin-frame/api/openapi/biz.yaml)：新增 `auth` tag 与上述路径（错误码覆盖 40000/40100/42900/50300），A 端补 sessions；确保 `check-openapi` 通过。

### 8. 单测与验证

- `pkg/jwt/manager_kid_test.go`：kid 签发 / 解析 / 未知 kid / 轮换。
- `internal/security/jwt_keys_test.go`：跨端点（aud + channel）拒绝、撤销 checker 的 401/503 映射。
- `internal/identity/app/token_test.go`：claims（ver/channel/sid/无 roles）、refresh 前缀与哈希。
- `internal/identity/app/auth_service_test.go`：登录成功 / 统一失败文案 / 刷新重放撤 family / 登出后 jti 拒绝 / 并发刷新仅 1 成功 / logout-all 递增版本。
- 验证命令：`gofmt -l`、`go build ./...`、`go vet ./...`、`go test ./pkg/jwt/... ./internal/security/... ./internal/identity/... ./internal/platform/...`；不提交。

## 风险与取舍

- 令牌 `sid` 声明不在 §4.1 表格中，但 §5.3 撤销当前会话与 §13.2 `Principal.SessionID` 需要它，作为 Extra 声明加入。
- 审计与业务写入同事务属于阶段 5；本阶段先接 `audit.Emitter`，失败只记录不回滚（代码注明），避免在本阶段引入 UnitOfWork 大改。
- `identity.enabled` 生产强制开启属于 §10.2 目标态；为不破坏现有部署，本阶段仅在启用时做一致性校验，不强制 prod 必须启用。
- 密钥环与旧单密钥字段互斥，保证 HS256 兼容路径与现网配置不受影响。
