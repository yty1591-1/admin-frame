# SAML 2.0 SP 接入实施计划

## Context

`docs/iam-p1-5-7-engineering-plan.md` §3.3 的 S6-3 尚未启动，代码库中没有任何 SAML/SCIM/M2M 实现。企业客户接入的判定仍为「不通过」，其中 SAML 2.0 SP-initiated SSO 是唯一尚未交付的登录协议。OIDC（授权码 + PKCE）已在 staging 用真实 Keycloak 验收，本计划按同一分层与安全基线补齐 SAML。

已确认的决策：

- 使用成熟库 `github.com/crewjam/saml` 做 XML 签名校验与断言解析，不自研 XML DSig。
- 仅支持 **SP-initiated + HTTP-POST binding**；`AllowIDPInitiated=false`。
- 支持**签名断言 + 加密断言**；SP 持有解密私钥，支持 active/next 双密钥。
- ACS 成功后用**一次性 ticket + 302 跳前端**，前端调 exchange 换令牌，避免浏览器看到裸令牌。
- 复用 OAuth 的账号映射、JIT 开通、审计与限流模式；未配置 provider 时不注册路由；默认关闭、显式开关。

## 架构与文件

### app 层（端口化，不出现 crewjam 类型）

- 新增 `internal/identity/app/saml.go`
  - `SAMLProvider` 端口：`Name() string`、`MetadataXML() ([]byte, error)`、`AuthorizationURL(requestID, relayState string) (string, error)`、`ParseResponse(ctx, samlResponse, expectedRequestID string) (SAMLIdentity, error)`。
  - `SAMLIdentity{Subject, AssertionID, Issuer string}`（仅保留映射所需字段）。
  - `SAMLServiceConfig`：Providers、States（复用 `OAuthStateStore`）、Identities、Accounts、IDs、Auth、Hasher、AutoProvision、StateTTL、Replay（`SAMLReplayStore`）、Tickets（`SAMLTicketStore`）、Metrics（`SAMLMetrics`，可为 nil）、Limiter、Breaker 参数。
  - `Authorize(ctx, SAMLAuthorizeInput) (AuthorizeResult, error)`：生成 requestID（`_` + base64url，满足 xs:ID）与 state，`Save` 到 state store（`OAuthState{Provider, RequestID}`），返回 IdP URL。
  - `ACS(ctx, SAMLACSInput) (ticket string, err error)`：Consume state → 校验 provider/长度 → `ParseResponse` → 显式要求断言签名 → 断言 ID 去重 → 生成一次性 ticket。
  - `Exchange(ctx, ticket, ipHash, uaHash, device, traceID) (LoginResult, error)`：Consume ticket → `AuthService.CompleteVerifiedLogin`。
  - `SPMetadata() ([]byte, error)`：所有 IdP 共用同一 SP entity/ACS/证书。
- `internal/identity/app/external_identity.go`：把 `OAuthService.resolveAccount` 抽成共享函数 `resolveExternalAccount`，OAuth 与 SAML 各自调用；行为不变，先跑 OAuth 回归测试。
- `OAuthState` 增 `RequestID string`（JSON 兼容，历史 state 解码不受影响），SAML 复用现有 memory/Redis 一次性 state 存储，不新增存储实现。

### infrastructure 层

- `internal/identity/infrastructure/saml/keys.go`：PEM → `*x509.Certificate` / `*rsa.PrivateKey`；配对校验、RSA ≥ 2048、证书过期检查、active/next 选择；解密失败时用 previous 私钥重试（仅在报文含 `EncryptedAssertion` 时）。
- `internal/identity/infrastructure/saml/metadata.go`：`metadata_url` 拉取（超时、缓存、最小刷新间隔、失败保留旧元数据）与 `metadata_xml` 内联解析；校验 IdP entityID 与配置一致。
- `internal/identity/infrastructure/saml/provider.go`：`app.SAMLProvider` 实现，封装 `saml.ServiceProvider` 的 `MakeAuthnRequest`、`GetAuthnRequestURL`、`ParseResponse`、`Metadata`。实现首步用 `go doc github.com/crewjam/saml` 核对 API 后落代码。
- `internal/identity/infrastructure/cache/saml_replay.go`：`SAMLReplayStore`。Redis 用 `SETNX` + TTL（key=`saml:replay:<provider>:<sha256(assertion_id)>`）；memory 用互斥 map + 过期。冲突视为重放，失败关闭。
- `internal/identity/infrastructure/cache/saml_ticket.go`：`SAMLTicketStore`。一次性 `Put(ctx, ticket, SAMLTicket, ttl)` / `Consume`，Redis 用 used-marker + GET/DEL，memory 同样；payload 只含 `AccountID/Provider/IPHash/UAHash/Device/TraceID`，不存令牌。

### transport 与路由

