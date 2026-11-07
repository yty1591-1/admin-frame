# internal/platform 整改任务清单与验收标准

> **状态**：已实施（Implemented，2026-10-20）
> **效力**：本文件是 `internal/platform` 的整改计划，任务验收通过后与 `docs/ARCHITECTURE.md` §5.6 / §6.1 同等约束
> **适用范围**：`internal/platform`、`internal/*/transport`、`internal/shared`、`cmd/*`、`.golangci.yml`、`scripts/ci/*`
> **关联**：`ARCHITECTURE.md` §5.6/§6.1/§7、`CONFIG_STANDARDS.md`（CFG）、`RELIABILITY_STANDARDS.md`（REL）、`OBSERVABILITY_STANDARDS.md`（OBS）、`SECURITY_STANDARDS.md`（SEC）、`TESTING_STANDARDS.md`（TEST）、`CI_STANDARDS.md`（CI）
> **执行方式**：每个任务以「验收标准」全绿 + `make ci` 通过为完成；未通过不得标记完成
> **最后更新**：2026-10-20
> **变更方式**：调整范围 / 新增任务须同步更新本文件「任务总览」与「验收门禁汇总」，经架构 Owner Review

---

## 1. 背景与结论

`internal/platform` 的分层定位（配置、日志、HTTP Server、中间件、追踪、健康检查、存储装配）符合 `ARCHITECTURE.md` §5.6 的方向：

- 只依赖 `pkg/*` 与 `internal/shared/*`，无业务域依赖；
- 配置严格解析 + 环境变量覆盖 + `Validate` 已落地；
- 日志 / 追踪 / 指标 / 探针 / 版本端点已闭环。

但当前实现是「可运行的开发骨架」，距离「可安全上线的生产骨架」仍有 7 个阻断项，主要集中在**真实鉴权缺失、平台层混入业务权限规则、transport 反向依赖 platform、HTTP Server 加固不足、标准请求链路缺超时/限流、配置校验与密钥脱敏缺口、退出路径资源泄漏**。

**结论：保留现有分层架构，不做微服务拆分、不引入 DI 框架；按本清单分三阶段收敛后继续业务开发。**

---

## 2. 范围

### 2.1 在范围内

| 范围 | 说明 |
|---|---|
| `internal/platform` | 配置、Server、中间件、鉴权/授权装配、存储装配、端点挂载 |
| `internal/*/transport/*` | 解除对 `internal/platform` 的依赖，改为注入 |
| `cmd/*` | 退出码与 `defer` 正确性、装配收敛 |
| `.golangci.yml` / `scripts/ci/*` | 补齐分层与覆盖率门禁 |
| 相关文档 | 修正 `ARCHITECTURE.md` 示例漂移，更新受影响的 STANDARDS |

### 2.2 不在范围内（另行立任务）

| 项 | 原因 |
|---|---|
| 真实 IAM / SSO / OIDC 供应商接入 | 需要组织级身份方案与密钥托管决策，本清单只做 JWT 可插拔能力 |
| DB / MQ / Redis 的生产实现 | 属于 `infrastructure` 与 `pkg` 的独立路线图 |
| 全链路压测、容量规划 | 见 `PERFORMANCE_STANDARDS.md` |
| 微服务拆分、`platform` 拆子包 | 当前规模不需要；触发条件见 PLAT-15 |
| 审计事件业务挂接 | 见 `AUDIT_STANDARDS.md` 与 `standards-roadmap.md` §七 |

---

## 3. 问题与任务映射

| 任务 | 问题摘要 | 严重级 | 违反条款 | 现状证据 |
|---|---|---|---|---|
| PLAT-01 | `defer` + `os.Exit` 导致 `stores.Close()` 被跳过 | P0 | REL-02、Go 语义 | `cmd/admin-api/main.go:83-91`（biz/c 同构） |
| PLAT-02 | `http.Server` 只设 `ReadHeaderTimeout`，无 `ErrorLog`/`BaseContext` | P0 | REL-02、OBS-01 | `internal/platform/httpserver/server.go:16-21` |
| PLAT-03 | `log.format` 未校验 → `logger.Must` panic；配置含明文密钥且无脱敏 | P0 | CFG-03/05、SEC-05 | `config.go:194-198`、`logger.go:11-19` |
| PLAT-04 | 无真实鉴权路径，`demo_mode=false` 直接拒绝启动 | P0 | SEC-04、CFG-05 | `middleware.go:67-74` |
| PLAT-05 | `defaultRBAC` 在 platform 内硬编码角色与 `order:*` / `file:*` 权限点 | P0 | ARCH §5.6 | `middleware.go:18-26` |
| PLAT-06 | `transport` 直接 import `platform`（TraceID、RequirePermission） | P0 | ARCH §6.1 | `internal/order/transport/admin/handler.go:8` 等 5 处 |
| PLAT-07 | 无 Timeout / RateLimit / CORS / BodyLimit；REL-05 限流码未落地 | P0 | REL-01/05、ARCH §7 | `cmd/*/main.go` pipeline |
| PLAT-08 | 3 份 API `main.go` 重复装配，修改需同步多处 | P1 | ARCH §5.1 | 106/91/91 行高度重复 |
| PLAT-09 | Storage 三处重复映射；`BucketConfig.ToStorageConfig` 为死代码 | P1 | 可维护性 | `storage.go:67`、`storage_set.go:108-119` |
| PLAT-10 | 全量 blank import 4 套云 SDK | P1 | CI-09、DEP | `storage_drivers.go:5-14` |
| PLAT-11 | platform 覆盖率 54.3%，启动/中间件路径 0% | P1 | TEST-02 精神 | `go test -cover` |
| PLAT-12 | 无 depguard / 分层检查；脚本以字符串 grep 为主，存在假阳性 | P1 | ARCH §6.4、CI | `.golangci.yml`、`check-transport-imports.sh` |
| PLAT-13 | 文档与命名漂移（`NewDB` 不存在、`wiring.go` 语义不符） | P2 | — | `ARCHITECTURE.md:330`、`wiring.go` |
| PLAT-14 | `NewConfigLoader` / `pkg/config.Watcher` 已导出但无场景 | P2 | CFG-04 | 无调用方 |
| PLAT-15 | `platform` 有成为 god package 的趋势 | P2 | — | 8 类职责、694 行（非测试） |

---

## 4. 任务总览

