# pkg 公共能力包路线图

> **状态**：主要能力已落地（P0～P3 完成；P4 工具包按需补齐）
> **最后更新**：2026-10-21（`pkg/clock` / `timeutil` / `circuitbreaker` / `convert` / `decimal` / `money` / `webhook` / `config` 已落地）
> **变更方式**：新增包、状态迁移或放置边界调整须同步本文「能力清单」与「最后更新」；新增第三方依赖按 DEP-06 在 MR 说明用途、许可证与替代方案
> **对照**：业务约定放 `internal/shared`；进程装配放 `internal/platform`；通知渠道见 [`notification-channels.md`](./notification-channels.md)；RBAC 服务化见 [`rbac-iam.md`](./rbac-iam.md)

---

## 一、放置边界

| 放 `pkg/` | 放 `internal/shared` | 放 `internal/platform` |
|-----------|----------------------|------------------------|
| 与具体业务无关、可跨仓库复用 | 仅本仓库用的约定与类型 | 进程启动与依赖接线 |
| 如：health、httpx、metrics、tracing、retry、ratelimit、id、jwt、crypto、db、redis、cache、mq、httpclient、logger、contextx、middleware、validator、pagination、idempotency、password、mask、rbac、storage | 如：apperr、httpx（薄封装）、auth.Principal | 如：config、server |

**经验法则**：换一个项目也能直接拷走 → `pkg`；一沾业务规则 → `internal`。

---

## 二、能力清单与状态

状态约定：`已完成` / `进行中` / `待做` / `暂缓` / `不计划`。优先级按「多入口（admin/biz/c-api/worker）立刻用得上」排序。

### 2.1 基础横切

| 包路径 | 用途 | 优先级 | 状态与装配备注 |
|--------|------|--------|----------------|
| `pkg/health` | 存活 / 就绪探针 | P0 | 已完成 |
| `pkg/config` | 配置加载（严格 YAML + 分层覆盖 + env 绑定 + 原子热更新）、校验 | P4 | 已完成；业务字段、默认值与 `Validate` 留 `internal/platform` |
| `pkg/logger` | 结构化日志（slog + ContextHandler） | P4 | 已完成；`platform.NewLogger` 薄封装，AccessLog 仍在 platform |
| `pkg/errors` | 统一错误类型、错误码、因果链 | P1 | 已完成；`internal/shared/apperr` 薄封装业务数字码 |
| `pkg/metrics` | Prometheus 指标注册、常用 counter / histogram | P1 | 已完成；HTTP RED + build_info，三端已挂 `/metrics` |
| `pkg/tracing` | OpenTelemetry 初始化与 span 工具 | P1 | 已完成；W3C 传播，与 RequestID / 信封 trace_id 对齐 |
| `pkg/contextx` | 从 context 取 requestID、user、tenant | P2 | 已完成；Auth 中间件注入，`Principal` 仍在 `internal/shared/auth` |

### 2.2 HTTP / API 公共层

| 包路径 | 用途 | 优先级 | 状态与装配备注 |
|--------|------|--------|----------------|
| `pkg/httpx` | 统一响应体、状态码映射 | P1 | 已完成；`internal/shared/httpx` 薄封装挂接 `apperr`，业务码映射留 shared |
| `pkg/middleware` | Chain / RequestID / Recover / Timeout / CORS / 限流 | P4 | 已完成；鉴权与装配留 platform |
| `pkg/validator` | 入参校验、自定义 tag、i18n 错误消息 | P2 | 已完成；Handler `DecodeJSON` + Struct，映射 `40000` |
| `pkg/pagination` | 分页 / 排序参数解析与默认值 | P2 | 已完成；列表 Handler 直接 `pagination.Parse` / `ParseParams` |
| `pkg/idempotency` | 幂等 Key 中间件与存储接口 | P3 | 已完成；C 写路径 middleware + Store，跨实例由 platform 接 Redis / DB |

### 2.3 安全与身份

| 包路径 | 用途 | 优先级 | 状态与装配备注 |
|--------|------|--------|----------------|
| `pkg/jwt` | JWT 签发 / 校验、claims 解析 | P2 | 已完成；HS256 / RS256，三端独立 iss / aud / 密钥由 platform 装配 |
| `pkg/crypto` | 加解密、签名、哈希、密钥包装 | P2 | 已完成；AES-256-GCM / HMAC / Ed25519 / WrapKey，stdlib only；密钥由 KMS / env 注入 |
| `pkg/password` | 密码哈希（argon2id / bcrypt） | P3 | 已完成；登录成功可按 `NeedsRehash` 透明升级 |
| `pkg/mask` | 日志 / 响应脱敏 | P2 | 已完成；日志、Trace、审计摘要显式调用 `Phone` / `IDCard` / `Text` |
| `pkg/rbac` | 权限判断原语（轻量） | P4 | 已完成；角色表由 cmd / IAM 装配，ABAC 留业务层；IAM 规划见 [`rbac-iam.md`](./rbac-iam.md) |

### 2.4 数据与基础设施适配

