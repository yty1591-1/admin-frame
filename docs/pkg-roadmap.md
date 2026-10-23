# pkg 公共能力包路线图

> **目的**：列出企业级项目中 `pkg/` 层常见可复用能力，按本仓库（模块化单体 + 多 cmd）优先级逐步实现。
> **原则**：只放与业务域无关、可被多个 `cmd`/模块复用、未来可拷到其他项目的纯技术能力。
> **对照**：业务约定放 `internal/shared`；进程装配放 `internal/platform`。
> **状态**：规划中
> **最后更新**：2026-10-21（`pkg/clock`/`timeutil`/`circuitbreaker`/`convert`/`decimal`/`money`/`webhook`/`config` 已落地；通知渠道见 [`notification-channels.md`](./notification-channels.md)；RBAC 权限管理服务规划见 [`rbac-iam.md`](./rbac-iam.md)）

---

## 一、放置边界

| 放 `pkg/` | 放 `internal/shared` | 放 `internal/platform` |
|-----------|----------------------|------------------------|
| 与具体业务无关、可跨仓库复用 | 仅本仓库用的约定与类型 | 进程启动与依赖接线 |
| 如：health、httpx、metrics、tracing、retry、ratelimit、id、jwt、crypto、db、redis、cache、mq、httpclient、logger、contextx、middleware、validator、pagination、idempotency、password、mask、rbac、storage | 如：apperr、httpx（薄封装）、auth.Principal | 如：config、server |

**经验法则**：换一个项目也能直接拷走 → `pkg`；一沾业务规则 → `internal`。

---

## 二、实现优先级（建议顺序）

按「多入口（admin/biz/c-api/worker）立刻用得上」排序。状态约定：`待做` / `进行中` / `已完成` / `暂缓` / `已落在其他层`。

| 优先级 | 包路径 | 说明 | 状态 | 备注 |
|-------|--------|------|------|------|
| P0 | `pkg/health` | 存活/就绪探针 | 已完成 | 已有实现 |
| P1 | `pkg/errors` | 统一错误类型、错误码、因果链 | 已完成 | `apperr` 薄封装业务数字码，底层挂接本包 |
| P1 | `pkg/httpx` | 统一响应体、状态码映射 | 已完成 | `internal/shared/httpx` 薄封装挂接 `apperr` |
| P1 | `pkg/metrics` | Prometheus 指标注册与常用指标 | 已完成 | HTTP RED + build_info；三端已挂 `/metrics` |
| P1 | `pkg/tracing` | OpenTelemetry 初始化与 span 工具 | 已完成 | W3C 传播；与 RequestID / 信封 trace_id 对齐 |
| P2 | `pkg/retry` | 重试、退避、可取消 | 已完成 | 默认挂接 `errors.RetryableOf`；`Permanent` 短路 |
| P2 | `pkg/ratelimit` | 令牌桶/滑动窗口 | 已完成 | 原语在 pkg；键策略 / 42900 映射留 platform |
| P2 | `pkg/id` | 分布式 ID（如 snowflake） | 已完成 | 订单创建已接入；多实例须唯一 NodeID |
| P2 | `pkg/jwt` | JWT 签发/校验、claims 解析 | 已完成 | HS256/RS256；业务鉴权仍在 platform |
| P2 | `pkg/crypto` | 加解密、签名、哈希、密钥包装 | 已完成 | AES-256-GCM / HMAC / Ed25519 / WrapKey；stdlib only |
| P3 | `pkg/db` | DB 连接池、事务 helper、健康检查 | 已完成 | database/sql；驱动由 platform 注册；无 migrate |
| P3 | `pkg/redis` | Redis 客户端、分布式锁、缓存封装 | 已完成 | go-redis；单实例锁；Cache-Aside KV |
| P3 | `pkg/cache` | 本地/多级缓存抽象 | 已完成 | Store + Memory LRU + Multi + Cache-Aside |
| P3 | `pkg/mq` | 消息生产/消费通用封装 | 已完成 | 接口 + Memory；事件信封；Broker 预留；Kafka/NATS 后接 |
| P3 | `pkg/outbox` | 事务性 Outbox 与投递 Relay | 已完成（ENT-03） | 条件认领/租约回收/指数退避/DLQ；MySQL + Memory Store |
| P3 | `pkg/httpclient` | 带超时、重试、tracing 的 HTTP Client | 已完成 | 强制超时；幂等重试；OTel 出站 |
| P4 | `pkg/config` | 配置加载与校验 | 已完成 | 通用 Loader/BindEnv/Watcher；platform 业务字段装配待迁移 |
| P4 | `pkg/logger` | 结构化日志 | 已完成 | slog + ContextHandler；platform 薄封装 |
| P4 | `pkg/middleware` | 通用中间件原语 | 已完成 | Chain/RequestID/Recover/Timeout/CORS/限流；鉴权留 platform |
| P4 | 其余工具包 | 见下方完整清单 | 待做 | 按实际需求再开 |