| 任务 | 名称 | 阶段 | 预估 | 依赖 | 主责 |
|---|---|---|---|---|---|
| PLAT-01 | 修正退出路径与错误出口 | P0 | 0.3d | — | 后端 |
| PLAT-02 | HTTP Server 生产加固 | P0 | 0.3d | PLAT-03 | 后端 |
| PLAT-03 | 配置校验补全与密钥脱敏 | P0 | 0.5d | — | 后端 |
| PLAT-04 | 接入真实鉴权（JWT，可插拔） | P0 | 1.0d | PLAT-03 | 后端 + 安全 |
| PLAT-05 | 授权去业务化并改为注入 | P0 | 0.5d | PLAT-04 | 后端 |
| PLAT-06 | transport 解除对 platform 的依赖 | P0 | 0.5d | PLAT-05 | 后端 |
| PLAT-07 | 标准请求链路（超时/限流/CORS/体积） | P0 | 0.8d | PLAT-02 | 后端 |
| PLAT-08 | cmd 收敛到 `platform.Run` | P1 | 1.0d | PLAT-01/02/07 | 后端 |
| PLAT-09 | Storage 配置映射去重 | P1 | 0.3d | — | 后端 |
| PLAT-10 | 云存储驱动按需注册 | P1 | 0.3d | PLAT-09 | 后端 |
| PLAT-11 | platform 测试补齐与覆盖率门槛 | P1 | 1.0d | PLAT-01/02/05/07 | 后端 |
| PLAT-12 | 分层与覆盖率门禁 | P1 | 0.5d | PLAT-06/11 | 后端 + CI |
| PLAT-13 | 命名与文档漂移修复 | P2 | 0.2d | PLAT-08 | 后端 |
| PLAT-14 | 配置热更新能力定位 | P2 | 0.5d | PLAT-08 | 后端 |
| PLAT-15 | 包拆分触发条件文档化 | P2 | 0.2d | — | 架构 |

**里程碑**

| 里程碑 | 包含 | 出口标准 |
|---|---|---|
| M1 可安全上线 | PLAT-01 ～ PLAT-07 | `make ci` 绿；prod 配置可启动；`/readyz` 失败返回 503；限流返回 `42900` |
| M2 结构收敛 | PLAT-08 ～ PLAT-12 | 4 个 cmd 共用同一装配入口；transport 零 platform 依赖；platform 覆盖率 ≥ 70% |
| M3 收尾 | PLAT-13 ～ PLAT-15 | 文档与代码一致；无未定义行为的预留 API |

---

## 5. P0 任务（上线阻断）

### PLAT-01 修正退出路径与错误出口

**目标**：确保任何退出路径上所有 `defer` 都执行，退出码可控。

**现状**

`cmd/admin-api/main.go` 中两个 `defer` 呈 LIFO：后注册的先执行，命中 `os.Exit(exitCode)` 后先注册的 `stores.Close()` 永远不会执行：

```go
defer func() { _ = stores.Close() }()
exitCode := 0
defer func() {
	_ = tp.Shutdown(context.Background())
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}()
```

**整改动作**

1. 每个 `cmd/*/main.go` 改为 `main()` + `run() error` 结构，`main` 只负责打印错误与 `os.Exit(1)`。
2. 所有资源关闭改为 `defer` 在 `run` 内注册，按「后申请先关闭」顺序（当前 `worker` 的 `defer` 顺序正确，可平移不改语义）。
3. 删除 `exitCode` 变量与「此后不得 `log.Fatalf`」的注释约定，改用错误返回。
4. `worker` / `migrate` 同样处理；`migrate` 若以 `log.Fatalf` 退出，改为返回 error 以统一出口。

**变更文件**

- `cmd/admin-api/main.go`、`cmd/biz-api/main.go`、`cmd/c-api/main.go`
- `cmd/worker/main.go`、`cmd/migrate/main.go`（如存在同类模式）

**验收标准**

- [ ] AC-PLAT-01-1：4 个 `cmd/*/main.go` 中 `main()` 函数体只包含 `if err := run(); err != nil { ... os.Exit(1) }`。
- [ ] AC-PLAT-01-2：仓库内 `cmd/` 下 `os.Exit` 仅出现在 `main()` 内，`grep -RIn "os.Exit" cmd/` 结果不超过 cmd 数量。
- [ ] AC-PLAT-01-3：新增单测 `TestRunClosesStorageOnServerError`（可用注入的 fake closer）断言服务启动失败时 `Close` 被调用。
- [ ] AC-PLAT-01-4：`go build ./...`、`go vet ./...`、`go test ./...` 全绿。
- [ ] AC-PLAT-01-5：手工验证：占用端口后启动进程，退出码非 0 且日志含 storage 关闭记录。

**门禁**：`make ci`；Review 检查「`defer` 与 `os.Exit` 不得出现在同一函数」。

---

### PLAT-02 HTTP Server 生产加固

**目标**：补齐超时、日志接入、上下文继承与启动日志真实性。

**现状**

```go
srv := &http.Server{
	Addr:              addr,
	Handler:           handler,
	ReadHeaderTimeout: 5 * time.Second,
}
```

缺少 `ReadTimeout` / `IdleTimeout` / `WriteTimeout`、`ErrorLog`、`BaseContext`；`log.Info("server listening")` 在 `ListenAndServe` 之前打印，绑定失败也会输出「已监听」。

**整改动作**

1. 新增配置段（含默认值与 `Validate`）：

```yaml
http:
  read_timeout: 15s
  write_timeout: 30s          # SSE/大文件下载场景须显式说明并单独放行
  idle_timeout: 60s
  max_header_bytes: 1048576
```

2. 抽出可测构造函数 `newHTTPServer(cfg Config, handler http.Handler, log *slog.Logger) *http.Server`。
3. `ErrorLog` 接入 `slog`（`slog.NewLogLogger(log.Handler(), slog.LevelWarn)`）。
4. `BaseContext` 返回进程 `ctx`，使请求上下文随进程取消。
5. `defer signal.Stop(sigCh)`；监听成功后再输出 `server listening`（用 `net.Listen` 先绑定再 `Serve`）。
6. 若决定由 Ingress 承担读写超时，须在 `server.go` 与本文件写明理由与对应配置项，不得留空。

**变更文件**

- `internal/platform/httpserver/server.go`、`internal/platform/config/config.go`
- `configs/*.yaml.example`、`docs/CONFIG_STANDARDS.md` §4 变量表

**验收标准**

- [ ] AC-PLAT-02-1：`newHTTPServer` 返回的 `*http.Server` 中 `ReadHeaderTimeout`、`ReadTimeout`、`IdleTimeout` 均非 0；`ErrorLog` 非 nil；`BaseContext` 非 nil（单测断言）。
- [ ] AC-PLAT-02-2：单测 `TestNewHTTPServerAppliesConfig` 覆盖自定义配置与零值默认。
- [ ] AC-PLAT-02-3：单测 `TestListenAndServeReturnsBindError`：占用端口后返回错误且不打印 listening 日志。
- [ ] AC-PLAT-02-4：单测 `TestListenAndServeGracefulShutdown`：收到取消后正在处理的请求完成，`Serve` 返回 nil。
- [ ] AC-PLAT-02-5：`configs/*.yaml.example` 四份均包含 `http` 段；`ADMIN_FRAME_HTTP_*` 写入 `CONFIG_STANDARDS.md` 变量表。
- [ ] AC-PLAT-02-6：`go test ./internal/platform/... -run 'TestNewHTTPServer|TestListenAndServe' -race` 通过。

