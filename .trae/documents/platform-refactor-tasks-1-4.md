# 平台边界整改：任务 1～4 实施计划

## Context

阶段 1～3（commit `924bd4c`）已完成平台基础子包拆分、`bootstrap.Run` 显式装配与守卫端口下沉。剩余四项架构债务按 `docs/archive/reviews/platform-architecture-review.md` 第十节执行：

1. `pkg/httpx` 与 `internal/shared/httpx` 同名双包，调用方需二选一，契约易混。
2. `Config.secretErr` 用隐状态弥补 `pkg/config.Normalize` 钩子不支持返回错误，密钥解析失败被延后到 `Validate` 才暴露。
3. `dependency.Registry` 单文件承担登记 / 熔断构造 / 超时 / 重试 / 观测五种职责；`pkg/httpclient` 另有独立重试入口与指标口径；配置测试散落在 `internal/platform` 根。
4. 域配置类型已下沉，但默认值 / 校验仍在平台层，`platform` 顶层与 `config` 包保留域别名；用户选择方案 **C**：域配置完全自持，且从 `platform.Config` 聚合根移除。

约束：行为不变、门禁只减不增、未经用户确认不提交。

---

## 任务 1：收敛 httpx 双包（改名 `apiresp`）

**方案**：保留 `pkg/httpx`（可移植原语），把 apperr 适配层 `internal/shared/httpx` 更名为 `internal/shared/apiresp`（package `apiresp`）。`internal/` 包无外部兼容义务，直接改全量引用，不留转发别名。

步骤：