---

## 三、完整能力清单

### 3.1 基础横切

| 包路径 | 用途 | 优先级 | 状态 |
|--------|------|--------|------|
| `pkg/config` | 配置加载（env/yaml）、校验、热更新封装 | P4 | 已完成（严格 YAML + 分层覆盖 + env 绑定 + 原子热更新） |
| `pkg/logger` / `pkg/logx` | 结构化日志、字段约定、trace 关联 | P4 | 已完成（`pkg/logger`） |
| `pkg/errors` / `pkg/apperr` | 统一错误类型、错误码、堆栈/因果链 | P1 | 已完成 |
| `pkg/health` | 存活/就绪探针 | P0 | 已完成 |
| `pkg/metrics` | Prometheus 指标注册、常用 counter/histogram | P1 | 已完成 |
| `pkg/tracing` | OpenTelemetry / Jaeger 初始化与 span 工具 | P1 | 已完成 |
| `pkg/contextx` | 从 context 取 requestID、user、tenant 等 | P2 | 已完成 |

### 3.2 HTTP / API 公共层

| 包路径 | 用途 | 优先级 | 状态 |
|--------|------|--------|------|
| `pkg/httpx` / `pkg/response` | 统一响应体、状态码映射 | P1 | 已完成 |
| `pkg/middleware` | 请求 ID、超时、CORS、recover、限流（通用部分） | P4 | 已完成 |
| `pkg/validator` | 入参校验、自定义 tag、i18n 错误消息 | P2 | 已完成 |
| `pkg/pagination` | 分页/排序参数解析与默认值 | P2 | 已完成 |
| `pkg/idempotency` | 幂等 Key 中间件与存储接口 | P3 | 已完成 |

### 3.3 安全与身份

| 包路径 | 用途 | 优先级 | 状态 |
|--------|------|--------|------|
| `pkg/auth` / `pkg/jwt` | JWT 签发/校验、claims 解析（无业务策略） | P2 | 已完成（`pkg/jwt`） |
| `pkg/crypto` | 加解密、签名、哈希、密钥包装 | P2 | 已完成 |
| `pkg/password` | 密码哈希（bcrypt/argon2） | P3 | 已完成 |
| `pkg/mask` | 日志/响应脱敏（手机号、身份证等） | P2 | 已完成 |
| `pkg/rbac` | 权限判断原语（轻量；复杂策略放业务层） | P4 | 已完成 | 服务化（角色/绑定/数据范围/管理 API）规划见 [`rbac-iam.md`](./rbac-iam.md) |

### 3.4 数据与基础设施适配

| 包路径 | 用途 | 优先级 | 状态 |
|--------|------|--------|------|
| `pkg/db` / `pkg/sqlx` | DB 连接池、事务 helper、健康检查 | P3 | 已完成（`pkg/db`） |
| `pkg/redis` | Redis 客户端、分布式锁、缓存封装 | P3 | 已完成 |
| `pkg/mq` / `pkg/kafka` | 消息生产/消费通用封装 | P3 | 已完成（`pkg/mq`；Kafka 后接） |
| `pkg/outbox` | 事务性 Outbox：状态机、MySQL/Memory Store、并发 Relay、DLQ | P3 | 已完成（ENT-03；`async_outbox`/`async_dlq`） |
| `pkg/cache` | 本地/多级缓存抽象 | P3 | 已完成 |
| `pkg/oss` / `pkg/storage` | 对象存储上传下载（多云可插拔） | P4 | 已完成（`storage`+`s3/oss/cos/obs`；见 [`storage-s3-oss.md`](./storage-s3-oss.md)） |
| `pkg/migrate` | 迁移工具薄封装（主逻辑在 `cmd/migrate`） | P4 | 已完成（`up/down/status`、咨询锁、checksum/dirty、`cmd/migrate`；见 [`migration-guide.md`](./migration-guide.md)） |

### 3.5 并发、时间与通用工具