**门禁**：`make check-reliability` 扩展为断言 `ReadTimeout`/`IdleTimeout` 字段存在，而非仅 `shutdownTimeout`。

---

### PLAT-03 配置校验补全与密钥脱敏

**目标**：非法配置返回错误而非 panic；敏感字段不被日志打印。

**现状**

- `Validate` 校验 `log.level` 但不校验 `log.format`，而 `NewLogger` 使用 `logger.Must`，`log.format: xml` 会在启动时 panic。
- `Config` 含 `DB.DSN`、`Storage.S3.SecretKey`、`SessionToken` 等，无 `LogValue` / `Redacted`。
- `Validate` 只返回首个错误，多错需多次重启才能发现。
- OBS §9「`text` 仅限 local/dev」未落到校验。

**整改动作**

1. `Validate` 增加 `log.format` ∈ {json, text}；`env=prod` 禁止 `text`。
2. 为 `Config` 及 `AuthConfig` / `StorageConfig` 实现 `slog.LogValuer`（或 `Redacted() Config`），密钥字段输出 `***`；`DSN` 只保留 scheme 与 host。
3. `Validate` 用 `errors.Join` 聚合全部错误。
4. 保留「错误信息不含取值」约定，聚合后逐条校验。
5. 为 `NewLogger` 增加 `error` 返回的重载（`NewLoggerE`），或改为 `NewLogger(cfg) (*slog.Logger, error)`；若保留 `Must`，必须在 `Validate` 阶段完全排除 panic 可能并注释说明。

**变更文件**

- `internal/platform/config/config.go`、`internal/platform/logger.go`、`internal/platform/config_test.go`

**验收标准**

- [ ] AC-PLAT-03-1：`log.format: xml` 的配置使 `LoadConfig` 返回 error，进程不 panic（单测 `TestLoadConfigRejectsInvalidLogFormat`）。
- [ ] AC-PLAT-03-2：`env=prod` + `log.format=text` 返回 error；`env=local` 允许。
- [ ] AC-PLAT-03-3：`TestConfigLogValueRedactsSecrets`：`slog.NewJSONHandler` 输出中不出现 `SecretKey`、`DSN`、`SessionToken` 原文，且出现 `***` 或等价占位。
- [ ] AC-PLAT-03-4：`TestValidateAggregatesErrors`：同时构造 3 处非法配置，返回错误文本包含 3 条。
- [ ] AC-PLAT-03-5：`go test ./internal/platform/... -run TestValidate|TestLoadConfig -race` 通过。

**门禁**：`make check-config`；SEC CI 的 gitleaks 不受影响。

---

### PLAT-04 接入真实鉴权（JWT，可插拔）

**目标**：提供 `demo_mode=false` 时的可用鉴权实现，而不是只拒绝启动。

**现状**

```go
if !cfg.DemoMode {
	return nil, fmt.Errorf("auth.demo_mode=false: production authenticator is not configured")
}
```

`AuthConfig` 只有 `DemoMode`、`ExpectedChannel`，没有 issuer / audience / 密钥 / leeway，任何环境都无法接入真实身份。

**整改动作**

1. `AuthConfig` 增加：

```yaml
auth:
  mode: demo | jwt
  expected_channel: admin
  jwt:
    alg: HS256 | RS256
    issuer: admin-issuer
    audience: [admin]
    leeway: 30s
    # HS256: secret / secrets（轮换）→ ADMIN_FRAME_AUTH_JWT_SECRET
    # RS256: private_key_file / public_key_file / public_keys_file
```

2. 新增 `internal/security` 包（不放入 `platform`，避免 platform 依赖验签实现）：
   - `security.NewDemoAuth(channel)`
   - `security.NewJWTAuth(cfg, channel, clock) (Middleware, error)`，内部使用 `pkg/jwt.Manager.Parse`。
3. claims → `auth.Principal` + `contextx.Identity` 映射：`sub` → `SubjectID`，`merchant_id`、`roles` 为可选 claims。
4. `NewAuthMiddleware` 改为：
   - `mode=demo` 且 `env ∈ {local,dev,test}` → DemoAuth；
   - `mode=demo` 且 `env=prod` → 拒绝启动（保留 CFG-05）；
   - `mode=jwt` → 构造 JWT 中间件，强制校验 `aud == channel`。
5. `Validate` 增加：`mode` 枚举；`jwt` 时 issuer/audience 必填、audience 必须包含 `channel`、HS256 密钥 ≥32 字节、RS256 公钥可解析。
6. 密钥只从环境变量 / 文件读取，不写入 yaml 样例明文。

**变更文件**

- `internal/security/{authn_demo.go,authn_jwt.go,authz.go,doc.go}`（新增）
- `internal/platform/httpserver/middleware.go`、`internal/platform/config/config.go`
- `configs/*.yaml.example`、`docs/SECURITY_STANDARDS.md`、`docs/CONFIG_STANDARDS.md`

**验收标准**

- [ ] AC-PLAT-04-1：`mode=demo` + `env=prod` → `LoadConfig` 或 `NewAuthMiddleware` 返回 error（不静默回退）。
- [ ] AC-PLAT-04-2：`mode=jwt` + `env=prod` → 进程可启动；携带合法 JWT 的请求返回 200。
- [ ] AC-PLAT-04-3：`TestJWTAuthRejectsWrongAudience`、`TestJWTAuthRejectsExpired`、`TestJWTAuthRejectsWrongIssuer` 均返回 401 且响应信封 `code` 为未授权码。
- [ ] AC-PLAT-04-4：`TestPrincipalMapping`：claims 正确映射 `SubjectID` / `MerchantID` / `Roles`，且 `contextx.IdentityFrom` 可见同值。
- [ ] AC-PLAT-04-5：`pkg/jwt` 未修改公开 API；`internal/security` 不 import `internal/platform`（`go list` 断言）。
- [ ] AC-PLAT-04-6：`docs/SECURITY_STANDARDS.md` 补充 `auth.mode` 与密钥托管要求，SEC-04 状态更新为「已提供 JWT 实现」。

**门禁**：`make check-config`、`make secrets`；新增单测进入 CI test job。

---

### PLAT-05 授权去业务化并改为注入

**目标**：platform 不再出现角色名与业务权限点。

**现状**

`internal/platform/httpserver/middleware.go:18-26` 的 `defaultRBAC` 硬编码 `admin`、`merchant_staff`、`ops` 与 `order:read`、`file:write` 等权限点，违反 `ARCHITECTURE.md:400`。

**整改动作**

1. 删除 `defaultRBAC` 与 `RequirePermission(perm string)` 的包级实现。
2. `internal/security` 提供：