1. `git mv internal/shared/httpx internal/shared/apiresp`；四个文件（`doc.go`、`response.go`、`trace.go`、`response_test.go`）包声明改为 `apiresp` / `apiresp_test`，doc 改为说明「apperr → 统一响应信封适配层，原语见 pkg/httpx」。
2. 全仓 Go 文件替换 import 路径 `internal/shared/httpx` → `internal/shared/apiresp`，并把引用该包的文件中标识符 `httpx.` → `apiresp.`。必须用带边界的替换（负向断言），避免误伤 `pkghttpx.` / `sharedhttpx.`；`notification`、`featureflag` 两处显式别名 `sharedhttpx` 一并去掉。
3. 代表性调用点：[pipeline.go](file:///d:/MyDocument/admin-frame/internal/platform/httpserver/pipeline.go)、[identity c handler](file:///d:/MyDocument/admin-frame/internal/identity/transport/c/handler.go)、[iam handler](file:///d:/MyDocument/admin-frame/internal/iam/transport/rest/handler.go)。
4. 更新文档引用：`docs/API_STANDARDS.md`、`docs/pkg-roadmap.md`、`docs/rbac-iam.md`、`docs/archive/reviews/platform-architecture-review.md`（仅替换路径，不改语义）。

验证：`go build ./...`、`go test ./internal/shared/apiresp ./internal/platform/httpserver ./internal/identity/... ./internal/iam/...`、`bash scripts/ci/check-layering.sh`。

---

## 任务 2：消除 `Config.secretErr`

步骤：

1. `pkg/config/loader.go`：`Options.Normalize` 签名改为 `func(*T) error`；`Load` 中 `return nil, normalizeError(err)`。同步注释（钩子现在可返回错误，仍禁止 IO 之外的长耗时操作）。
2. `pkg/config/errors.go` 新增 `normalizeError(err) error`，包装为 `ErrInvalidConfig` 且保留 `%w` 链（`errors.Is` 可同时命中哨兵与根因）。
3. `internal/platform/config/config.go`：删除 `secretErr` 字段及其注释。
4. `internal/platform/config/defaults.go`：`(*Config).normalize` 改为返回 `error`，末尾 `return c.resolveSecrets()`。
5. `internal/platform/config/secrets.go`：`resolveSecrets` 直接返回 `error`，不再写入字段；删除所有“由 Validate 汇总”的注释。
6. `internal/platform/config/validate.go`：删除 `secretErr` 前置聚合块。
7. `pkg/config/loader_test.go` 的 Normalize 探针返回 `nil`；新增用例：Normalize 返回错误时 `Load` 失败、`errors.Is(err, ErrInvalidConfig)` 且根因可追溯。
8. `internal/platform/config_secrets_test.go` 的注释/用例名从「Validate 汇总」改为「Load 直接失败」（断言不变）。

验证：`go test ./pkg/config/... ./internal/platform/...`、`go build ./...`。

---

## 任务 3：依赖注册表职责收敛 + 配置测试迁移

### 3.1 Registry 拆分与 HTTP 并轨

现状：`registry.go` 单文件 220 行；全仓无生产 `Register` 调用，`pkg/httpclient` 自建重试链，指标口径无法统一。

**设计（保留行为，不改 pkg 语义）**：

- 拆分 `internal/platform/dependency`：
  - `spec.go`：`Caller` / `BreakerSpec` / `Spec` / `Observer`；
  - `registry.go`：`Registry` 仅保留 `NewRegistry` / `Register` / `Get` / `Names` 与并发保护；
  - `caller.go`：执行链路（超时 → 熔断 → 重试 → 观测），逻辑逐行保持不变；
  - `breaker.go`：`circuitbreaker.New` 构造与默认值；
  - `http.go`：HTTP 依赖适配。
- `pkg/httpclient` 增加可选观测端口（不改默认行为）：
  - `Config.Observer`，接口 `AttemptObserved(statusCode int, err error)`；
  - `retryTransport` 在每次尝试结束回调（含最终失败）；未设置时零开销。
- `dependency.HTTP(spec Spec, cfg httpclient.Config) (*HTTPCaller, error)`：
  - `spec.Retry` 映射为 `cfg.Retry`、`spec.Timeout` 映射为 `cfg.Timeout`，唯一策略来源；
  - `cfg.Observer` 由 `Registry.observer` 适配（调用 `CallObserved` / `RetryObserved`），指标口径与 `Caller.Do` 一致；
  - `spec.Breaker` 通过一个包内 `breakerTransport` 包在 `cfg.Transport` 上（每次 HTTP 尝试计入熔断）；
  - `HTTPCaller.Do(req)` 复用 `httpclient.Client` 的请求克隆、`Retry-After`、非幂等判定与 `StatusError` 语义，不重复实现。
- 迁移一个内部调用点验证并轨：[saml/metadata.go](file:///d:/MyDocument/admin-frame/internal/identity/infrastructure/saml/metadata.go) 支持注入客户端（默认仍走 `httpclient.New`）。`secrets/vault.go` 在配置解析阶段执行、`pkg/sms`、`pkg/push` 受 import 方向约束，保持直连，并在包注释登记例外与后续迁移条件。

测试：`dependency` 新增 HTTP 用例（状态码重试、`Retry-After`、指标回调次数、熔断打开、非幂等不重试）；`pkg/httpclient` 新增 Observer 回调用例；原 `registry_test.go` 全部保持通过。

### 3.2 配置测试迁移

- 把 `internal/platform` 根 11 个 `config_*_test.go`（含 `config_test.go`）移入 `internal/platform/config`，package 改为 `config_test`；`platform.X` 改为 `config.X`（域符号见任务 4）。
- `writeConfig` 等仅测试用 helper：在 `internal/platform/config` 测试内保留一份；`bootstrap_test.go` 仍需要的 helper 复制到 `internal/platform/testsupport_test.go`（测试代码允许少量重复）。
- `config_test.go` 中同时覆盖核心段与域段的用例：任务 3 先整体迁移，任务 4 再按域拆分到各域包。

验证：`go test ./internal/platform/...`、`bash scripts/ci/check-layering.sh`（根目录非测试文件数不受影响）。

---

## 任务 4（方案 C）：域配置完全自持 + 拆出聚合 + 删除别名

### 4.1 域默认值 / 校验下沉

- `internal/identity/config`：新增 `defaults.go`、`validate.go`，迁入 `applyIdentityDefaults` / `applySAMLDefaults` / `identity_validate.go` / `identity_social_validate.go` / `identity_limits.go` 的域内逻辑。
- `internal/iam/config`：新增 `defaults.go`、`validate.go`，迁入 `applyIAMDefaults` 与 `validateIAM`。
- `internal/audit/config`：新增 `defaults.go`、`validate.go`，迁入 `applyAuditDefaults` 与 `validateAudit`。
- `internal/tenant/config`：现状无默认值/校验，补齐 `ApplyDefaults`（若有字段需要）与校验入口（无则不加）。
- 跨段依赖参数化（域包不得 import platform）：
  - `IdentityConfig.Validate(env, dbDriver string, redisEnabled, asyncEnabled bool) error`
  - `IAMConfig.Validate(env, dbDriver string, redisEnabled, asyncEnabled bool, channel string) error`
  - `AuditConfig.Validate(env, dbDriver, storageDriver string) error`
  - 默认值方法 `ApplyDefaults()` 仅在确实依赖跨段时接收参数。
- `RateLimitFailureClosed/Open`：identity 域包内定义同名常量（值一致），平台 `RateLimit` 段继续使用原常量；不新增中立包。
- 样例密钥拒绝：`identity.hash_key` / `identity.mfa.secret_key` / `identity.subject.keys` 的检查随身份校验下沉；`auth.jwt` / `redis` / `s3` 检查留在平台核心。错误文案与顺序保持。

### 4.2 拆分 `platform.Config` 聚合

- `internal/platform/config/config.go` 删除 `IAM` / `Identity` / `Audit` / `Tenant` 四个字段；平台 `Validate` / `applyDefaults` 只处理核心段。
- 新增 `cmd/internal/appconfig`：
  ```go
  type Config struct {
      platformconfig.Config `yaml:",inline"`
      Identity identityconfig.IdentityConfig `yaml:"identity"`
      IAM      iamconfig.IAMConfig           `yaml:"iam"`
      Audit    auditconfig.AuditConfig       `yaml:"audit"`
      Tenant   tenantconfig.TenantConfig     `yaml:"tenant"`
  }
  ```
  使用 `pkg/config.Loader[Config]`；`ApplyDefaults` / `Normalize` / `Validate` 依次调用平台核心与四个域包，校验顺序保持 IAM → Identity → Audit。
- 平台配置钩子改为导出方法（`ApplyDefaults`、`Normalize`）供组合调用；`LoadConfig` / `NewConfigLoader` 保留。
- `internal/platform.Options` 增加 `Config *Config`（优先使用）；`ConfigPath` 保留为兼容路径（为零值时走 `LoadConfig`），避免 migrate/测试破坏。cmd 入口（`admin-api`、`biz-api`、`c-api`、`worker`）改为先加载 `appconfig.Config` 再传给 `platform.Run`。
- YAML 加载兼容性已验证：`pkg/config` 的 `BindEnv` 明确支持匿名嵌入内联（`inlineField`），`yaml.v3` 使用 `,inline` 展开，include/覆盖语义不变。

### 4.3 域 runtime 自持配置

- 构造函数改为显式接收域配置：`identityruntime.New(deps, cfg.Identity)`、`iamruntime.New(deps, cfg.IAM)`、`tenantruntime.New(deps, cfg.Tenant)`、`auditruntime.New(deps, cfg.Audit, cfg.DB.Driver)`；内部把 `deps.Config.X` 改为结构体字段 `cfg`。
- 调用点集中在 `cmd/{admin,biz,c,worker}-api` 的 `newIAMRuntime` / `newIdentityRuntime` / `registerRoutes` 等装配文件；逐处传入闭包捕获的 `appCfg`。
- 平台生产代码不读取域配置（已确认），因此 `Deps` 不新增域字段，避免平台反向依赖。

### 4.4 删除兼容别名

- 删除 `internal/platform/config/domain_aliases.go`。
- 删除 `internal/platform/aliases.go` 中域配置类型、域常量转发；保留非域别名（storage / httpserver / async）与守卫薄包装。
- 全量替换引用：`platform.IdentityConfig` / `IAMConfig` / `AuditConfig` / `TenantConfig`、`platform.IAMMode*` / `IAMCacheDriver*` / `AuditStore*` / `AuditMinRetentionDays` / `Identity*Driver` / `RateLimitFailure*` 等，改指各域包；代表性位置：`cmd/internal/iamruntime`、`cmd/internal/auditruntime`、`cmd/internal/identityruntime` 及其测试。

### 4.5 测试与文档

- 域相关配置测试按归属拆分：`config_identity*` / `config_mfa` / `config_saml` / `config_sms_oauth` → `internal/identity/config`；`config_iam*` → `internal/iam/config`；`config_audit*` → `internal/audit/config`；`config_secrets` / `config_multiinstance` / 核心段 → `internal/platform/config`。
- 更新 `docs/adr/0011-platform-boundary-convergence.md` 与 `docs/archive/reviews/platform-architecture-review.md` 第十节状态表。

---

## 验证（每个任务后执行，全部完成后复跑）

```bash
go build ./...
go test ./pkg/config/... ./pkg/httpclient/... ./internal/platform/... ./internal/identity/... ./internal/iam/... ./internal/audit/... ./internal/tenant/... ./cmd/...
bash scripts/ci/check-layering.sh
make check-config
make check-deps
make check-test-gates
```

基线只减不增：`check-layering.sh` 的 `PLATFORM_MAX_FILES` / `PLATFORM_MAX_FILE_LINES` 不调整；如文件数下降，同步下调基线。

## 风险与回滚

- 任务 4 为内部 API 破坏性变更，但所有调用方在本仓内；按 4.1 → 4.2 → 4.3 → 4.4 顺序小步提交式推进，每步保持可编译。
- `pkg/config` 严格解码 + 环境变量绑定是组合加载的最大风险点，先用 `appconfig` 单测覆盖 `include` 覆盖、`ADMIN_FRAME_IDENTITY_*` 注入、未知字段拒绝。
- `pkg/httpclient` 行为不变由现有测试守护；Registry HTTP 适配为新增路径，不替换现有直连调用。
- 回滚：按任务粒度回退文件；不涉及数据库/配置格式变更。