- `internal/identity/transport/c/saml.go`：`SAMLAuthorize`、`SAMLACS`、`SAMLExchange`、`SAMLMetadata`。
  - ACS 必须 `POST` 且 `Content-Type=application/x-www-form-urlencoded`；独立 **512KB** 请求体上限（不能复用 16KB `AuthBodyLimit`）；`NoStore`；不要求 Bearer。
  - ACS 成功返回 `303 Location: <login_redirect_url>?ticket=...`；`login_redirect_url` 为配置固定值并做严格绝对 URL / prod https 校验，禁止用户输入参与拼接。
  - exchange 走现有 `secure` 中间件（NoStore + `AuthBodyLimit`）。
- `internal/identity/transport/c/router.go`：仅当 `h.saml != nil` 注册：
  - `POST /c/v1/auth/saml/{provider}/authorize`
  - `POST /c/v1/auth/saml/acs`
  - `POST /c/v1/auth/saml/exchange`
  - `GET /c/v1/auth/saml/metadata`
- `handler.go` 增加 `SAML *app.SAMLService` 字段，按 OAuth 同样方式注入。

### 装配

- `cmd/internal/identityruntime/runtime_c.go` 新增 `buildSAMLService`，模式对齐 `buildOAuthService`：配置为空返回 nil；构造 provider、state/replay/ticket 存储（Redis 优先，否则内存）、限流与熔断；注入 `app.SAMLMetrics` 并注册 gauge。
- `platform.Deps` 增加可选 `SAMLSPCertificate` 等运行时产物仅由 runtime 内部持有，不下沉 platform。

### 配置模型（`identity.saml`）

```yaml
identity:
  saml:
    enabled: false
    entity_id: "https://c.example.com/c/v1/auth/saml"
    acs_url: "https://c.example.com/c/v1/auth/saml/acs"
    login_redirect_url: "https://app.example.com/login/saml"
    certificate_pem: ""              # SP 公钥证书（可公开）
    private_key: ""                  # 生产由密钥托管注入，禁止明文
    certificate_next_pem: ""
    private_key_next: ""
    auto_provision: false
    timeout: 5s
    clock_skew: 2m
    assertion_ttl: 5m
    request_ttl: 10m
    replay_ttl: 10m
    ticket_ttl: 60s
    rate_limit: { rps: 0, burst: 0, failure_mode: "" }
    providers:
      - name: keycloak
        idp_entity_id: "https://keycloak.example.com/realms/admin-frame"
        metadata_url: "https://keycloak.example.com/realms/admin-frame/protocol/saml/descriptor"
        # metadata_url 与 metadata_xml 二选一
```

- 新增 `IdentitySAMLConfig` / `IdentitySAMLProviderConfig` 于 `internal/platform/config.go`，并在 `validateIdentity()` 中调用 `validateIdentitySAML()`。
- 启动校验：`enabled=false` 却配置 providers → 失败；`enabled=true` 时 entity_id/acs_url/login_redirect_url/证书/私钥/providers 必填；绝对 URL、prod 强制 https、ACS 路径必须等于 `/c/v1/auth/saml/acs`；证书与私钥配对且 RSA ≥ 2048、证书未过期；next 必须成对且与 active 不同；`clock_skew ≤ 5m`、`assertion_ttl ≤ 15m`、`request_ttl ≤ 15m`、`replay_ttl ≥ assertion_ttl + clock_skew`、`ticket_ttl ∈ [30s, 5m]`；provider name 复用 OAuth 正则、唯一；`metadata_url` XOR `metadata_xml`。
- 密钥托管：在 `internal/platform/config_secrets.go` 的 `secretTargets()` 增加 `identity_saml_sp_private_key`、`identity_saml_sp_private_key_next` 逻辑名，映射到 `identity.saml.private_key` / `private_key_next`；生产样例留空。
- 示例配置同步 `configs/prod/modules/identity.yaml.example`（如有分角色样例则一并同步）与 `docs/identity-authn.md §10`。

## 流程与安全细节

1. `authorize`：限流 → 生成 state/requestID → 保存 state（TTL=request_ttl）→ 生成 AuthnRequest URL → 审计 `auth.saml.authorize`（失败关闭）。
2. `acs`：校验方法与 Content-Type → 长度上限 → `RelayState` 非空 → `Consume` state（失败=40100）→ `ParseResponse(expectedRequestID)`，校验 `InResponseTo`、Issuer、Audience（SP entity_id）、Conditions ± clock_skew、`SubjectConfirmationData`、最大断言生命周期。
3. 显式要求 `assertion.Signature != nil`；签名证书必须来自 IdP metadata（未知证书触发限频刷新一次，仍失败则失败关闭）。
4. 加密断言：用 active 私钥解密；失败时用 next 私钥重试；都失败返回 40100。
5. Assertion ID 去重：`SAMLReplayStore.Reserve(provider, sha256(assertionID), expiresAt)`；已存在 → 重放，40100；存储故障 → 50300。
6. 断言校验通过后生成 ticket（32 字节随机），`Put` 成功才重定向，避免「跳转了但兑换不了」。
7. `exchange`：Consume ticket（不存在/过期=40100；存储故障=50300）→ `CompleteVerifiedLogin` → 返回现有 `LoginResult`。
8. 审计：`auth.saml.authorize`、`auth.saml.callback`（attrs `saml_provider`、`reason`）；authorize 审计失败阻断，ACS 审计失败不覆盖业务结果（与 OAuth 一致）。
9. 指标（低基数）：`identity_saml_authorize_total{provider,result,reason}`、`identity_saml_acs_total{provider,result,reason}`、`identity_saml_acs_duration_seconds{provider,result}`。
10. 失败码：签名/时间/Audience/解密/重放/参数 → 40100；state/Redis/DB/账号写入 → 50300；限流 → 42900；provider 不存在 → 40400；SAML 关闭 → 50300。