```go
type Authorizer struct{ checker *rbac.Checker }
func NewAuthorizer(checker *rbac.Checker) *Authorizer
func (a *Authorizer) Require(perm string) func(http.Handler) http.Handler
```

3. 权限点常量下沉到各域（如 `internal/order` 导出 `PermOrderRead`/`PermOrderWrite`，`internal/files` 导出 `PermFileRead`/`PermFileWrite`）。
4. 演示用角色表移入 `cmd` 的装配分支（`env=local` 时构造），或放入 `internal/security/demo_roles.go` 并明确标注「仅演示」。
5. platform 只保留「AuthN 中间件工厂」的编排职责，不持有权限数据。

**变更文件**

- `internal/platform/httpserver/middleware.go`
- `internal/security/authz.go`（新增）
- `internal/order/*`、`internal/files/*`（权限点常量）
- `cmd/*/main.go`（注入 checker）

**验收标准**

- [ ] AC-PLAT-05-1：`internal/platform/` 下 `grep -RIn "order:\|file:\|merchant_staff"` 无结果。
- [ ] AC-PLAT-05-2：`grep -RIn "defaultRBAC" .` 无结果。
- [ ] AC-PLAT-05-3：`TestAuthorizerDeniesWithoutPermission` / `TestAuthorizerAllowsGranted` / `TestAuthorizerUnauthenticated` 三态通过。
- [ ] AC-PLAT-05-4：新增一个域时无需修改 `internal/platform`（Review 确认）。
- [ ] AC-PLAT-05-5：`go build ./...` + 三端路由原有集成用例全绿。

**门禁**：Review + PLAT-12 的分层规则（platform 不得 import 业务域）。

---

### PLAT-06 transport 解除对 platform 的依赖

**目标**：恢复 `transport → app / domain / shared` 的单向依赖。

**现状**

- `internal/order/transport/{admin,biz,c}/handler.go`、`internal/files/transport/admin/handler.go` 使用 `platform.TraceIDFromContext`。
- `internal/order/transport/{admin,biz}/router.go`、`internal/files/transport/admin/router.go` 使用 `platform.RequirePermission`。

**整改动作**

1. `TraceIDFromContext` 下沉到 `internal/shared/httpx`（内部转调 `pkg/contextx.RequestID`），transport 改 import `httpx`。
2. 路由注册函数签名改为注入式：

```go
type PermissionFunc func(permission string) func(http.Handler) http.Handler

func Register(mux *http.ServeMux, h *Handler, authMW func(http.Handler) http.Handler, perm PermissionFunc)
```

3. `cmd` 传入 `security.Authorizer.Require`（PLAT-05）。
4. 删除 transport 中对 `platform` 的全部 import。

**变更文件**

- `internal/shared/httpx/trace.go`（新增）
- `internal/order/transport/{admin,biz,c}/{handler.go,router.go}`
- `internal/files/transport/admin/{handler.go,router.go}`
- `cmd/*/main.go`

**验收标准**

- [ ] AC-PLAT-06-1：`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/...` 的输出中，`/transport` 包的 import 不含 `internal/platform`。
- [ ] AC-PLAT-06-2：`grep -RIn "internal/platform" internal/order/transport internal/files/transport` 无结果（`_test.go` 除外）。
- [ ] AC-PLAT-06-3：`httpx.TraceIDFromContext` 有单测，行为与 OTel TraceID 复用一致（迁移 `tracing_middleware_test.go` 中的断言）。
- [ ] AC-PLAT-06-4：三端路由的鉴权/鉴权用例全部通过，401/403 信封不变。
- [ ] AC-PLAT-06-5：`make check-transport` 通过且脚本已覆盖 platform 规则（PLAT-12）。

**门禁**：`make check-transport` + depguard（PLAT-12）。

---

### PLAT-07 标准请求链路（超时 / 限流 / CORS / 体积）

**目标**：所有 API 进程共用一条可配置、顺序固定的中间件链路；REL-05 限流码落地。

**现状**

`cmd/*/main.go` 手工拼装 `Recover → Trace → RequestID → Metrics → AccessLog`，`pkg/middleware` 已有 `Timeout` / `HardTimeout` / `RateLimit` / `CORS` 但无人使用。

**整改动作**

1. `internal/platform/httpserver/pipeline.go` 新增：

```go
type PipelineConfig struct {
	Timeout      time.Duration
	HardTimeout  bool
	BodyLimit    int64
	RateLimit    *RateLimitConfig   // channel 级
	CORS         *middleware.CORSConfig
}

func NewPipeline(cfg PipelineConfig, deps PipelineDeps) func(http.Handler) http.Handler
```

2. 固定顺序（写入注释与文档）：

```text
Recover → Trace → RequestID → Metrics → AccessLog → Timeout → BodyLimit → RateLimit → Handler
```

3. AuthN / AuthZ 仍由 cmd 在 transport 注册处注入，`NewPipeline` 不处理鉴权。
4. `RateLimit` 拒绝时返回 `apperr.TooManyRequests(...)`，信封码 `42900`；`Timeout` 超时返回 503/504 与稳定码。
5. 配置新增 `http.timeout`、`http.body_limit_bytes`、`rate_limit.{rps,burst,enabled}`；C 端示例给出更强限流值。
6. `CORS` 仅在显式配置 `allow_origins` 时启用，禁止反射任意 Origin。

**变更文件**

- `internal/platform/httpserver/pipeline.go`（新增）、`internal/platform/config/config.go`
- `cmd/*/main.go`、`configs/*.yaml.example`
- `docs/RELIABILITY_STANDARDS.md`（补充实现位置）、`docs/CONFIG_STANDARDS.md`

**验收标准**

- [ ] AC-PLAT-07-1：`TestPipelineOrder` 断言中间件执行顺序与文档一致（用记录切片的探针中间件）。
- [ ] AC-PLAT-07-2：`TestPipelineRateLimitReturns42900`：超限请求返回 HTTP 429，信封 `code == 42900`。
- [ ] AC-PLAT-07-3：`TestPipelineTimeout`：处理器阻塞超过 `http.timeout` 时按配置返回超时码，且不 panic。
- [ ] AC-PLAT-07-4：`TestPipelineBodyLimit`：超限请求返回 413 或 `apperr` 稳定码。
- [ ] AC-PLAT-07-5：三端 `main.go` 均通过 `NewPipeline` 装配，不再手写中间件序列。
- [ ] AC-PLAT-07-6：`make check-reliability` 通过；`docs/RELIABILITY_STANDARDS.md` §11 工具表补充 `internal/platform/httpserver/pipeline.go`。

**门禁**：`make check-reliability`、单测、三端冒烟。

---

## 6. P1 任务（结构收敛）

### PLAT-08 cmd 收敛到 `platform.Run`

**目标**：4 个进程共用同一装配流程，cmd 只声明「本端路由 + 依赖装配」。

**整改动作**

1. `internal/platform/bootstrap.go` 新增：

