# ENT-02 多实例运行语义实施计划

## Context

Wave 0（集成基座 + 门禁骨架）已提交（`df422e3`）。当前 `docs/production-readiness-implementation-plan.md` 中 ENT-02 仍为「待做」，实际缺口：

- `cmd/internal/iamruntime/runtime.go:103` 固定 `NodeID: 1`，多副本会生成重复 ID；`internal/iam/app` 四个用例构造器在未注入 `IDs` 时也回退到固定节点号。
- `internal/platform/pipeline.go` 的限流只使用进程内 `ratelimit.Keyed`，多副本配额被放大；`pkg/idempotency` 已有 Store/中间件但没有平台级挂载点，也没有 Redis 实现。
- IAM Outbox 发布器只在 API 进程内运行，缺少 `publish_in_api` 开关与 worker 侧独立发布入口，API/worker 无法按迁移节奏切换。

本计划对应 ENT-02 的 T1/T2/T3/T5/T6/T7/T8，目标：两副本 ID 不冲突、幂等跨副本、限流全局共享、后台发布与 API 隔离。

**范围说明（T4）**：三端 OpenAPI 的 `Idempotency-Key` 参数已在 `api/openapi/components.yaml` 定义；C 端当前无写路由。平台级统一挂载幂等中间件即覆盖全部写路由，`check-idempotency` 校验挂载与配置契约。A/B 端逐 operation 标注作为 API 契约变更单独跟进（需评估客户端兼容），不在本次改动内。

## 设计决策

1. **ID**：`id.node_id` 用 `*int64` 区分「未配置」与显式 0；`id.node_id_source=config|env|statefulset`。`config`/`env` 语义相同（环境变量已由 `pkg/config` 合并到字段），`statefulset` 从 `HOSTNAME` 末尾 ordinal 派生；`registry` 本迭代不支持，启动即拒绝。prod/staging 非 worker 进程必须显式配置或使用 statefulset。生成器由 `platform.Run` 构造并注入 `Deps.IDs`，业务用例不再自带默认节点号。
2. **幂等**：新增 `pkg/idempotency.RedisStore`，Lua 原子完成 `Begin/Complete/Release`；记录以 JSON 存 Redis，TTL 即记录过期时间。`idempotency.driver` 默认 `memory`，prod/staging 启用时强制 `redis`。失败关闭：存储不可用返回 `50300`，指纹冲突返回 `40900`，处理中返回 `40901`（由 `httpx.FromError` 统一信封）。
3. **限流**：新增 `pkg/ratelimit.RedisKeyed`，Lua 令牌桶（Redis `TIME` 取服务端时钟，`HGET/HMSET/PEXPIRE`）。中间件消费侧改为接口 `Allow(key) Result`，同时兼容内存 `*ratelimit.Keyed` 与 Redis 实现。`failure_mode=fail_open|fail_closed` 默认 `fail_open`；超限继续统一 `42900`。分键策略 `channel|channel_user|channel_tenant_user|client`，默认 `channel`（有界、不会因匿名流量膨胀）。
4. **Outbox 隔离**：`iam.outbox.publish_in_api`（默认 true，保持现有行为）。API 始终写入 outbox；仅当开关为 true 时启动 API 内发布器。worker 新增数据库装配与独立发布循环，API/worker 共用同一段发布器构造代码，避免两处漂移。

## 文件级改动

### 1. 配置（T1）

