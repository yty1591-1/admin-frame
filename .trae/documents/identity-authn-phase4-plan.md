# ENT-01 阶段 4：C 端登录实施计划

> 依据：[`docs/identity-authn.md`](../docs/identity-authn.md) §8 / §10 / §11 / §12 / §18、[`ent-01-identity-authn-plan.md`](./ent-01-identity-authn-plan.md)
> 阶段目标：短信验证码登录 + 防刷、OAuth 授权码回调、OIDC 预留；出口标准「C 端登录与限流用例通过」
> 本轮已确认：OAuth 做完整回调链、手机号账号用摘要定位、短信接入 `pkg/sms`

---

## 一、Summary

在既有 A/B 端身份域之上补齐 C 端登录能力，复用 `AuthService` 的会话创建 / 审计 / 锁定逻辑，不新建第二套令牌链路：

1. **短信验证码**：`POST /c/v1/auth/sms/send` 发送 6 位验证码；`POST /c/v1/auth/login` 用手机号 + 验证码登录。
2. **防刷**：重发 60s、单手机号日限、单 IP 时限、验证码最多 5 次、响应恒定防枚举。
3. **OAuth**：`POST /c/v1/auth/oauth/{provider}/authorize` 下发 state + PKCE，`POST /c/v1/auth/oauth/{provider}/callback` 完成授权码换取与账号映射；`auto_provision=false` 时拒绝未映射身份。
4. **装配**：`identityruntime` 放开 C 端，`c-api` 接入身份运行时、撤销检查器与 C 路由。
5. **契约与文档**：OpenAPI c.yaml 增认证路径、示例配置增 `identity.sms` / `identity.oauth`、阶段状态回写。

不引入新第三方依赖：OAuth2 用标准库 `net/http` 实现授权码 + PKCE（S256）。

---

## 二、Current State Analysis（已核实）