```go
type Options struct {
	ConfigPath string
	Build      BuildInfo
	Register   func(deps Deps, mux *http.ServeMux) error
}

type Deps struct {
	Config            *Config
	Logger            *slog.Logger
	Metrics           *metrics.Metrics     // pkg/metrics.New 的返回值
	Tracer            *tracing.Provider    // pkg/tracing.Init 的返回值
	Storage           *StorageSet
	AuthMW            func(http.Handler) http.Handler
	RequirePermission func(string) func(http.Handler) http.Handler
	Health            *health.Handler
}

func Run(ctx context.Context, opts Options) error
```

2. `Run` 内部完成：配置加载 → 日志 → 指标 → tracing → 存储 → pipeline → 端点挂载 → 监听 → 优雅关闭 → 资源释放；任一阶段失败返回 error。
3. 各 cmd 的 `run()` 收敛为「构造业务依赖 + `Register` 回调」，目标 ≤ 30 行。
4. `Deps` 只暴露平台能力，不暴露 repo/bus 等业务对象，避免变成 god object。

**变更文件**

- `internal/platform/bootstrap.go`（新增）
- `cmd/{admin-api,biz-api,c-api,worker}/main.go`

**验收标准**

- [ ] AC-PLAT-08-1：`cmd/*-api/main.go` 行数 ≤ 30（不含 package/import 注释可放宽至 40），且不含 `http.Server`、`signal.Notify`、`tracing.Init` 等平台细节。
- [ ] AC-PLAT-08-2：4 个进程 `go build ./...` 成功，冒烟：`/healthz` `/readyz` `/version` `/metrics` 全部可用。
- [ ] AC-PLAT-08-3：`platform.Run` 有单测覆盖「配置非法 → 返回 error」「Register 返回 error → 资源已释放」。
- [ ] AC-PLAT-08-4：`Deps` 字段中不含任何业务域类型（Review 确认）。
- [ ] AC-PLAT-08-5：`make ci` 绿。

**门禁**：`make ci` + Review。

---

### PLAT-09 Storage 配置映射去重

**目标**：消除三处重复映射与死代码。

**整改动作**

1. 抽出共享连接结构：

```go
type storageConn struct {
	Driver         string        `yaml:"driver"`
	KeyPrefix      string        `yaml:"key_prefix"`
	MaxObjectBytes int64         `yaml:"max_object_bytes"`
	PresignPutTTL  time.Duration `yaml:"presign_put_ttl"`
	PresignGetTTL  time.Duration `yaml:"presign_get_ttl"`
	Local storage.LocalConfig `yaml:"local"`
	S3    storage.S3Config    `yaml:"s3"`
	OSS   storage.OSSConfig   `yaml:"oss"`
	COS   storage.COSConfig   `yaml:"cos"`
	OBS   storage.OBSConfig   `yaml:"obs"`
}
```

2. `StorageConfig` / `BucketConfig` 内嵌 `storageConn`，只保留各自差异字段（顶层 `Buckets` / `Lifecycle`）。
3. `OpenStorageSet` 统一调用 `bcfg.ToStorageConfig()`，删除手写映射。
4. 删除未使用的 `BucketConfig.ToStorageConfig` 或补齐调用方（二选一，不得留死代码）。
5. `ValidateStorage` 同步适配内嵌字段。

**变更文件**

- `internal/platform/storage.go`、`internal/platform/storage_set.go`、`internal/platform/storage_set_test.go`

**验收标准**

- [ ] AC-PLAT-09-1：`grep -c "storage.Config{" internal/platform/*.go` 结果 ≤ 2（两个 `ToStorageConfig`）。
- [ ] AC-PLAT-09-2：`TestStorageConfigFieldParity`：用反射遍历 `storage.Config` 字段，断言 `StorageConfig` 能映射全部字段且无遗漏（防新增字段漏改）。
- [ ] AC-PLAT-09-3：`go tool cover -func` 中 `BucketConfig.ToStorageConfig` 覆盖率 > 0（不再死代码）。
- [ ] AC-PLAT-09-4：多桶、生命周期、命名冲突、非法 driver 的原有用例全部通过。
- [ ] AC-PLAT-09-5：`configs/*.yaml.example` 未发生字段路径变更（兼容性）。

**门禁**：`go test ./internal/platform/...`、`make check-config`。

---

### PLAT-10 云存储驱动按需注册

**目标**：不需要多云能力的进程不链接四套 SDK。

**整改动作**

1. 将 `storage_drivers.go` 的 blank import 迁移到 `pkg/storage/all`（或按云拆 `pkg/storage/drivers/{awss3,oss,cos,obs}`）。
2. `internal/platform` 不再主动注册驱动；由 `cmd`（或 `Deps` 装配点）显式 import。
3. 在 `pkg/storage/doc.go` 写明「未 import 对应驱动包时，`Open` 返回未注册错误」。
4. 记录改造前后二进制体积与 `go list -deps` 差异。

**变更文件**

- `internal/platform/storage_drivers.go`（删除或改为文档说明）
- `pkg/storage/all/all.go`（新增）、`pkg/storage/doc.go`
- `cmd/*/main.go`

**验收标准**

- [ ] AC-PLAT-10-1：未 import 的 cmd 的 `go list -deps` 中不含 `pkg/storage/{cos,obs,oss,s3}`。
- [ ] AC-PLAT-10-2：显式 import 后 `Open` 对各 driver 行为不变（既有 storage 单测全绿）。
- [ ] AC-PLAT-10-3：`bin/` 下二进制体积变化记录在 PR 描述（前后对比）。
- [ ] AC-PLAT-10-4：`make build`、`make ci` 绿；Dockerfile 构建参数不受影响。

**门禁**：`make check-deps`、`go list -deps` 断言脚本（可并入 PLAT-12）。

---

### PLAT-11 platform 测试补齐与覆盖率门槛

**目标**：启动路径与中间件路径具备回归保护。

**现状**（`go test ./internal/platform/ -coverprofile` 实测）

| 函数 | 覆盖率 |
|---|---|
| `ListenAndServe` | 0% |
| `Recover` | 0% |
| `AccessLog` | 0% |
| `RequirePermission` | 0% |
| `MountHealth` / `MountVersion` / `MountMetrics` | 0% |
| `TracingOptions` | 0% |
| `parseDemoToken` | 0% |
| `NewLogger` | 0% |
| 合计 | 54.3% |

**整改动作**：补齐以下测试（文件名建议按职责拆分）。