| 包路径 | 用途 | 优先级 | 状态 |
|--------|------|--------|------|
| `pkg/retry` | 重试、退避、可取消 | P2 | 已完成 |
| `pkg/circuitbreaker` | 熔断 | P3 | 已完成（三态 + 半开探测 + 可注入时钟） |
| `pkg/ratelimit` | 令牌桶/滑动窗口 | P2 | 已完成 |
| `pkg/clock` / `pkg/timeutil` | 可注入时钟、时区、业务日切 | P3 | 已完成（Clock/Manual、时区、日/周/月边界、固定 cutoff 日切） |
| `pkg/id` / `pkg/snowflake` | 分布式 ID 生成 | P2 | 已完成 |
| `pkg/set` / `pkg/slice` / `pkg/maputil` | 集合工具（避免变成杂物间） | P4 | 待做 |
| `pkg/convert` | 类型安全转换、指针工具 | P4 | 已完成（checked 数值/字符串/any 转换 + 指针工具） |
| `pkg/money` / `pkg/decimal` | 金额/精度（多域共用时） | P3 | 已完成（big.Int 定点 decimal + 最小单位 money / 最大余数分配） |

### 3.6 集成与对外客户端

| 包路径 | 用途 | 优先级 | 状态 |
|--------|------|--------|------|
| `pkg/httpclient` | 带超时、重试、tracing 的 HTTP Client | P3 | 已完成 |
| `pkg/sms` | 短信渠道适配（接口 + 实现） | P4 | 已完成（契约 + memory/阿里云驱动 + `smstest`） |
| `pkg/email` | 邮件渠道适配 | P4 | 已完成（契约 + MIME + memory/SMTP 驱动 + `emailtest`） |
| `pkg/push` | 推送渠道适配 | P4 | 已完成（契约 + memory/APNs/FCM 驱动 + `pushtest`） |
| `pkg/webhook` | 签名校验、重放保护 | P4 | 已完成（Standard Webhooks 兼容签名 + 时间窗 + `NonceStore` + 中间件；含出站 `Signer`） |

### 3.7 测试与开发辅助

| 包路径 | 用途 | 优先级 | 状态 |
|--------|------|--------|------|
| `pkg/testkit` | 测试 fixture、fake clock、临时 DB | P3 | 待做 |
| `pkg/mock` | 一般不单独建包；用 gomock 或模块旁 mock | — | 不计划 |

---

## 四、与现有代码的对照

| 能力 | 当前位置 | 后续建议 |
|------|----------|----------|
| health | `pkg/health` | 保持 |
| metrics | `pkg/metrics` | 保持；`platform.MountMetrics` + cmd 装配 |
| tracing | `pkg/tracing` | 保持；cmd Init + HTTPMiddleware；配置在 platform |
| retry | `pkg/retry` | 保持；下游 I/O / worker 按需调用 |
| ratelimit | `pkg/ratelimit` | 保持；HTTP 中间件与键策略在 platform |
| id | `pkg/id` | 保持；订单等场景注入 Generator；NodeID 按实例配置 |
| jwt | `pkg/jwt` | 保持；三端独立 iss/aud/密钥由 platform 装配 |
| crypto | `pkg/crypto` | 保持；字段级加密 / DEK 包装；密钥由 KMS/env 注入 |
| db | `pkg/db` | 保持；platform 注册驱动并装配 Config；就绪挂 `Checker()` |
| redis | `pkg/redis` | 保持；platform 装配 Addr/口令/前缀；就绪挂 `Checker()` |
| cache | `pkg/cache` | 保持；L1 Memory + L2 `redis.Cache.Store()` 由 platform 组合 |
| mq | `pkg/mq` | 保持；Kafka/NATS 实现 Broker 接口后注入；事件信封与 Header 契约 |
| outbox | `pkg/outbox` | 保持；平台装配 Store/Relay，业务只注册 Handler；DLQ 走 runbook |
| httpclient | `pkg/httpclient` | 保持；下游 HTTP 禁止 DefaultClient，统一本包 |
| logger | `pkg/logger` | 保持；`platform.NewLogger` 薄封装；AccessLog 仍在 platform |
| contextx | `pkg/contextx` | 保持；Auth 中间件注入；Principal 仍在 shared/auth |
| middleware | `pkg/middleware` | 保持；platform 薄封装 + AuthN/AuthZ |
| validator | `pkg/validator` | 保持；Handler DecodeJSON + Struct；映射 40000 |
| 配置 | `pkg/config` + `internal/platform/config` | 加载/环境变量/校验/热更新原语在 pkg；业务字段、默认值与 `Validate` 留 platform |
| 日志 | `pkg/logger` + `platform.NewLogger` | 原语在 pkg；进程装配留 platform |
| 中间件 / Server | `pkg/middleware` + `platform` | 通用原语在 pkg；鉴权/装配留 platform |
| 统一错误 | `pkg/errors` + `internal/shared/apperr` | 语义码/栈在 pkg；业务数字码留 apperr |
| HTTP 响应 | `pkg/httpx` + `internal/shared/httpx` | 信封/映射在 pkg；业务码挂接留 shared |
| 分页/排序 | `pkg/pagination` | 保持；列表 Handler 直接 `pagination.Parse` / `ParseParams` |
| 幂等 | `pkg/idempotency` | 保持；C 写路径 middleware + Store；跨实例由 platform 接 Redis/DB |
| 密码哈希 | `pkg/password` | 保持；默认 argon2id；登录成功可 NeedsRehash 升级 |
| 脱敏 | `pkg/mask` | 保持；日志/Trace/审计摘要显式 Phone/IDCard/Text |
| 权限点 | `pkg/rbac`；`internal/security.Authorizer` 薄封装 | 保持；角色表由 cmd/IAM 装配；IAM 域规划见 [`rbac-iam.md`](./rbac-iam.md)；ABAC 留业务层 |
| 对象存储 | `pkg/storage` + `s3/oss/cos/obs` 可选驱动 | L1 S3 兼容 + L2 原生（`native=true`）；多桶 / Lifecycle |
| 鉴权主体 | `internal/shared/auth` | 保留 internal；JWT 原语见 `pkg/jwt` |
| 熔断 | `pkg/circuitbreaker` | 保持；下游按 SLO 配置 Timeout/ReadyToTrip，`OnStateChange` 接指标 |
| 时钟/时区 | `pkg/clock` + `pkg/timeutil` | 保持；测试注入 `clock.Manual`；业务日切 cutoff 由 platform 配置 |
| 类型转换 | `pkg/convert` | 保持；数值/字符串/any 转换统一走本包，禁止绕过溢出检查 |
| 金额/精度 | `pkg/decimal` + `pkg/money` | 保持；金额落库用最小单位，汇率/比例用 decimal 并显式舍入 |