- [internal/platform/config.go](file:///d:/MyDocument/admin-frame/internal/platform/config.go)
  - 新增 `IDConfig{NodeID *int64, NodeIDSource string}`、`IdempotencyConfig{Enabled, Driver, Header, TTL, Required, MaxBodyBytes}`。
  - `RateLimitConfig` 增加 `Driver`、`Key`、`FailureMode`。
  - `IAMOutboxConfig` 增加 `PublishInAPI *bool` 与 `PublishInAPIEnabled()`。
  - `applyDefaults`：`node_id_source=env`；幂等 `memory/Idempotency-Key/24h/1MiB`；限流 `memory/channel/fail_open`；`publish_in_api=true`。
  - `Config.LogValue` 增加 `id`、`idempotency` 与限流新字段（不含敏感值）。
  - 新增 `validateID`、`validateIdempotency`，扩展 `validateRateLimit`：
    - 枚举校验；`node_id` 范围 [0,1023]；statefulset 与显式 node_id 互斥。
    - prod/staging 非 worker：非 statefulset 时必须显式 `node_id`。
    - `driver=redis` 时要求 `redis.addr`；prod/staging 启用幂等或限流时强制 `driver=redis`。
- 新增 [internal/platform/id.go](file:///d:/MyDocument/admin-frame/internal/platform/id.go)：`NewIDGenerator(cfg IDConfig) (id.Generator, int64, error)` + 纯函数 `resolveNodeID(cfg, hostname)`，非法/缺失返回带字段名的错误，不 panic（S-04）。
- [configs/admin.yaml.example](file:///d:/MyDocument/admin-frame/configs/admin.yaml.example)、[configs/biz.yaml.example](file:///d:/MyDocument/admin-frame/configs/biz.yaml.example)、[configs/c.yaml.example](file:///d:/MyDocument/admin-frame/configs/c.yaml.example) 增加 `id`、`idempotency`、`rate_limit` 段；admin/biz 的 `iam.outbox` 增加 `publish_in_api`；[configs/worker.yaml.example](file:///d:/MyDocument/admin-frame/configs/worker.yaml.example) 增加 `iam.outbox` 段。

### 2. 装配（T1/T2）

- [internal/platform/bootstrap.go](file:///d:/MyDocument/admin-frame/internal/platform/bootstrap.go)：`Deps` 增加 `IDs id.Generator`；`Run` 在日志初始化后构造生成器，失败即返回；启动日志记录 `node_id`。
- [cmd/internal/iamruntime/runtime.go](file:///d:/MyDocument/admin-frame/cmd/internal/iamruntime/runtime.go)：删除 `id.MustSnowflake(NodeID:1)`，改为 `deps.IDs`，nil 返回错误；outbox 发布器注册受 `PublishInAPIEnabled()` 控制（写入 outbox 不受影响）。
- [internal/iam/app/role_service.go](file:///d:/MyDocument/admin-frame/internal/iam/app/role_service.go)、[binding_service.go](file:///d:/MyDocument/admin-frame/internal/iam/app/binding_service.go)、[change_request_service.go](file:///d:/MyDocument/admin-frame/internal/iam/app/change_request_service.go)、[breakglass_service.go](file:///d:/MyDocument/admin-frame/internal/iam/app/breakglass_service.go)：`IDs == nil` 改为返回 `apperr.Internal("iam id generator is required")`；[audit.go](file:///d:/MyDocument/admin-frame/internal/iam/app/audit.go) 移除固定 NodeID 回退（构造器已保证非 nil）。
- 更新未注入 `IDs` 的测试（`internal/iam/transport/{admin,biz}` 与 `internal/iam/app` 个别用例），统一注入测试用 `id.MustSnowflake` 或现有 `seqIDs`。

### 3. 幂等（T3）

- 新增 [pkg/idempotency/redis.go](file:///d:/MyDocument/admin-frame/pkg/idempotency/redis.go)：
  - `NewRedisStore(client *redis.Client) (*RedisStore, error)`，键 `idem:<storeKey>`，走 `Client.FullKey`。
  - `Begin`：无记录则写入 InProgress；同指纹 InProgress → `ErrInProgress`；Completed → 返回记录；异指纹 → `ErrConflict`。
  - `Complete`：仅 InProgress 且指纹一致转 Completed 并重置 TTL。
  - `Release`：仅 InProgress 且指纹一致删除。
  - Lua 脚本用 `redis.NewScript` 缓存，JSON 记录新增 `json` 标签。
- [pkg/idempotency/store.go](file:///d:/MyDocument/admin-frame/pkg/idempotency/store.go)：仅补 JSON 标签，接口不变。
- [internal/platform/pipeline.go](file:///d:/MyDocument/admin-frame/internal/platform/pipeline.go)：
  - `PipelineConfig` 增加 `Idempotency`；`PipelineDeps` 增加 `Redis *redis.Client`。
  - 新增 `newIdempotency`：按 driver 构造 Memory/Redis Store，`driver=redis` 且 Redis 为 nil 时返回错误。
  - 顺序：`RateLimit → Idempotency → Handler`；`OnError` 用 `httpx.FromError`。
- [internal/platform/bootstrap.go](file:///d:/MyDocument/admin-frame/internal/platform/bootstrap.go) 把 `cfg.Idempotency` 与 `redisClient` 传入管线。

### 4. 分布式限流（T5/T6）

- 新增 [pkg/ratelimit/redis.go](file:///d:/MyDocument/admin-frame/pkg/ratelimit/redis.go)：
  - `RedisKeyedConfig{RPS, Burst, IdleTTL, FailureMode, KeyPrefix}`、`NewRedisKeyed(*redis.Client, cfg)`。
  - `Allow(key)` / `AllowN(key,n)`；Lua 令牌桶，`RetryAfter` 由缺口令牌数/rps 计算；Redis 错误按 `failure_mode` 放行或拒绝。
- [pkg/middleware/ratelimit.go](file:///d:/MyDocument/admin-frame/pkg/middleware/ratelimit.go)：`RateLimitConfig.Keyed *ratelimit.Keyed` 改为接口字段 `Buckets`（`Allow(string) ratelimit.Result`），保留 `Limiter` 全局桶语义；`allow()` 优先 `Buckets`。
- [internal/platform/pipeline.go](file:///d:/MyDocument/admin-frame/internal/platform/pipeline.go)：`newRateLimit` 根据 driver 构造内存或 Redis 桶；新增 `rateLimitKeyFunc(strategy, channel)`（IP 取 `X-Forwarded-For` 首段，匿名时回退 IP）。
- 限流拒绝仍走 `apperr.TooManyRequests`（42900），保留 `Retry-After`。

### 5. worker Outbox（T7）

- 新增 [cmd/internal/iamruntime/outbox.go](file:///d:/MyDocument/admin-frame/cmd/internal/iamruntime/outbox.go)：导出 `NewOutboxTask(deps *platform.Deps) (func(context.Context) error, error)`，复用 `buildOutboxRepository`、`app.NewOutboxPublisher`、`app.AuditOutboxDeliverer(audit.SlogEmitter{...})`；要求 `deps.Database != nil`（worker 的内存 outbox 无共享语义）。
- [cmd/worker/main.go](file:///d:/MyDocument/admin-frame/cmd/worker/main.go)：按 `db` 配置打开数据库并关闭；`iam.outbox.enabled` 时构造任务并在 goroutine 中运行，退出时等待任务收敛（可取消、可汇合，S-09）；无任务时保留现有心跳。
- [internal/platform/config.go](file:///d:/MyDocument/admin-frame/internal/platform/config.go)：`iam.outbox.publish_in_api` 默认 true；文档说明迁移期先部署 worker，再关闭 API 侧发布。

### 6. 门禁与文档（T8）

- 新增 `scripts/ci/check-idempotency.sh`：
  1. `internal/platform/pipeline.go` 必须挂载 `idempotency.Middleware`；
  2. 配置样例必须包含 `id:`（含 `node_id_source`）与 `idempotency:` 段；
  3. `cmd/`、`internal/` 非测试 Go 代码禁止 `NodeID:\s*[0-9]` 固定节点号；
  4. `api/openapi/components.yaml` 必须定义 `Idempotency-Key`。
- [Makefile](file:///d:/MyDocument/admin-frame/Makefile)：新增 `check-idempotency`，加入 `ci` 聚合；[scripts/ci/gitee-job.sh](file:///d:/MyDocument/admin-frame/scripts/ci/gitee-job.sh) 的 `arch` job 调用。
- 新增 `tests/integration/multiinstance/multiinstance_test.go`：
  - 两个不同 NodeID 生成器批量生成，断言 ID 无交集、`id.Parse` 节点号正确；
  - 两个 `RedisStore` 实例：同 key 同指纹跨实例命中、异指纹 `ErrConflict`、Complete 后回放；
  - 两个 `RedisKeyed` 实例共享配额，超限 `Allowed=false`；
  - MySQL outbox：发布 → 模拟重启 → 再次发布，断言无丢失、无重复投递。
- 文档回写：[docs/CONFIG_STANDARDS.md](file:///d:/MyDocument/admin-frame/docs/CONFIG_STANDARDS.md)（新字段、环境变量、生产强制项）、`docs/production-readiness-implementation-plan.md` 与 `docs/production-readiness-gaps.md` 的 ENT-02 状态与 AC 勾选。

## 验证

1. `make fmt-check`、`go build ./...`、`go test ./...`（含新增单测：`internal/platform` 配置/NodeID、`pkg/idempotency`、`pkg/ratelimit`、管线幂等挂载；Redis 单测优先用已有 `miniredis`，若 Lua `TIME` 不支持则下沉到集成用例）。
2. `golangci-lint run ./...`。
3. `bash scripts/ci/check-config.sh`、`check-reliability.sh`、`check-idempotency.sh`。
4. Docker 可用时 `make test-integration`（MySQL/Redis/MinIO；新增 multiinstance 用例）；不可用则记录跳过原因并至少跑 `go test ./tests/integration/...` 确认按环境变量正确 skip。
5. 手工冒烟：以两份 admin 配置（node_id 1/2、`rate_limit.driver/redis`、`idempotency.driver/redis`）启动，重复 `Idempotency-Key` 请求返回同一响应；超出 `burst` 返回 42900；`iam.outbox.publish_in_api=false` 时 API 不投递、worker 投递。

## 风险与回滚

- 生产开启 `driver=redis` 后 Redis 故障：限流按 `failure_mode` 处理（默认放行），幂等失败关闭（`50300`），可通过配置回退 `memory` 单副本运行。
- NodeID 变更必须成对操作，文档注明窗口内禁止同时重启全部副本；statefulset 来源依赖 `HOSTNAME` 后缀，非 K8s 环境启动会失败而非静默复用节点号。
- API 与 worker 同时发布会产生重复投递风险：迁移期由 `publish_in_api` 单边控制，worker 就绪后关闭 API 侧。
- 本次不逐条标注 A/B OpenAPI 写 operation，作为独立契约变更跟进，避免未经客户端协调的强制头导致破坏性升级。