| 测试 | 断言要点 |
|---|---|
| `server_test.go`::`TestListenAndServeGracefulShutdown` | ctx 取消后返回 nil，处理中的请求完成 |
| `server_test.go`::`TestListenAndServeReturnsBindError` | 端口占用返回 error |
| `middleware_test.go`::`TestRecoverWritesEnvelope` | panic → 500 + 信封 + 日志含 trace_id |
| `middleware_test.go`::`TestAccessLogFields` | 含 method/path/status/duration/trace_id |
| `middleware_test.go`::`TestChainSkipsNilAndKeepsOrder` | 顺序与 nil 过滤 |
| `security_test.go`::`TestAuthorizerStates` | 未认证 401 / 无权限 403 / 放行 200 |
| `endpoints_test.go`::`TestMountVersionJSON` | 字段完整、Content-Type 正确 |
| `endpoints_test.go`::`TestMountHealthProbes` | `/healthz` 200、`/readyz` 失败 503 |
| `endpoints_test.go`::`TestMountMetricsNilSafe` | nil handler 不 panic 且不注册 |
| `tracing_test.go`::`TestTracingOptionsMapping` | enabled=false → noop；字段映射正确 |
| `middleware_test.go`::`TestParseDemoToken` | 各端合法/非法 token 表驱动 |

**验收标准**

- [ ] AC-PLAT-11-1：`go test ./internal/platform/... -coverprofile=c.out` 后 `go tool cover -func=c.out | tail -1` 显示 `total ≥ 70.0%`。
- [ ] AC-PLAT-11-2：上表所有函数在 `go tool cover -func` 输出中覆盖率 > 0%。
- [ ] AC-PLAT-11-3：`go test ./internal/platform/... -race -count=1` 通过。
- [ ] AC-PLAT-11-4：测试不依赖真实网络、真实云凭据、固定端口（使用 `httptest` / 随机端口）。
- [ ] AC-PLAT-11-5：`TESTING_STANDARDS.md` §5 增加 platform 包门槛条目（与 domain 门槛并列），并同步 `check-test-gates.sh`。

**门禁**：`make test-ci`、`make check-test-gates`。

---

### PLAT-12 分层与覆盖率门禁

**目标**：让 PLAT-05/06 类违规在 CI 即失败，而不是靠 Review。

**整改动作**

1. `.golangci.yml` 启用 `depguard`：

```yaml
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        transport-isolation:
          files:
            - "**/internal/*/transport/**"
          deny:
            - pkg: "github.com/admin-frame/admin-frame/internal/platform"
              desc: "transport 只能依赖 app/domain/shared；平台能力由 cmd 注入"
        platform-no-business:
          files:
            - "**/internal/platform/**"
          deny:
            - pkg: "github.com/admin-frame/admin-frame/internal/order"
              desc: "platform 不得依赖业务域"
            - pkg: "github.com/admin-frame/admin-frame/internal/files"
              desc: "platform 不得依赖业务域"
        pkg-no-internal:
          files:
            - "**/pkg/**"
          deny:
            - pkg: "github.com/admin-frame/admin-frame/internal"
              desc: "pkg 不得反向依赖 internal"
```

2. 新增 `scripts/ci/check-layering.sh`：用 `go list -f` / `go list -deps` 做真实依赖断言（覆盖 depguard 漏网的 test-only import 与 `_` import）。
   > 说明：上文 `depguard` 片段为 v2 结构，落地时须以本地 `golangci-lint`（钉扎 v2.14.0）实跑校验，若 schema 差异以实际报错为准，并同步 `.golangci.yml` 注释。
3. `check-reliability.sh` 的 REL-02 检查由「grep 变量名」升级为「断言 `ReadTimeout`/`IdleTimeout` 字段 + `http` 配置段」。
4. platform 覆盖率门槛接入 `check-test-gates.sh`（`TEST_PLATFORM_COVER_MIN=70`，禁止下调）；同时补充 `internal/security` 覆盖率门槛（`TEST_SECURITY_COVER_MIN=80`，禁止下调）。
5. `Makefile` 增加 `check-layering` target，并纳入 `ci` 与 `gitee-job.sh arch`。

**变更文件**

- `.golangci.yml`、`Makefile`、`scripts/ci/check-layering.sh`（新增）、`scripts/ci/check-reliability.sh`、`scripts/ci/check-test-gates.sh`、`scripts/ci/gitee-job.sh`

**验收标准**

- [ ] AC-PLAT-12-1：临时在 transport 中 import `internal/platform`，`make lint` 失败且提示来自 `transport-isolation`。
- [ ] AC-PLAT-12-2：临时在 platform 中 import `internal/order`，`make lint` 失败。
- [ ] AC-PLAT-12-3：临时把平台覆盖率降到 70% 以下，`make check-test-gates` 失败（验证门槛真实生效）。
- [ ] AC-PLAT-12-4：恢复上述临时改动后 `make ci` 全绿。
- [ ] AC-PLAT-12-5：`scripts/ci/check-layering.sh` 在 Linux CI 与本地 WSL/Git Bash 均可运行；Windows 下由 `make` 前置说明替代方案。
- [ ] AC-PLAT-12-6：`docs/standards-roadmap.md` 与 `CI_STANDARDS.md` 同步新增门禁条目。

**门禁**：`make ci`；分支保护必过。

---

## 7. P2 任务（收尾）

### PLAT-13 命名与文档漂移修复

**整改动作**

1. `internal/platform/wiring.go` 更名为 `endpoints.go`（职责是 health/version/metrics 挂载，不是依赖注入）。
2. `ARCHITECTURE.md` §5.1 示例中的 `platform.NewDB(cfg.DB)` 改为真实存在的调用，或标注「示意，实际由 `infrastructure` 提供」。
3. `platform.ListenAndServe` / `OpenStorageSet` 等导出函数补齐「生命周期 / 调用时机」注释。
4. `docs/ARCHITECTURE.md` §5.6 补充 platform 的「不包含内容」清单（权限点、角色表、业务校验）。

**验收标准**

- [ ] AC-PLAT-13-1：`grep -rn "NewDB" docs/` 不再出现误导性示例（或已标注示意）。
- [ ] AC-PLAT-13-2：`wiring.go` 不存在，`endpoints.go` 有对应说明；引用该文件的 CI 脚本同步更新。
- [ ] AC-PLAT-13-3：`make check-observability` 通过（脚本中对 `wiring.go` 的路径断言已更新）。
- [ ] AC-PLAT-13-4：新增导出符号均通过 `revive` 的 `exported` 规则。

**门禁**：`make lint`、`make check-observability`。

---

### PLAT-14 配置热更新能力定位

**目标**：消除「有 API 无场景」的模糊能力。

**整改动作（二选一，须在 PR 描述写明决策）**

- **方案 A（推荐，暂不启用）**：保留 `NewConfigLoader`，在注释与 `CONFIG_STANDARDS.md` §4 明确「仅支持启动期加载；热更新为预留能力，启用须 ADR」。同时列出可热更新的字段白名单（如 `log.level`）。
- **方案 B（启用）**：`platform.Run` 内部接入 `config.Watcher`，只对白名单字段生效；变更事件写审计/日志；`/readyz` 反映当前快照版本。

**验收标准**

