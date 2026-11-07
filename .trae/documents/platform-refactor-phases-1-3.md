# internal/platform 整改计划（阶段 1～3）

## Context

`docs/archive/reviews/platform-architecture-review.md` 的结论是：`internal/platform` 运行时可靠性达标，但配置层（`config.go` 3228 行、47 个域结构体）和装配层（`bootstrap.go` 386 行 `Run` + 5 个回调）违反 ARCHITECTURE §5.6（PLAT-15）与 S-11；域配置与租户/配额端口滞留平台层（P0-2、P1-4）。本次按报告第六节执行阶段 1～3：

- 阶段 1：拆文件（`config` 按域拆分、`Run` 拆为命名步骤），不改导出符号；
- 阶段 2：拆包（`config`/`observability`/`httpserver`/`authn`/`async`/`storage`），顶层保留别名与薄封装，`cmd/*` 零改动；
- 阶段 3：域配置下沉、守卫端口下沉、装配顺序显式化（破坏性，先写 ADR）；
- 门禁基线只减不增；不主动提交。

已确认的关键事实：

- 顶层 21 个非测试文件；`config.go` 3228 行、`bootstrap.go` 386 行、`async_nats.go` 576 行。
- 顶层测试全部是 `package platform_test`（`server_internal_test.go`、`database_internal_test.go`、`id_test.go`、`background_test.go` 为内部包测试）。
- 外部（`cmd/`、`tests/`、顶层测试）引用到的 platform 符号已枚举：`Deps`、`Options`、`Run`、`Config`、`LoadConfig`、`LoadDBConfig`、`NewLogger`、`OpenDatabase`、`OpenRedis`、`NewIDGenerator`、`OpenStorageSet`、`OpenStorage`、`ValidateStorage`、`NewAsyncRuntime`、`NewAsyncStore`、`NewAsyncMetrics`、`NewNATSBroker`、`NATSBrokerOptions`、`NewAuthMiddleware`、`NewPipeline`、`Chain`、`RequestID`、`Mount*`、`RegisterDBMetrics`、`WithTenantGuard`、`WithQuotaGuard`、全部域配置类型与模式常量。保留别名即可保证 `cmd/*` 与现有测试不改一行。
- `goimports`、`gofmt`、`bash` 可用；模块 Go 1.26.10。

## 目标结构

```text
internal/platform/
├── doc.go
├── aliases.go          # 类型别名 + 常量转发 + 薄封装（兼容层）
├── bootstrap.go        # Run 编排（命名步骤）
├── database.go         # OpenDatabase / OpenRedis（基础设施适配）
├── id.go               # NewIDGenerator
├── config/             # 按域拆分，单文件 ≤ 500 行
├── observability/      # logger / tracing(options) / db_metrics
├── httpserver/         # server / endpoints / pipeline / middleware
├── authn/              # NewAuthMiddleware
├── async/              # AsyncRuntime / metrics / nats
├── storage/            # StorageConfig / StorageSet
└── secrets/            # 保持现状
```

## 阶段 1：拆文件（不改 API）

1. 新建 `internal/platform/config/`，把 `config.go`（1～3375，按 `type` 声明行切分）与 `config_secrets.go` 机械拆为：
   - `config.go`（1-94：根 `Config` + 核心常量）
   - `audit.go`（95-156 + 177-207）、`tenant.go`（157-176）
   - `identity.go`（208-787）、`runtime_core.go`（788-864）
   - `async_config.go`（865-924）、`redis.go`（925-977）、`iam.go`（978-1082）
   - `http_config.go`（1083-1191）、`db_worker.go`（1192-1340）
   - `helpers.go`（1341-1448）、`defaults.go`（1449-1899）
   - `validate.go`（1900-2147）、`validate_infra.go`（2148-2460）
   - `identity_validate.go`（2461-2847）、`identity_social_validate.go`（2848-3183）
   - `saml.go`（3184-3264）、`async_validate.go`（3265-3375）
   - `secrets.go`（原 `config_secrets.go`）
   - 每个文件先复制原 import 块，`goimports -w` 自动裁剪；包名改为 `config`。
2. `TracingOptions`（`tracing.go`）作为 `(*Config)` 方法一并迁入 `config` 包，避免方法随类型丢失。
3. 顶层新建 `aliases.go`：`type X = config.X`、`const X = config.X`、`func LoadConfig/LoadDBConfig/NewConfigLoader` 转发；删除顶层 `config.go`、`config_secrets.go`。
4. `bootstrap.go` 的 `Run` 拆为命名步骤：`prepareInfra`（配置/日志/指标/追踪/存储/DB/Redis/健康检查）、`buildAsync`、`buildAuthN`（守卫 + 撤销检查 + AuthMW）、`buildAuthorizer`、`buildRouter`、`serve`；`Run` 仅顺序调用，行为与错误文案不变，每个顺序约束写成步骤内前置检查/注释。

## 阶段 2：拆包（保持导出兼容）

按目标结构移动文件，顶层仅保留 `doc.go`、`aliases.go`、`bootstrap.go`、`database.go`、`id.go`（5 个非测试文件）：