---

## 五、落地约定

1. **按需实现**：有明确调用方再开包，避免空目录堆砌。
2. **一个包一个职责**：包名即能力；禁止 `pkg/utils` 大杂烩。
3. **无业务依赖**：`pkg` 不得 import `internal/...`。
4. **可测试**：每个包至少有单元测试；涉及 IO 的提供 interface。
5. **状态回写**：实现或决定不下沉时，更新本文「状态」列与「最后更新」日期。

---

## 六、下一迭代建议（最小切片）

1. worker 消费路径挂 span；下游 HTTP 统一改用 `pkg/httpclient`。
2. C 端 `FromError` 可按端传入 `WithMessage` 笼统文案（`pkg/httpx` 已支持）。
3. 下游调用处接入 `pkg/retry`（order / worker）；NodeID 纳入配置/环境变量。
4. platform 已用 `internal/security` + `pkg/jwt` 接入真实鉴权（`auth.mode=jwt`）；敏感字段落库可挂 `pkg/crypto`；日志字段统一 `pkg/mask`。
5. 接入真实存储时：platform `NewDB` 挂 `pkg/db.Open`，`/readyz` 注册 `Checker()`；扩展 yaml 池参数。
6. 接入 Redis 时：platform 挂 `pkg/redis.Open`；热点读可用 `pkg/cache` Multi(L1 Memory + redis.Store)。
7. worker 消费挂 `pkg/mq`；领域事件经 `pkg/outbox`（事务性 Outbox → Relay → Handler）；Kafka/NATS 驱动按需实现 `pkg/mq.Broker`。
8. `pkg/storage` M0–M5 已落地（含 OSS/COS/OBS L2 原生）；细节见 [`storage-s3-oss.md`](./storage-s3-oss.md)。
9. 下游调用按「重试包住熔断」还是「熔断包住重试」明确策略后接入 `pkg/circuitbreaker`；金额换算统一 `pkg/decimal`/`pkg/money`，禁止 float64。
10. `internal/platform.LoadConfig` 已改为 `pkg/config.Loader` 装配（默认值/`normalize`/`Validate` 保留在 platform）：严格 YAML、环境变量解析失败即报错、历史存储变量名兼容。`pkg/config.Watcher` 已具备热更新能力，但 `pkg/logger` 尚不支持运行时改级别，接入前须先引入 `slog.LevelVar`；且连接池/存储等资源不做热重建。