- [ ] AC-PLAT-14-1：`NewConfigLoader` 的注释明确「当前是否有调用方」与「可热更新字段范围」。
- [ ] AC-PLAT-14-2：若选方案 B，`TestWatcherAppliesLogLevel` 通过，且非法热更新不改变当前快照（保持旧值并记录 error）。
- [ ] AC-PLAT-14-3：`CONFIG_STANDARDS.md` 与代码注释一致，无「已支持热更新」的过度承诺。
- [ ] AC-PLAT-14-4：`make check-config` 通过。

**门禁**：Review + 文档一致性检查。

---

### PLAT-15 包拆分触发条件文档化

**目标**：避免过早拆分，也避免无计划膨胀成 god package。

**整改动作**

在 `docs/ARCHITECTURE.md` §5.6 或 ADR 中写明拆分触发条件，满足任一即启动评估：

1. `internal/platform` 非测试代码 > 800 行，或对外导出符号 > 25 个；
2. 职责类别 > 10 类；
3. 单个文件 > 250 行且包含 2 类以上职责；
4. 出现第 2 个进程形态（如 gRPC-only 服务）需要不同的装配路径。

拆分方向（届时执行）：`platform/{config,httpserver,auth,storage}` + `platform` 作为 facade，保持 `cmd` 调用面不变。

**验收标准**

- [ ] AC-PLAT-15-1：触发条件已写入架构文档并可被 Review 引用。
- [ ] AC-PLAT-15-2：给出当前度量基线（694 行非测试代码、导出符号数、8 类职责），便于后续比对。
- [ ] AC-PLAT-15-3：明确「facade 保持调用面不变」的兼容性要求。

**门禁**：架构 Owner Review。

---

## 8. 通用 Definition of Done

每个任务除自身验收标准外，必须同时满足：

- [ ] `gofmt` / `goimports` 干净（`make fmt-check`）。
- [ ] `go vet ./...` 无输出。
- [ ] `golangci-lint run ./...` 无新增告警；新增 `nolint` 必须带原因与具体 linter。
- [ ] `go test ./... -race` 全绿。
- [ ] 新增行为有对应单测；修复缺陷有复现用例。
- [ ] 导出符号有注释，且说明「调用时机 / 并发安全 / 失败语义」。
- [ ] 受影响的 `docs/*_STANDARDS.md` 与 `configs/*.yaml.example` 同步更新。
- [ ] PR 描述包含：变更动机、验收命令与输出、风险与回滚方式。
- [ ] 无真实密钥、无生产 DSN、无固定端口/固定路径写入代码或测试。

---

## 9. 验收门禁汇总

| 门禁 | 覆盖任务 | 入口 | 失败即阻断 |
|---|---|---|---|
| 格式 / 依赖整理 | 全部 | `make fmt-check`、`make tidy-check` | 是 |
| 静态检查 | PLAT-05/06/12/13 | `make lint`（含 depguard） | 是 |
| 分层依赖 | PLAT-06/12 | `make check-layering` | 是 |
| 单测 + race + 覆盖率 | PLAT-01/02/03/05/07/11 | `make test-ci` | 是 |
| 配置规范 | PLAT-02/03/04/07/14 | `make check-config` | 是 |
| 可靠性 | PLAT-02/07 | `make check-reliability` | 是 |
| 可观测性 | PLAT-02/13 | `make check-observability` | 是 |
| 密钥扫描 | PLAT-04 | `make secrets` | 是 |
| 依赖与许可证 | PLAT-10 | `make check-deps`、`make vuln` | 是 |
| 构建 | 全部 | `make build` | 是 |
| 冒烟 | PLAT-04/07/08 | `/healthz` `/readyz` `/version` `/metrics` + 三端鉴权用例 | 是 |

---

## 10. 风险与回滚

| 风险 | 影响 | 缓解 | 回滚 |
|---|---|---|---|
| JWT 接入改变身份语义 | 三端鉴权失败 | 保留 `mode=demo` 兼容；灰度单个 cmd 先行 | 切回 `mode=demo`（仅非 prod） |
| transport 签名变更 | 编译期破坏 | 一次性改完 4 个 router + cmd；不保留双签名 | `git revert` 单 PR |
| 限流默认值过严 | 误伤正常流量 | 默认关闭或保守值；按 channel 配置；先观察指标 | 关闭 `rate_limit.enabled` |
| `http.timeout` 过短 | 长任务被切断 | 大文件/SSE 路由单独放行；默认 30s | 调整配置并热重启 |
| platform 覆盖率门槛 | 短期阻塞合并 | 门槛与 PLAT-11 同 PR 合入，先补测再开门禁 | 临时下调须 Owner + ADR（禁止永久豁免） |
| 驱动按需注册后遗漏 import | 运行期 `Open` 失败 | 启动期 `Open` 即失败（fail fast）；示例配置注明 | 恢复全量 import |

---

## 11. 附：验收命令速查

```bash
# 构建与静态检查
make fmt-check && make lint && go vet ./...

# platform 单测与覆盖率
go test ./internal/platform/... -race -count=1 \
  -coverprofile=coverage-platform.out
go tool cover -func=coverage-platform.out | tail -1

# 分层断言（PLAT-06/12）
if go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/... \
  | grep '/transport' | grep -q 'internal/platform'; then
  echo "ERROR: transport 仍依赖 internal/platform"; exit 1
fi
bash scripts/ci/check-layering.sh

# 门禁
make check-config check-reliability check-observability check-transport
make test-ci
make ci

# 冒烟（进程启动后）
curl -s -o /dev/null -w '%{http_code}\n' localhost:8081/healthz
curl -s -o /dev/null -w '%{http_code}\n' localhost:8081/readyz
curl -s localhost:8081/version
```

> Windows PowerShell 下 `make` 等价命令：`go test ./...`、`go vet ./...`、`gofmt -l .`；`scripts/ci/*.sh` 需在 Git Bash / WSL 中执行，或使用 `make gitee-job JOB=arch`。

---

## 12. 执行记录