## 需要同步修改的既有文件

- `internal/shared/audit/actions.go`：新增 `ActionAuthSAMLAuthorize`、`ActionAuthSAMLCallback` 并加入 `KnownActions`。
- `internal/shared/audit/event_test.go`：登记新 action。
- `docs/audit/EVENTS.md`：新增两行（`check-authn.sh` 会一一比对）。
- `api/openapi/c.yaml` 与对应 paths 文件：新增 4 条路由契约。
- `docs/identity-authn.md`：新增 §8.3 SAML 与 §10 配置项说明。
- `docs/iam-p1-5-7-engineering-plan.md` §3.3：标注 S6-3 实施状态（实施完成后再改，避免提前宣称）。
- `configs/prod/modules/identity.yaml.example`：新增 `saml` 段，私钥留空。

## 测试方案

- `internal/identity/app/saml_test.go`：fake provider/state/replay/ticket；覆盖 authorize 生成、state 复用、重放、JIT、错误码、审计。
- `internal/identity/infrastructure/saml/provider_test.go`：运行时生成自签 IdP 密钥与证书，构造签名与加密断言；覆盖篡改、过期、错误 Audience、未知证书、未签名、错误解密 key。
- `internal/identity/infrastructure/saml/metadata_test.go`：URL/XML、双证书轮换、刷新失败保留旧值、entityID 不匹配。
- `internal/identity/infrastructure/cache/saml_replay_test.go` / `saml_ticket_test.go`：并发一次性语义、TTL。
- `internal/platform/config_saml_test.go`：成对/必填/prod https/明文私钥拒绝/禁用却配置。
- `internal/identity/transport/c/saml_test.go`：GET 拒绝、415/413、RelayState 缺失、路由开关、303 跳转。
- `tests/integration/identity/saml_acs_test.go`（可选）：真实 MySQL + Redis 下的全链路。
- 测试密钥全部运行时生成，不落盘，避免 gitleaks。

## 实施顺序（每步可编译、可测试）

1. 引入依赖：`go get github.com/crewjam/saml@<最新稳定 tag>` → `go mod tidy` → `go doc` 核对 `MakeAuthnRequest/GetAuthnRequestURL/ParseResponse` API → `go build ./...`。
2. app 端口 + 共享账号映射抽取；OAuth 回归测试。
3. infrastructure keys/metadata/provider 与运行时密钥夹具。
4. cache replay/ticket 存储。
5. 配置结构、默认值、校验、secrets targets、示例文件。
6. runtime 装配、SAMLMetrics、transport 与路由、OpenAPI。
7. 审计 action 与文档。
8. 安全用例、e2e、全量门禁。

## 验证

```text
go build ./...
go test -race ./internal/identity/... ./internal/platform/... ./cmd/internal/identityruntime/...
gofmt -l cmd internal pkg tests          # 期望无输出
go vet ./...
golangci-lint run ./...                  # 期望 0 issues
bash scripts/ci/check-authn.sh
bash scripts/ci/check-audit.sh
bash scripts/ci/check-config.sh
bash scripts/ci/check-secret-provider.sh
bash scripts/ci/check-license.sh
bash scripts/ci/check-deps.sh
bash scripts/ci/check-openapi.sh
```

## 风险与回滚

- crewjam/saml 各版本 API 存在差异：实施第 1 步以 `go doc` 为准，所有调用收敛在 `infrastructure/saml/provider.go`，app 层不受影响。
- 加密断言依赖 SP 私钥与证书配对：启动期强校验，避免「配置成功但无法解密」。
- `login_redirect_url` 是固定配置并严格校验，不接受用户输入，避免开放重定向。
- 密钥轮换：SP 证书 active+next 同时写入 metadata；IdP 签名证书按 metadata TTL 刷新并保留旧值；重叠窗口后再移除。
- 回滚：`identity.saml.enabled=false` 重启即摘除 4 条路由；不新增迁移、不改令牌契约；保留密钥与缓存键不影响其它流程。
- Redis/DB 故障一律拒绝登录（失败关闭），与 OAuth state 语义一致。