| 包路径 | 用途 | 优先级 | 状态与装配备注 |
|--------|------|--------|----------------|
| `pkg/db` | DB 连接池、事务 helper、健康检查 | P3 | 已完成；`database/sql`，驱动由 platform 注册，无 migrate；就绪挂 `Checker()` |
| `pkg/redis` | Redis 客户端、分布式锁、缓存封装 | P3 | 已完成；go-redis，单实例锁 + Cache-Aside KV；platform 装配 Addr / 口令 / 前缀 |
| `pkg/cache` | 本地 / 多级缓存抽象 | P3 | 已完成；Store + Memory LRU + Multi；L1 Memory + L2 `redis.Cache.Store()` 由 platform 组合 |
| `pkg/mq` | 消息生产 / 消费通用封装 | P3 | 已完成；接口 + Memory + 事件信封；NATS JetStream 适配器在 `internal/platform/async_nats.go`；Kafka 后接 |
| `pkg/outbox` | 事务性 Outbox 与投递 Relay | P3 | 已完成（ENT-03）；条件认领 / 租约回收 / 指数退避 / DLQ；Store 与 Relay 由 platform 装配，业务只注册 Handler |
| `pkg/storage` | 对象存储上传下载（多云可插拔） | P4 | 已完成；L1 S3 兼容 + L2 原生（`native=true`），多桶 / Lifecycle；见 [`storage-s3-oss.md`](./storage-s3-oss.md) |
| `pkg/migrate` | 迁移工具薄封装 | P4 | 已完成；主逻辑在 `cmd/migrate`，见 [`migration-guide.md`](./migration-guide.md) |

### 2.5 并发、时间与通用工具

| 包路径 | 用途 | 优先级 | 状态与装配备注 |
|--------|------|--------|----------------|
| `pkg/retry` | 重试、退避、可取消 | P2 | 已完成；默认挂接 `errors.RetryableOf`，`Permanent` 短路 |
| `pkg/circuitbreaker` | 熔断（三态 + 半开探测） | P3 | 已完成；可注入时钟，下游按 SLO 配置，`OnStateChange` 接指标 |
| `pkg/ratelimit` | 令牌桶 / 滑动窗口 | P2 | 已完成；原语在 pkg，键策略 / `42900` 映射留 platform |
| `pkg/clock` / `pkg/timeutil` | 可注入时钟、时区、业务日切 | P3 | 已完成；Clock / Manual、日 / 周 / 月边界、固定 cutoff 日切在 platform 配置 |
| `pkg/id` | 分布式 ID（snowflake） | P2 | 已完成；订单创建已接入，多实例须唯一 NodeID |
| `pkg/set` / `pkg/slice` / `pkg/maputil` | 集合工具 | P4 | 待做（避免演化成杂物间） |
| `pkg/convert` | 类型安全转换、指针工具 | P4 | 已完成；checked 数值 / 字符串 / any 转换，禁止绕过溢出检查 |
| `pkg/decimal` / `pkg/money` | 金额与精度 | P3 | 已完成；big.Int 定点 decimal + 最小单位 money；金额落库用最小单位，禁止 float64 |

### 2.6 集成、对外客户端与测试辅助

| 包路径 | 用途 | 优先级 | 状态与装配备注 |
|--------|------|--------|----------------|
| `pkg/httpclient` | 带超时、重试、tracing 的 HTTP Client | P3 | 已完成；下游禁止 `http.DefaultClient`，统一本包 |
| `pkg/sms` | 短信渠道适配 | P4 | 已完成；契约 + memory / 阿里云驱动 + `smstest` |
| `pkg/email` | 邮件渠道适配 | P4 | 已完成；契约 + MIME + memory / SMTP 驱动 + `emailtest` |
| `pkg/push` | 推送渠道适配 | P4 | 已完成；契约 + memory / APNs / FCM 驱动 + `pushtest` |
| `pkg/webhook` | 签名校验、重放保护 | P4 | 已完成；Standard Webhooks 兼容签名 + 时间窗 + `NonceStore` + 中间件，含出站 `Signer` |
| `pkg/testkit` | 测试 fixture、fake clock、临时 DB | P3 | 待做 |
| `pkg/mock` | 测试替身 | — | 不计划；用 gomock 或模块旁 mock |

---

## 三、容器与装配边界

- `cmd` / `internal/platform` 负责全部装配：驱动注册、地址 / 口令 / 前缀、iss / aud / 密钥、鉴权中间件、业务配置字段与 `Validate`。
- `pkg` 不得 import `internal/...`；包内不写业务规则，不读配置文件与环境变量（由 platform 映射）。
- 出站 HTTP 统一 `pkg/httpclient`；敏感字段落库挂 `pkg/crypto`；日志字段统一 `pkg/mask`。
- 异步链路：业务只注册 Handler；MQ 适配器、Outbox Store / Relay、就绪检查均在 platform / cmd 装配。
- 密钥类配置只来自环境变量 / KMS，示例配置保持为空。

---

## 四、落地约定

1. **按需实现**：有明确调用方再开包，避免空目录堆砌。
2. **一个包一个职责**：包名即能力；禁止 `pkg/utils` 大杂烩。
3. **无业务依赖**：`pkg` 不得 import `internal/...`。
4. **可测试**：每个包至少有单元测试；涉及 IO 的提供 interface。
5. **状态回写**：实现或决定不下沉时，更新本文「状态」列与「最后更新」日期。

---

## 五、下一迭代建议（最小切片）

1. worker 消费路径挂 span；下游 HTTP 统一改用 `pkg/httpclient`。
2. 下游调用处接入 `pkg/retry`（order / worker）；NodeID 纳入配置 / 环境变量。
3. 下游调用明确「重试包住熔断」或「熔断包住重试」后接入 `pkg/circuitbreaker`；金额换算统一 `pkg/decimal` / `pkg/money`。
4. 跨团队消费需求出现时切 `async.driver=nats`；Kafka 按需实现 `pkg/mq.Broker`，领域事件统一经 `pkg/outbox`。
5. C 端 `FromError` 可按端传入 `WithMessage` 笼统文案（`pkg/httpx` 已支持）。
6. `pkg/logger` 接入运行时改级别前须先引入 `slog.LevelVar`；连接池 / 存储等资源不做热重建。