| 日期 | 任务 | 状态 | PR | 备注 |
|---|---|---|---|---|
| 2026-10-20 | 计划创建 | — | — | 基于当前 `main`（`95846ff`）现状盘点 |
| 2026-10-20 | PLAT-01 | 已完成 | 本地 | `cmd/*` 统一 `run() error`，`os.Exit` 仅在 `main`；`platform.Run` 负责资源释放 |
| 2026-10-20 | PLAT-02 | 已完成 | 本地 | `internal/platform/httpserver/server.go`：Timeouts/MaxHeaderBytes/ErrorLog/BaseContext；先 `net.Listen` 后打印 listening |
| 2026-10-20 | PLAT-03 | 已完成 | 本地 | `log.format` 校验；`errors.Join` 聚合；`Config/DBConfig/StorageConfig.LogValue` 脱敏；新增 `http.*` 校验 |
| 2026-10-20 | PLAT-04 | 已完成 | 本地 | 新增 `internal/security`：DemoAuth + JWT（HS256/RS256、iss/aud/exp/leeway、channel 声明校验）；`auth.mode=demo\|jwt` 为唯一开关，已移除 `demo_mode` 兼容 |
| 2026-10-20 | PLAT-05 | 已完成 | 本地 | 删除 `defaultRBAC`/`RequirePermission`；`security.Authorizer` 由 cmd 注入 `rbac.Checker` |
| 2026-10-20 | PLAT-06 | 已完成 | 本地 | `httpx.TraceIDFromContext`；`auth.PermissionMiddleware` 注入 transport；删除 platform 别名 |
| 2026-10-20 | PLAT-07 | 已完成 | 本地 | `platform.NewPipeline`：Recover→Trace→RequestID→Metrics→AccessLog→CORS→Timeout→BodyLimit→RateLimit；42900 + Retry-After |
| 2026-10-20 | PLAT-08 | 已完成 | 本地 | `platform.Run` 统一装配；cmd/admin-api 拆 `main.go` + `register.go`，三端 API 均 ≤ 45 行 |
| 2026-10-20 | PLAT-09 | 已完成 | 本地 | `BucketConfig = StorageConfig` 类型别名 + 单一 `ToStorageConfig`；删除 `storage_set.go` 手写映射；禁止嵌套 buckets |
| 2026-10-20 | PLAT-10 | 已完成 | 本地 | 新增 `pkg/storage/all`；删除 `platform/storage_drivers.go`；由 cmd 显式 import（可按需裁剪） |
| 2026-10-20 | PLAT-11 | 已完成 | 本地 | 新增 config/pipeline/endpoints/server/bootstrap/security 测试；platform 覆盖率 **81.4%**；`internal/security` 补齐 JWT 密钥格式/轮换/过期/非法 PEM 与 demo token 用例，覆盖率 **96.0%** |
| 2026-10-20 | PLAT-12 | 已完成 | 本地 | 新增 `scripts/ci/check-layering.sh`；`check-test-gates.sh` 增加 platform（`TEST_PLATFORM_COVER_MIN`）与 security（`TEST_SECURITY_COVER_MIN`）覆盖率门禁；Makefile/CI 接入 |
| 2026-10-20 | PLAT-13 | 已完成 | 本地 | `wiring.go` → `endpoints.go`；`check-observability.sh`、`check-release.sh`（RELS-03）路径断言同步更新；ARCHITECTURE §5.1 示例改为 `platform.Run` |
| 2026-10-20 | PLAT-14 | 已完成 | 本地 | `NewConfigLoader` 标注为预留；CONFIG_STANDARDS §9 + ARCHITECTURE §5.6 明确热更新白名单与 ADR 前置 |
| 2026-10-20 | PLAT-15 | 已完成 | 本地 | ARCHITECTURE §5.6 写入拆分触发条件与目标形态 |

### 12.1 实施偏差与决策

| 任务 | 计划 | 实际 | 原因 |
|---|---|---|---|
| PLAT-04 | `auth.mode` 为唯一开关，`demo_mode` 作为过渡兼容（`mode` 优先） | 直接移除 `demo_mode` 与 `ADMIN_FRAME_AUTH_DEMO_MODE`，不保留兼容；未声明 mode 时 prod/staging → `jwt`（缺配置即失败），其余环境 → `demo` | 避免两套开关并存导致生产误判；本地最小配置仍可启动 |
| PLAT-07 | `http.hard_timeout` 硬切断 | 仅实现软超时（`context` deadline） | stdlib `TimeoutHandler` 返回固定文本且与流式/大文件响应冲突；软超时是 Go 生态推荐做法 |
| PLAT-09 | `BucketConfig` 内嵌 `storageConn` | `type BucketConfig = StorageConfig` | 类型别名同样消除重复映射，且保持既有配置字面量与公开 API 不变 |
| PLAT-10 | 未使用驱动的 cmd 不链接云 SDK | `pkg/*` 与 `cmd/migrate` 已不链接；API/worker 显式 import `storage/all` | API 需支持运行时多云切换；需要裁剪时改为单一驱动 import 即可 |
| PLAT-12 | depguard | 自定义 `check-layering.sh` + 覆盖率门禁 | 未在本机验证 golangci-lint v2 depguard schema，避免错误配置直接卡死 CI |

### 12.2 验收执行结果（2026-10-20，Windows 宿主 + WSL Ubuntu 24.04）

在 WSL（Ubuntu 24.04 / go1.26.9 / gcc 可用 / `CGO_ENABLED=1`）执行完整 `make ci`，退出码 **0**，全部门禁通过：

| 验收命令 | 结果 | 说明 |
|---|---|---|
| `make ci`（WSL） | **通过** | 23 个 target 全部通过，无 WARN；含 `-race`、`golangci-lint`、`govulncheck`、`oasdiff`、`gitleaks`（45 commits / no leaks） |
| `gofmt -l .` / `make fmt-check` | 通过 | 无输出 |
| `make tidy-check` | 通过 | `go.mod` 幂等 |
| `go build ./...` / `make build` | 通过 | 5 个二进制均构建成功（`bin/`） |
| `go vet ./...` | 通过 | 无输出 |
| `go test -race -count=1 ./...` | 通过 | 全仓绿；platform **81.4%**、security **96.0%**、domain **100%** |
| `golangci-lint run ./...`（v2.14.0） | 通过 | 0 issue；本轮修复 gocritic `httpNoBody` ×3、gosec G101 ×2、G304 ×1、revive `unused-parameter` ×1、revive `blank-imports` ×1 |
| `govulncheck ./...` | 通过 | 代码路径 0 漏洞（依赖中 1 个未被调用的漏洞，不阻断） |
| `check-layering` / `check-transport-imports` | 通过 | LAYER-01 ~ LAYER-04 |
| `check-config` / `check-reliability` / `check-observability` | 通过 | REL-02 断言 `ReadTimeout` / `IdleTimeout`；OBS 断言 `bootstrap.go` / `endpoints.go` |
| `check-test-gates` | 通过 | domain / platform / security 覆盖率门禁 + 必测场景（含 `-race`） |
| `check-debug-endpoints` / `check-codegen` / `check-migrations` / `check-license` / `check-runbooks` / `check-audit` / `check-git` / `check-release` | 通过 | — |
| `check-openapi` | 通过 | 三端 validate 无 findings；对 `main` 无 breaking |
| `check-perf` | 通过 | `BenchmarkNewOrder`、`BenchmarkVerifierVerify` 可运行 |
| `gitleaks v8.24.2` | 通过 | 无密钥泄露 |

> 说明：Windows 宿主侧因 `CGO_ENABLED=0`、无 `golangci-lint`、离线无法拉取 `oasdiff` 等模块，故 `-race` / lint / openapi 只能在 WSL 或 CI 执行；上表 WSL 结果即为最终验收结论。