| 维度 | 现状 | 证据 |
|---|---|---|
| C 路由 | 只有平台 probe，`registerRoutes` 空实现 | [`cmd/c-api/register.go`](../cmd/c-api/register.go) |
| C 主程序 | 未构造 identity runtime / `NewSessionValidator` | [`cmd/c-api/main.go`](../cmd/c-api/main.go) |
| 装配限制 | 仅允许 admin/biz | [`runtime.go`](../cmd/internal/identityruntime/runtime.go#L47-L50) |
| 配置限制 | `identity.enabled` 仅允许 admin/biz；MFA 生产规则未按端别收窄 | [`config.go`](../internal/platform/config.go#L1266-L1269)、`#L1313-L1326` |
| HTTP Handler | 通道无关，已有 Login/Refresh/Logout/MFA，无 C 用例 | [`handler.go`](../internal/identity/transport/rest/handler.go) |
| C 端定位账号 | `CredentialRepository` 只能按账号查，不能按哈希反查 | [`repository.go`](../internal/identity/domain/repository.go#L28-L37) |
| OAuth 映射表 | `identity_oauth_identity` 已建（UNIQUE(provider, provider_subject)），无仓储 | [`000011_create_identity_tables.up.sql`](../migrations/000011_create_identity_tables.up.sql) |
| 短信 | `pkg/sms` 提供 Sender / memory / aliyun，但平台未装配 | [`pkg/sms`](../pkg/sms/sender.go) |
| Redis | `Cache` 有 Get/Set/Delete/SetNX，**无 Incr** | [`cache.go`](../pkg/redis/cache.go) |
| OpenAPI c.yaml | 仅 probe，自包含 components | [`c.yaml`](../api/openapi/c.yaml) |
| 示例配置 | `configs/c.yaml.example` 无 identity 块 | [`c.yaml.example`](../configs/c.yaml.example) |

关键复用点：

- `AuthService` 已具备：限流键 `loginRateKey(channel, accountRef, ipHash)`、`guard.Authorize/RecordSuccess`、`createSession`、`emitAudit`。
- `TokenService` 已支持 `ChannelC` 与 `cr_` 刷新前缀。
- `rest.Handler` 的 Refresh / Logout 可直接给 C 端复用。
- `pkg/sms.Message` 要求 E.164 手机号 + `TemplateID` 或 `Content`，memory 驱动默认拒绝裸内容。
- `identityruntime` 已在装配期注册身份库 / Redis 就绪检查。

---

## 三、Assumptions & Decisions

| # | 决策 | 说明 |
|---|---|---|
| D1 | 手机号账号 `subject = "phone:" + sha256(phone)`（hex） | 库内无明文手机号；复用 `accounts.FindBySubject`；不新增迁移与仓储反查方法 |
| D2 | OAuth 自动建号 `subject = "oauth:<provider>:" + sha256(provider_subject)` | 避免第三方主体过长/含 PII；映射查询仍走 `(provider, provider_subject)` |
| D3 | 短信未注册手机号**不自动建号**，统一 `40100` | 文档无 `sms.auto_provision` 配置，保持失败关闭；不新增未请求开关 |
| D4 | OAuth 未映射身份且 `auto_provision=false` → `40100` | 与文档 §8.2 一致 |
| D5 | OAuth 增加 `POST .../authorize` 发起端点 | state/PKCE 必须服务端生成并一次性消费；同步补文档 §12 |
| D6 | OAuth 实现用标准库 `net/http`，不引入 `golang.org/x/oauth2` | 符合总体计划「不引入新第三方依赖」 |
| D7 | 短信驱动 `memory|aliyun`，默认 `memory`；生产禁止 `memory` | 复用 `pkg/sms`；aliyun 凭证只读环境变量 `ALIYUN_SMS_ACCESS_KEY_ID` / `ALIYUN_SMS_ACCESS_KEY_SECRET` |
| D8 | 所有验证码 / state / 限流 Redis 错误一律失败关闭（`50300`） | 与身份域 fail_closed 一致 |
| D9 | 不新增审计 action | 登录成功 / 失败复用 `auth.login` / `auth.login_failed`；短信发送无 actor，不落审计 |
| D10 | C 端不挂 `AuthMW` 的端点：login / refresh / sms/send / oauth authorize / oauth callback；仅 logout 挂 Bearer | 同 A/B 路由约定 |
| D11 | MFA 生产校验按端别收窄：仅 `channel != c` 时要求 `mfa.admin_required=true` | 保持 A/B 现有行为，避免 C 端被强制 TOTP |
| D12 | 阶段完成后不自动创建 commit（除用户明确指示） | 沿用项目约定 |

---

## 四、Proposed Changes

### 4.1 配置与校验（`internal/platform/config.go`、`configs/c.yaml.example`）

**新增结构**

```go
type IdentitySMSConfig struct {
    Enabled        bool          `yaml:"enabled"`
    Driver         string        `yaml:"driver"`          // memory|aliyun
    TemplateID     string        `yaml:"template_id"`
    SignName       string        `yaml:"sign_name"`
    Locale         string        `yaml:"locale"`
    CodeTTL        time.Duration `yaml:"code_ttl"`
    MaxAttempts    int           `yaml:"max_attempts"`
    ResendInterval time.Duration `yaml:"resend_interval"`
    DailyPerPhone  int           `yaml:"daily_per_phone"`
    HourlyPerIP    int           `yaml:"hourly_per_ip"`
}

type IdentityOAuthConfig struct {
    AutoProvision bool                          `yaml:"auto_provision"`
    Providers     []IdentityOAuthProviderConfig `yaml:"providers"`
}

type IdentityOAuthProviderConfig struct {
    Name            string   `yaml:"name"`
    ClientID        string   `yaml:"client_id"`
    ClientSecretEnv string   `yaml:"client_secret_env"`
    AuthURL         string   `yaml:"auth_url"`
    TokenURL        string   `yaml:"token_url"`
    UserInfoURL     string   `yaml:"userinfo_url"`
    RedirectURI     string   `yaml:"redirect_uri"`
    Scopes          []string `yaml:"scopes"`
    SubjectField    string   `yaml:"subject_field"` // 默认 sub
}
```

- `IdentityConfig` 增加 `SMS`、`OAuth` 两个字段。
- `applyIdentityDefaults`：`sms.driver` 默认 `memory`；`code_ttl=5m`、`max_attempts=5`、`resend_interval=60s`、`daily_per_phone=10`、`hourly_per_ip=30`；`provider.subject_field` 默认 `sub`。
- `validateIdentity`：
  - 端别改为允许 `admin|biz|c`，仍拒绝 `worker`。
  - `sms.enabled` / `len(oauth.providers)>0` 仅允许 `channel=c`。
  - `channel=c` 且 `env=prod|staging` 时，必须 `sms.enabled` 或至少一个 provider（对齐 §10.2「C 端无可用登录方式」）。
  - `sms.enabled=true`：`template_id` 非空；`driver ∈ {memory, aliyun}`；生产禁止 `memory`；`code_ttl ∈ (0,15m]`；`max_attempts ∈ [1,10]`；`resend_interval ∈ (0, code_ttl]`；`daily_per_phone ≥ 1`；`hourly_per_ip ≥ 1`。
  - provider：`name` 唯一且匹配 `^[a-z][a-z0-9_-]{1,31}$`；`client_id`/`client_secret_env`/`auth_url`/`token_url`/`userinfo_url`/`redirect_uri` 非空；三个 URL 必须为绝对 URL，生产要求 `https`。
  - MFA 规则加端别条件（D11）。
- `LogValue`：`identity` 组内补 `sms`（enabled/driver/ttl 等）与 `oauth`（auto_provision、provider 名称列表、`client_secret_configured` 布尔），不输出任何 secret。
- `configs/c.yaml.example`：新增注释态 `identity` 块（含 sms/oauth 字段示例），`auth.mode` 给出 `jwt` 注释示例。

### 4.2 Redis 原子计数（`pkg/redis/cache.go`）

新增：

```go
// Incr 原子自增并保证首次写入设置 TTL；ttl 必须 >0。
func (c *Cache) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)
```

实现：`INCR` + 首次（值为 1）时 `EXPIRE`，用 pipeline 降低 RTT；参数校验与 `Set` 保持一致。
单测：`pkg/redis/cache_test.go`（无则新建 `cache_incr_test.go`），覆盖首次建 TTL、递增、ttl<=0 40000/错误、空 key、关闭客户端。

### 4.3 领域与仓储（OAuth 身份映射）

- 新增 `internal/identity/domain/oauth_identity.go`：
  ```go
  type OAuthIdentity struct {
      ID              string
      AccountID       string
      Provider        string
      ProviderSubject string
      CreatedAt       time.Time
      UpdatedAt       time.Time
  }
  func NewOAuthIdentity(p NewOAuthIdentityParams) (*OAuthIdentity, error) // 校验非空、长度上限
  ```
- `domain/repository.go` 增加：
  ```go
  type OAuthIdentityRepository interface {
      FindByProviderSubject(ctx context.Context, provider, subject string) (*OAuthIdentity, error)
      Save(ctx context.Context, identity *OAuthIdentity) error
  }
  ```
- 新增 `internal/identity/infrastructure/persistence/oauth_identity.go`：memory（mutex + 索引 `provider\x00subject`）与 SQL 实现（读写 `identity_oauth_identity`，唯一键冲突 → `apperr.Conflict`）。
- 单测：`oauth_identity_test.go` 覆盖创建、重复映射冲突、不存在返回 `ErrNotFound`、副本隔离。

### 4.4 应用层：已验证登录收口（`internal/identity/app/auth_service.go`）

- 抽取私有 `completeLogin(ctx, account, now, traceID)`：`guard.Authorize` → pending 激活 → `guard.RecordSuccess` → `createSession` → `emitAudit(auth.login)`；`Login` 在凭据校验通过后复用它（行为不变）。
- 新增导出：
  ```go
  type VerifiedLoginInput struct{ AccountID, IPHash, UAHash, TraceID string }
  // CompleteVerifiedLogin 在短信 / OAuth 外部验证通过后完成登录。
  func (s *AuthService) CompleteVerifiedLogin(ctx context.Context, in VerifiedLoginInput) (LoginResult, error)
  ```
  流程：`FindByID` → `Authorize`（锁定/停用写 `auth.login_failed` 并 `RecordFailure`）→ `completeLogin`；C 端不触发 MFA。
- 单测补充到 `auth_service_test.go`：pending 激活、locked/disabled 拒绝、审计 action、会话与令牌字段。

### 4.5 应用层：短信验证码（`internal/identity/app/sms_login.go`）

**端口**

```go
type SMSCodeStore interface {
    Save(ctx context.Context, phoneHash, codeHash string, ttl time.Duration) error
    Get(ctx context.Context, phoneHash string) (string, error) // apperr.NotFound
    Delete(ctx context.Context, phoneHash string) error
    IncrAttempts(ctx context.Context, phoneHash string, ttl time.Duration) (int, error)
    ResetAttempts(ctx context.Context, phoneHash string) error
}

type SMSRateLimiter interface {
    AllowResend(ctx context.Context, phoneHash string, interval time.Duration) (bool, time.Duration, error)
    AllowDaily(ctx context.Context, phoneHash string, limit int) (bool, error)
    AllowHourlyIP(ctx context.Context, ipHash string, limit int) (bool, error)
}

type CodeSender interface {
    SendCode(ctx context.Context, phone, code string) error
}
```

**服务**

```go
type SMSLoginConfig struct {
    Accounts domain.AccountRepository
    Guard    *LoginGuard
    Codes    SMSCodeStore
    Limiter  SMSRateLimiter
    Sender   CodeSender
    Auth     *AuthService
    Clock    Clock
    CodeTTL, ResendInterval time.Duration
    MaxAttempts, DailyPerPhone, HourlyPerIP int
}
func NewSMSLoginService(cfg SMSLoginConfig) (*SMSLoginService, error)
func (s *SMSLoginService) Send(ctx context.Context, in SendSMSCodeInput) error
func (s *SMSLoginService) Login(ctx context.Context, in SMSLoginInput) (LoginResult, error)
```

**Send 规则**

1. 手机号必须 E.164（`^\+[1-9]\d{2,14}$`），非法 → `apperr.BadRequest`（40000）；不落明文。
2. `phoneHash = HashAccountRef(ChannelC, phone)`（复用现有 sha256 helper）。
3. 依次检查：重发 SetNX（命中 → `42900` 并带 RetryAfter）→ 日限 → IP 时限（命中 → `42900`）。
4. `crypto/rand` 生成 6 位数字，零填充；失败 → `50300`。
5. `codeHash = sha256("sms:" + phoneHash + ":" + code)`，`Save` TTL=code_ttl，并 `ResetAttempts`。
6. `Sender.SendCode(ctx, phone, code)`；发送失败 → `50300`（删除已写验证码，避免“发了但没到”仍可用）。
7. 响应恒定：不查账号，成功返回空（204）；任何账号存在性差异不外泄。

**Login 规则**

1. 校验手机号与 6 位验证码格式（`^\d{6}$`），非法 → `40000`。
2. `guard.CheckRateLimit(loginRateKey(ChannelC, phoneHash, ipHash))` → 超限 `42900`。
3. 读取 code hash：不存在 → `40100`（统一文案）。
4. 比对使用 `subtle.ConstantTimeCompare`；不匹配 → `IncrAttempts`，达到 `max_attempts` 时删除验证码；写 `auth.login_failed`（reason `bad_credential`）→ `40100`。
5. 匹配则删除验证码与尝试计数，`FindBySubject(ChannelC, "phone:"+phoneHash)`：
   - 不存在 → `auth.login_failed` + `40100`（D3，恒定响应）；
   - 存在 → `Auth.CompleteVerifiedLogin`。
6. 任何分支都不在错误文案、日志、审计中输出手机号或验证码。

### 4.6 应用层：OAuth（`internal/identity/app/oauth.go`）

**端口**

```go
type OAuthIdentityInfo struct{ Subject string }

type OAuthProvider interface {
    Name() string
    AuthorizationURL(state, codeVerifier string) (string, error)
    Exchange(ctx context.Context, code, codeVerifier string) (OAuthIdentityInfo, error)
}

type OAuthState struct {
    Provider string
    Verifier string
}
type OAuthStateStore interface {
    Save(ctx context.Context, state string, value OAuthState, ttl time.Duration) error
    // Consume 一次性消费；不存在/已消费返回 apperr.NotFound。
    Consume(ctx context.Context, state string) (OAuthState, error)
}
```

**服务**

```go
type OAuthServiceConfig struct {
    Providers     map[string]OAuthProvider
    States        OAuthStateStore
    Identities    domain.OAuthIdentityRepository
    Accounts      domain.AccountRepository
    IDs           IDGenerator
    Auth          *AuthService
    AutoProvision bool
    StateTTL      time.Duration
    Clock         Clock
}
func (s *OAuthService) Authorize(ctx context.Context, provider string) (AuthorizeResult, error)
func (s *OAuthService) Callback(ctx context.Context, in OAuthCallbackInput) (LoginResult, error)
```

**Authorize**：未知 provider → `apperr.NotFound`；生成 32 字节 state / verifier（base64url），`States.Save`（TTL 10m），返回 `authorize_url` + `state`。

**Callback**：
1. `States.Consume(state)`；provider 不匹配 / 不存在 → `40100`。
2. `Provider.Exchange(code, verifier)`；`invalid_grant` / 4xx → `40100`，网络 / 5xx → `50300`。
3. `Identities.FindByProviderSubject(provider, subject)`：
   - 命中 → `Accounts.FindByID` → `Auth.CompleteVerifiedLogin`；账号不存在 → `40100`。
   - 未命中且 `AutoProvision=false` → `40100`。
   - 未命中且开启：`NewAccount{ID: ids.NextString(), Channel: c, Subject: "oauth:"+provider+":"+sha256(subject)}` → `Accounts.Save`（expected 0）→ 写映射；映射写失败返回 `50300`（残留 pending 账号可被后续登录复用或人工清理）。
4. 审计仍由 `CompleteVerifiedLogin` 统一写 `auth.login`。

### 4.7 基础设施实现（`internal/identity/infrastructure/`）

| 文件 | 内容 |
|---|---|
| `cache/sms_code.go` | `MemorySMSCodeStore`（mutex + 注入 clock + TTL 过期）与 `RedisSMSCodeStore`（`redis.Cache`；键 `sms:code:<hash>` / `sms:attempt:<hash>`，`Incr` 记录尝试） |
| `cache/sms_limiter.go` | `MemorySMSRateLimiter`（map + 窗口起点）与 `RedisSMSRateLimiter`（重发 `SetNX`、日限 `Incr("sms:daily:<yyyy-mm-dd>:<hash>", 25h)`、IP `Incr("sms:ip:<yyyy-mm-dd-hh>:<hash>", 65m)`） |
| `cache/oauth_state.go` | `MemoryOAuthStateStore` 与 `RedisOAuthStateStore`（值 JSON：provider/verifier；`SetNX("oauth:state:used:<state>")` 抢占成功后 `Get`+`Delete` 原始 state，保证一次性） |
| `sms/code_sender.go` | `NewCodeSender(pkg/sms.Sender, templateID, signName, locale)`，构造 `sms.Message{Phone, SignName, TemplateID, Params{"code": code}, Reference:"auth_code"}` |
| `oauth/oauth2.go` | `NewOAuth2Provider(cfg)`：授权 URL 拼 `response_type=code/client_id/redirect_uri/scope/state/code_challenge/code_challenge_method=S256`；token 端点 `application/x-www-form-urlencoded` + `code_verifier`；userinfo `GET` + Bearer；按 `subject_field` 取字符串/数字主体 |

约束：

- 所有实现并发安全；不记录手机号、验证码、access_token、client_secret。
- Redis 不可用 / 序列化失败一律返回可映射为 `50300` 的内部错误。

### 4.8 HTTP 层（`internal/identity/transport/c/`）

新增 `handler.go`：

```go
type HandlerConfig struct {
    Rest      *rest.Handler       // 复用 Refresh / Logout
    SMS       *app.SMSLoginService
    OAuth     *app.OAuthService
    Providers []string            // 已配置 provider，用于路由裁剪
}
type Handler struct{ ... }

func (h *Handler) SendSMSCode(w, r)     // POST /c/v1/auth/sms/send {phone} -> 204
func (h *Handler) Login(w, r)           // POST /c/v1/auth/login {phone, code} -> TokenPair
func (h *Handler) OAuthAuthorize(w, r)  // POST /c/v1/auth/oauth/{provider}/authorize -> {authorize_url, state}
func (h *Handler) OAuthCallback(w, r)   // POST /c/v1/auth/oauth/{provider}/callback {code, state} -> TokenPair
```

- 复用 `httpx.OK/FromError/TraceIDFromContext`；请求体 `DisallowUnknownFields`。
- 客户端 IP / UA 摘要复用 `rest.HashClientRef`（若未导出则改为 `app.HashClientRef`）。
- 令牌响应字段与 A/B 完全一致（`access_token/refresh_token/token_type/expires_in`）。
- 错误码：手机号格式 `40000`；验证码/state/账号失败 `40100`；超限 `42900`；依赖不可用 `50300`。

新增 `router.go`：

```go
func Register(mux *http.ServeMux, h *Handler, authMW func(http.Handler) http.Handler) {
    mux.HandleFunc("POST /c/v1/auth/sms/send", h.SendSMSCode)
    mux.HandleFunc("POST /c/v1/auth/login", h.Login)
    mux.HandleFunc("POST /c/v1/auth/refresh", h.Rest.Refresh)
    mux.Handle("POST /c/v1/auth/logout", authMW(http.HandlerFunc(h.Rest.Logout)))
    for _, p := range h.Providers {
        mux.HandleFunc("POST /c/v1/auth/oauth/"+p+"/authorize", h.OAuthAuthorize)
        mux.HandleFunc("POST /c/v1/auth/oauth/"+p+"/callback", h.OAuthCallback)
    }
}
```

- 不注册 `logout-all`、会话列表（文档仅 A 端）。
- OAuth 路由仅对已配置 provider 注册（未配置 provider 返回 404，符合 §8.2）。

### 4.9 装配（`cmd/internal/identityruntime`、`cmd/c-api`）

- `runtime.go`：
  - 端别判断改为允许 `admin|biz|c`。
  - `Runtime` 增加 `CHandler *identityc.Handler`。
  - `buildRepositories` 增加 OAuth 身份仓储返回值。
  - MFA 相关装配仅对 `admin|biz` 执行；`c` 端 `AuthServiceConfig` 的 MFA 字段留空、`MFAAdminRequired=false`。
  - 新增 `runtime_c.go`：`buildChannelC(deps, cfg, channel, accounts, guard, authSvc)` 构造短信栈（`sms.Open` + code store + limiter + sender + `SMSLoginService`）、OAuth 栈（providers + state store + identities + `OAuthService`）与 `identityc.NewHandler`；`identity.sms.enabled=false` 时不构造短信服务（路由不注册 send/login？——login 端点仍注册，验证码校验失败即 40100；handler 需允许 SMS 为 nil 时返回 50300？）。

  **决策细化**：`POST /c/v1/auth/login` 始终注册；短信未启用时 handler 返回 `50300`（避免路由漂移暴露配置），`sms/send` 仅在启用时注册。OAuth 路由按 provider 裁剪。
- `cmd/c-api/main.go`：复制 biz 端 `sync.Once` 模式，注入 `NewSessionValidator`（返回 `Checker`）与 `Register`（复用同一 runtime 实例）；blank import `_ "github.com/admin-frame/admin-frame/pkg/sms/aliyun"`。
- `cmd/c-api/register.go`：`identityc.Register(mux, identityRT.CHandler, deps.AuthMW)`，observe `identityRT == nil || CHandler == nil` 时跳过。

### 4.10 契约与文档

- `api/openapi/components.yaml`：新增 `SMSSendRequest{phone}`、`SMSLoginRequest{phone, code}`、`OAuthAuthorizeResult{authorize_url, state}`。
- `api/openapi/c.yaml`：
  - `info.version` 3.0.0 → 3.1.0（新增路径，向后兼容）。
  - 新增 `auth` tag 与 6 条路径（login / sms/send / oauth authorize / oauth callback / refresh / logout），复用 `./components.yaml` 的 `Envelope`、`TokenPair`、`RefreshRequest` 与标准错误响应。
  - `bearerAuth` 描述改为 C 端独立 JWT（去掉演示 Token 文案）。
- `docs/identity-authn.md`：
  - §8 补手机号摘要 subject、`authorize` 端点、短信驱动与限流键。
  - §10 配置示例补 `sms.driver/template_id/sign_name/locale` 与 `oauth.providers[].userinfo_url/redirect_uri/subject_field`。
  - §12 端点表增加 `POST /c/v1/auth/oauth/{provider}/authorize`。
  - §18 阶段 4 状态改为「已完成」。
- `docs/production-readiness-implementation-plan.md`：ENT-01 阶段 4 状态同步为已完成。
- 不新增审计 action，故 `docs/audit/EVENTS.md` 与 `internal/shared/audit/actions.go` 不改。

### 4.11 测试清单（表驱动，与现有风格一致）

| 包 | 用例 |
|---|---|
| `pkg/redis` | `Incr` 首次 TTL、递增、非法参数、关闭客户端 |
| `internal/identity/app` | `CompleteVerifiedLogin`（pending 激活 / locked / disabled / 审计）；短信发送（E.164、重发 60s、日限、IP 限、驱动失败清理）；短信登录（成功、错误码、5 次超限作废、账号不存在、验证码过期、常量时间比对）；OAuth（authorize 参数、state 重放 / provider 不匹配、exchange 失败、映射命中、auto_provision 开/关、禁用账号） |
| `internal/identity/infrastructure/cache` | memory 与 miniredis 双实现：code TTL / attempts / 重发 / 日限 / IP 限 / state 一次性消费 |
| `internal/identity/infrastructure/oauth` | `httptest` 模拟 token + userinfo：PKCE challenge 正确、subject_field 解析、4xx → 40100、5xx → 50300 |
| `internal/identity/infrastructure/sms` | 构造 Message 字段正确、手机号/模板校验、Sender 错误透传 |
| `internal/identity/infrastructure/persistence` | OAuth 映射 memory / SQL 行为一致（SQL 用现有 sqlmock 或内存驱动测试风格） |
| `internal/identity/transport/c` | 路由注册（sms 开关、provider 裁剪）、请求校验、204 响应 |
| `internal/platform` | C 端配置校验（生产必须有短信或 OAuth、生产禁 memory 驱动、provider 唯一/URL 必填、LogValue 不含 secret） |

---

## 五、Verification

按顺序执行：

```bash
gofmt -l cmd internal pkg                      # 必须无输出
go build ./...
go vet ./...
go test ./pkg/redis/... ./pkg/sms/... ./internal/identity/... ./internal/platform/... ./internal/shared/audit/...
bash scripts/ci/check-openapi.sh
bash scripts/ci/check-audit.sh
```

- 集成测试包串行执行（项目经验：并行会互相干扰）。
- `check-openapi` 预期仍只有 login 200 `oneOf` 警告，无 ERR。
- 不新增迁移文件；`identity_oauth_identity` 复用 `000011`。
- 临时文件（`tmp_*`）不得留库，确认 `.gitignore` 已覆盖。

阶段完成后输出：改动清单、门禁结果、已知限制（C 端 MFA 不纳入、短信未注册手机号不自动建号、OAuth provider 需真实 IdP 配置），不自动 commit。