- `observability`：`logger.go`（`NewLogger(*config.Config)`）、`db_metrics.go`；`tracing.go` 的 `TracingOptions` 已在阶段 1 迁入 config。
- `httpserver`：`server.go`、`endpoints.go`、`pipeline.go`、`middleware.go`；`PipelineConfig` 引用 `config.RateLimitConfig/IdempotencyConfig/CORSConfig`；同步移动 `server_internal_test.go`（改为 `package httpserver`）。
- `authn`：`authn.go`（`NewAuthMiddleware(config.AuthConfig, ...)`）。
- `async`：`async.go`、`async_metrics.go`、`async_nats.go`；为避免 `async → platform` 循环，`NewAsyncRuntime` 改收 `async.Deps{Config, Logger, Database, Metrics}`，顶层 `aliases.go` 提供 `NewAsyncRuntime(*platform.Deps)` 薄封装转译。
- `storage`：`storage.go`、`storage_set.go`；`config.Config.Storage` 字段类型改为 `storage.Config`（字段名不变，`cfg.Storage.Xxx` 调用不变）；`defaults/validate` 调用 `storage.ValidateStorage`。
- 顶层 `aliases.go` 补齐对方才枚举到的全部导出符号；`bootstrap.go` 仅调整 import，不改变逻辑。
- 同步更新 `scripts/ci/check-layering.sh`：`PLATFORM_MAX_FILES=5`，清空 `LAYER_LINE_BASELINE`。

## 阶段 3：域配置下沉 + 显式装配（破坏性，先写 ADR）

1. 新增 ADR：`docs/adr/`（按现有编号顺延）记录域配置归属、`deps.Config` 迁移路径、守卫端口下沉、装配顺序声明；说明 `platform` 类型别名作为一版兼容层保留。
2. 新建域配置包并迁入结构体与域方法：
   - `internal/identity/config`：`Identity*` 全部类型 + `DecodeKeys` / `DecodeSecretKey` / `DecodeHashKeys`；
   - `internal/iam/config`：`IAM*` + `Enabled` / `IsEnabled`；
   - `internal/audit/config`：`Audit*` + `EffectiveStore`；
   - `internal/tenant/config`：`Tenant*`。
   `platform/config.Config` 字段改为对应域类型（字段名不变），默认值/归一化/校验仍留在 `platform/config`（报告允许的「加载+绑定+编排校验」）。
3. 守卫端口下沉：新建 `internal/tenant/app/ports.go` 定义 `GuardPort` / `MembershipPort`（`tenant/app.Guard` 已满足 `Verify`）；删除 `platform/tenantguard.go`、`platform/quotaguard.go` 的 interface 定义。
   - `platform.Deps` 删除 `TenantGuard` / `QuotaGuard` / `TenantMembership` 字段；
   - `platform.Options` 以 `NewAuthGuards func(*Deps) ([]func(http.Handler) http.Handler, error)` 取代 `NewTenantGuard` / `NewQuotaGuard`，由 `buildAuthN` 按序包裹 AuthMW（顺序即显式装配）；
   - `cmd/internal/tenantruntime`：`NewGuard` / `NewQuotaGuard` 改为返回中间件（内部直接调用 `tenantapp.Guard.Verify` / 配额执行器）；`InjectMembership` 改为写入通用函数字段 `Deps.IsTenantMember func(ctx, tenantID, subjectID string) (bool, error)`（identityruntime 适配层改用该函数，不再依赖平台命名接口）；
   - `cmd/biz-api/main.go` 适配新 `Options`。
4. 显式装配顺序：`Run` 各步骤返回前置条件错误（例如 `buildAuthN` 要求 `deps.Config`、`deps.Logger` 已就绪），移除「注释约定」。
5. 门禁：`LAYER_SEMANTIC_BASELINE` 清空；`PLATFORM_MAX_FILES` 保持 5；回写 `docs/archive/reviews/platform-architecture-review.md` 状态表。

## 关键文件

- 修改：`internal/platform/bootstrap.go`、`internal/platform/{config,storage,async,server,pipeline,middleware,endpoints,logger,db_metrics,authn}*.go`、`scripts/ci/check-layering.sh`、`cmd/biz-api/main.go`、`cmd/internal/tenantruntime/{runtime.go,quotaguard.go}`、`cmd/internal/identityruntime/runtime.go`
- 新增：`internal/platform/{config,observability,httpserver,authn,async,storage}/`、`internal/{identity,iam,audit,tenant}/config/`、`internal/tenant/app/ports.go`、ADR
- 保留：`internal/platform/{doc.go,aliases.go,database.go,id.go}`

## 验证

1. 每步 `goimports -w` + `gofmt -l`。
2. 阶段 1/2 每步：`go build ./...`、`go test ./internal/platform/...`、`bash scripts/ci/check-layering.sh`。
3. 阶段 3 后：`go build ./...`、`go test ./...`、`make check-config check-outbox check-resilience check-layering`，必要时 `make test-ci`。
4. 验收：`check-layering.sh` 无 `BASELINE` 行；`git diff` 阶段 1/2 无导出符号变更（仅新增别名/子包）；`cmd/*` 在阶段 2 无编译改动。
