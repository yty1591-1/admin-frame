# 工程化配置与多环境规范（硬约束）

> **状态**：生效中（Normative）
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**
> **适用范围**：本仓库配置样例、加载逻辑、环境变量与多环境约定
> **关联**：架构 §十七；密钥见 `SECURITY_STANDARDS.md`；CI 见 `CI_STANDARDS.md`
> **执行方式**：`Validate()` 单测 + 配置样例门禁 + gitleaks + Review
> **最后更新**：2026-10-20
> **变更方式**：修改本规范须同步更新 `internal/platform/config.go`、`scripts/ci/check-config.sh`、`configs/*.example` 与架构 §十七，经架构 Owner Review

---

## 1. 目标

1. **启动即安全**：非法配置、互斥项、生产演示模式在启动时失败，不带病运行。
2. **密钥不入库**：仓库只保留 example；真实密钥来自环境变量 / 密钥管理系统。
3. **优先级清晰**：环境变量覆盖文件，行为可预期、可测。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| CFG-01 | 各业务进程必须有 `configs/prod/<name>.yaml.example`（admin / biz / c / worker） | `check-config.sh` |
| CFG-02 | Git 只跟踪 `*.yaml.example`；本地 `configs/*.yaml` 由 `.gitignore` 忽略，禁止提交真实密钥配置 | `check-config.sh` + `.gitignore` |
| CFG-03 | `LoadConfig` 内部按 CFG-04 顺序加载并由 `Loader` 自动调用 `Validate()`；失败则进程非零退出 | 代码 + 单测 |
| CFG-04 | 配置优先级：**环境变量 > 配置文件 > 默认值**（配置中心若引入须插在环境变量与文件之间，并更新本表） | `pkg/config.Loader`（`BindEnv`）+ 单测 |
| CFG-05 | `env=prod`（或等价）时 `auth.mode` 不得为 `demo`；`auth.expected_channel` 必须与 `channel` 一致；`auth.mode=jwt` 时 `issuer` / `audience` / 密钥必填 | `Validate` + 单测 |
| CFG-06 | example 中 `db.dsn` 必须为空或明显占位；禁止真实口令 / 生产主机 | `check-config.sh` + gitleaks |
| CFG-07 | 禁止在业务代码硬编码监听地址、超时、功能开关（应用配置或常量默认值经 `applyDefaults`；跨字段派生经 `normalize`） | Review |
| CFG-08 | 合并请求须勾选配置自检 | PR 模板 |

---

## 3. 文件与命名

配置按「领域模块 + 服务差异」分层声明，服务文件通过顶层 `include:` 组合模块，避免同一段
`http / auth / redis / db / storage / tracing / iam / identity` 在四个进程间各抄一份。

| 文件 | 用途 |
|------|------|
| `configs/prod/modules/http.yaml.example` | HTTP 运行时基线 |
| `configs/prod/modules/log.yaml.example` | 日志基线 |
| `configs/prod/modules/platform.yaml.example` | 雪花 ID、幂等、限流、异步 Outbox |
| `configs/prod/modules/auth.yaml.example` | JWT 认证基线 |
| `configs/prod/modules/infra.yaml.example` | Redis / MySQL / 对象存储 / 链路追踪 |
| `configs/prod/modules/iam.yaml.example` | IAM 基线（enforce） |
| `configs/prod/modules/identity.yaml.example` | 身份域基线 |
| `configs/prod/{admin,biz,c,worker}.yaml.example` | 各端差异（service / channel / addr / audience / node_id / CORS 等） |
| `configs/prod-*.yaml`（本地） | 从对应 example 复制；**不入库**，include 路径改为 `prod/modules/...` |

组合规则：

- `include:` 只能是顶层字段，值为非空路径列表；相对路径按声明文件所在目录解析，绝对路径允许
- 被包含文件按声明顺序先加载，当前文件最后加载；后出现的字段覆盖先出现的字段，未出现的字段保留
- 嵌套层数上限 8，出现循环引用或缺失文件直接启动失败；自定义 `Decode` 不解析 include
- YAML 锚点只在单个文件内有效，跨文件复用请用模块文件而不是锚点
- 环境变量优先级仍高于所有文件（CFG-04）

本地：

```bash
cp configs/prod/admin.yaml.example configs/prod-admin.yaml
# 把 include 的 modules/... 改为 prod/modules/...，按需修改；勿提交
```

---

## 4. 环境变量覆盖

前缀：`ADMIN_FRAME_`，由 `internal/platform.NewConfigLoader` 交给
[`pkg/config`](./pkg-roadmap.md) 的 `Loader` 完成：默认值 → 文件（含 `include` 展开）→
环境变量 → `normalize` → `Validate`。变量名由 yaml 标签自动推导（`log.level` →
`ADMIN_FRAME_LOG_LEVEL`），新增字段无需改环境变量代码。

行为约定：

- YAML 严格解析，未知字段（拼写错误、已删除字段）直接启动失败
- 环境变量无法解析（如 `ADMIN_FRAME_SHUTDOWN_TIMEOUT=15` 缺少单位）时报错，
  不再静默保留文件值；错误只含变量名，不含取值
- 空值（未设置或仅空白）视为未覆盖，无法用环境变量把字段置为空串
- 只识别由 yaml 标签推导出的规范变量名（如 `ADMIN_FRAME_STORAGE_S3_ACCESS_KEY`），
  不保留历史别名；改名须同步部署清单
- 仅标量字段与字符串列表（`[]string`，按逗号分隔）会自动推导环境变量；
  结构体列表与映射字段（`identity.subject.keys`、`identity.mfa.keys`、`identity.oauth.providers`、
  `identity.previous_hash_keys`、`auth.jwt.keys`、`storage.buckets`）不生成环境变量，
  必须写入挂载的 Secret 文件或由部署侧（Helm values）生成
- `auth.mode` 是唯一的鉴权模式开关；已移除 `auth.demo_mode` 旧字段与
  `ADMIN_FRAME_AUTH_DEMO_MODE` 环境变量，不再兼容。残留旧字段会因严格解析
  （未知字段）在启动期失败，须改为 `auth.mode: demo | jwt`

| 变量 | 覆盖字段 |
|------|----------|
| `ADMIN_FRAME_ENV` | `env`（local / dev / test / staging / prod） |
| `ADMIN_FRAME_SERVICE` | `service` |
| `ADMIN_FRAME_CHANNEL` | `channel` |
| `ADMIN_FRAME_ADDR` | `addr` |
| `ADMIN_FRAME_SHUTDOWN_TIMEOUT` | `shutdown_timeout`（Go duration，如 `15s`） |
| `ADMIN_FRAME_HTTP_READ_HEADER_TIMEOUT` | `http.read_header_timeout` |
| `ADMIN_FRAME_HTTP_READ_TIMEOUT` | `http.read_timeout` |
| `ADMIN_FRAME_HTTP_WRITE_TIMEOUT` | `http.write_timeout` |
| `ADMIN_FRAME_HTTP_IDLE_TIMEOUT` | `http.idle_timeout` |
| `ADMIN_FRAME_HTTP_MAX_HEADER_BYTES` | `http.max_header_bytes` |
| `ADMIN_FRAME_HTTP_TIMEOUT` | `http.timeout`（单请求软超时） |
| `ADMIN_FRAME_HTTP_BODY_LIMIT_BYTES` | `http.body_limit_bytes` |
| `ADMIN_FRAME_LOG_LEVEL` | `log.level` |
| `ADMIN_FRAME_LOG_FORMAT` | `log.format`（json / text） |
| `ADMIN_FRAME_AUTH_MODE` | `auth.mode`（demo / jwt） |
| `ADMIN_FRAME_AUTH_EXPECTED_CHANNEL` | `auth.expected_channel` |
| `ADMIN_FRAME_AUTH_JWT_ALG` | `auth.jwt.alg`（HS256 / RS256） |
| `ADMIN_FRAME_AUTH_JWT_ISSUER` | `auth.jwt.issuer` |
| `ADMIN_FRAME_AUTH_JWT_AUDIENCE` | `auth.jwt.audience`（逗号分隔） |
| `ADMIN_FRAME_AUTH_JWT_LEEWAY` | `auth.jwt.leeway` |
| `ADMIN_FRAME_AUTH_JWT_SECRET` | `auth.jwt.secret`（HS256，≥32 字节；禁止写文件） |
| `ADMIN_FRAME_AUTH_JWT_PUBLIC_KEY_FILE` | `auth.jwt.public_key_file`（RS256） |
| `ADMIN_FRAME_RATE_LIMIT_ENABLED` | `rate_limit.enabled` |
| `ADMIN_FRAME_RATE_LIMIT_DRIVER` | `rate_limit.driver`（memory / redis） |
| `ADMIN_FRAME_RATE_LIMIT_KEY` | `rate_limit.key`（channel / channel_user / channel_tenant_user / client） |
| `ADMIN_FRAME_RATE_LIMIT_FAILURE_MODE` | `rate_limit.failure_mode`（fail_open / fail_closed） |
| `ADMIN_FRAME_RATE_LIMIT_RPS` | `rate_limit.rps` |
| `ADMIN_FRAME_RATE_LIMIT_BURST` | `rate_limit.burst` |
| `ADMIN_FRAME_RATE_LIMIT_IDLE_TTL` | `rate_limit.idle_ttl` |
| `ADMIN_FRAME_ID_NODE_ID` | `id.node_id`（[0,1023]；prod/staging 非 worker 必填） |
| `ADMIN_FRAME_ID_NODE_ID_SOURCE` | `id.node_id_source`（env / config / statefulset） |
| `ADMIN_FRAME_IDEMPOTENCY_ENABLED` | `idempotency.enabled` |
| `ADMIN_FRAME_IDEMPOTENCY_DRIVER` | `idempotency.driver`（memory / redis） |
| `ADMIN_FRAME_IDEMPOTENCY_HEADER` | `idempotency.header`（默认 `Idempotency-Key`） |
| `ADMIN_FRAME_IDEMPOTENCY_TTL` | `idempotency.ttl` |
| `ADMIN_FRAME_IDEMPOTENCY_REQUIRED` | `idempotency.required` |
| `ADMIN_FRAME_IDEMPOTENCY_MAX_BODY_BYTES` | `idempotency.max_body_bytes` |
| `ADMIN_FRAME_ASYNC_ENABLED` | `async.enabled`（默认 false；启用要求 `db.driver ≠ memory`） |
| `ADMIN_FRAME_ASYNC_DRIVER` | `async.driver`（本轮仅 `db`） |
| `ADMIN_FRAME_ASYNC_OUTBOX_PUBLISH_IN_API` | `async.outbox.publish_in_api`（prod/staging + IAM enforce 必须 false） |
| `ADMIN_FRAME_ASYNC_OUTBOX_POLL_INTERVAL` | `async.outbox.poll_interval`（[100ms, 1m]） |
| `ADMIN_FRAME_ASYNC_OUTBOX_BATCH_SIZE` | `async.outbox.batch_size`（[1, 500]） |
| `ADMIN_FRAME_ASYNC_OUTBOX_LEASE` | `async.outbox.lease`（processing 租约，[5s, 10m]） |
| `ADMIN_FRAME_ASYNC_OUTBOX_MAX_ATTEMPTS` | `async.outbox.max_attempts`（[1, 20]） |
| `ADMIN_FRAME_ASYNC_OUTBOX_CONCURRENCY` | `async.outbox.concurrency`（[1, 16]） |
| `ADMIN_FRAME_ASYNC_CONSUMER_CONCURRENCY` | `async.consumer.concurrency`（[1, 32]） |
| `ADMIN_FRAME_ASYNC_CONSUMER_VISIBILITY_TIMEOUT` | `async.consumer.visibility_timeout`（[5s, 30m]） |
| `ADMIN_FRAME_ASYNC_CONSUMER_RETRY_BACKOFF` | `async.consumer.retry_backoff`（exponential / fixed） |
| `ADMIN_FRAME_REDIS_ADDR` | `redis.addr`（限流 / 幂等 / IAM 缓存 driver=redis 时必填） |
| `ADMIN_FRAME_REDIS_USERNAME` | `redis.username` |
| `ADMIN_FRAME_REDIS_PASSWORD` | `redis.password`（禁止写文件） |
| `ADMIN_FRAME_REDIS_DB` | `redis.db` |
| `ADMIN_FRAME_REDIS_KEY_PREFIX` | `redis.key_prefix` |
| `ADMIN_FRAME_CORS_ALLOW_ORIGINS` | `cors.allow_origins`（逗号分隔；生产禁止 `*` + credentials） |
| `ADMIN_FRAME_DB_DRIVER` | `db.driver` |
| `ADMIN_FRAME_DB_DSN` | `db.dsn` |
| `ADMIN_FRAME_STORAGE_DRIVER` | `storage.driver`（memory/local/s3/minio/oss/cos/obs） |
| `ADMIN_FRAME_STORAGE_KEY_PREFIX` | `storage.key_prefix` |
| `ADMIN_FRAME_STORAGE_LOCAL_ROOT` | `storage.local.root` |
| `ADMIN_FRAME_STORAGE_S3_ENDPOINT` | `storage.s3.endpoint` |
| `ADMIN_FRAME_STORAGE_S3_REGION` | `storage.s3.region` |
| `ADMIN_FRAME_STORAGE_S3_BUCKET` | `storage.s3.bucket` |
| `ADMIN_FRAME_STORAGE_S3_ACCESS_KEY` | `storage.s3.access_key` |
| `ADMIN_FRAME_STORAGE_S3_SECRET_KEY` | `storage.s3.secret_key` |
| `ADMIN_FRAME_STORAGE_S3_FORCE_PATH_STYLE` | `storage.s3.force_path_style` |
| （yaml）`storage.buckets.*` | 命名附加桶；不可用环境变量批量覆盖，见 `storage-s3-oss.md` §6.3 |
| （yaml）`storage.lifecycle` | 桶生命周期；仅 `apply_on_start=true` 时写入 |
| `ADMIN_FRAME_WORKER_POLL_INTERVAL` | `worker.poll_interval` |
| `ADMIN_FRAME_TRACING_ENABLED` | `tracing.enabled` |
| `ADMIN_FRAME_TRACING_EXPORTER` | `tracing.exporter`（none / otlp / stdout / noop） |
| `ADMIN_FRAME_TRACING_ENDPOINT` | `tracing.endpoint` |
| `ADMIN_FRAME_TRACING_INSECURE` | `tracing.insecure` |
| `ADMIN_FRAME_TRACING_SAMPLE_RATIO` | `tracing.sample_ratio` |

生产密钥、DSN **优先**用环境变量注入，不写进文件或镜像层。

---

## 5. Validate 基线

`platform.Config.Validate()` 至少校验（错误经 `errors.Join` 聚合，一次列出全部问题）：

- `service`、`channel`、`addr` 非空
- `channel` ∈ {admin, biz, c, worker}
- `auth.expected_channel` 与 `channel` 一致（缺省时由 `normalize` 在 env 覆盖后派生）
- `auth.mode` ∈ {demo, jwt}；`env=prod/staging` 且未显式声明 mode 时回退 `jwt`（缺 JWT 配置即失败，绝不静默回退 demo）
- `auth.mode=jwt` ⇒ `auth.jwt.issuer` 非空、`audience` 含当前 channel、算法密钥满足长度/文件要求
- `db.driver` ∈ {memory, mysql, postgres}；非 memory 时 `db.dsn` 必填
- `storage.driver` ∈ {memory, local, s3, minio, oss, cos, obs}；`local` 时 `storage.local.root` 必填；`env=prod` 禁止 `memory`；命名桶内禁止嵌套 `buckets`
- `log.level` ∈ {debug, info, warn, error}；`log.format` ∈ {json, text}；`env=prod` 禁止 `text`（避免 `logger.Must` panic）
- `http.read_header_timeout` / `max_header_bytes` / `timeout` / `body_limit_bytes` ≥ 0；`env=prod` 时 `http.timeout > 0`
- `rate_limit.enabled=true` ⇒ `driver ∈ {memory, redis}`、`key ∈ {channel, channel_user, channel_tenant_user, client}`、`rps > 0`、`burst ≥ 1`；`driver=redis` ⇒ `redis.addr` 必填
- `id.node_id` ∈ [0,1023]；`id.node_id_source ∈ {env, config, statefulset}`；`statefulset` 与显式 `node_id` 互斥，`registry` 暂不支持（启动失败）
- `env ∈ {prod, staging}` 且 `channel ≠ worker` ⇒ 非 statefulset 时必须显式 `id.node_id`（避免多副本重复 ID）
- `idempotency.enabled=true` ⇒ `driver ∈ {memory, redis}`、`ttl > 0`、`header` 非空；`driver=redis` ⇒ `redis.addr` 必填
- `env ∈ {prod, staging}` 且启用幂等或限流 ⇒ `driver` 必须为 `redis`（否则配额/去重被副本数放大）
- `async.driver ∈ {db, nats}`：`db`（默认）只走 DB Outbox；`nats` 在其之上把事件发布到 NATS JetStream（Outbox 仍是事实源）；`poll_interval ∈ [100ms,1m]`、`batch_size ∈ [1,500]`、`lease ∈ [5s,10m]`、`max_attempts ∈ [1,20]`、`outbox.concurrency ∈ [1,16]`、`consumer.concurrency ∈ [1,32]`、`visibility_timeout ∈ [5s,30m]`、`retry_backoff ∈ {exponential, fixed}`
- `async.driver=nats` 且 `async.enabled=true` ⇒ `async.broker.url` 必填且以 `nats://`、`tls://`、`ws://`、`wss://` 开头；`env ∈ {prod, staging}` 时必须 `tls://` 或 `wss://`；`broker.stream` 仅允许 `[A-Za-z0-9_-]`（默认 `async`）；`broker.subject_prefix` 禁止通配符、空白与空层（默认 `async`）；`broker.ack_wait ∈ [1s,30m]`（默认 `30s`）、`broker.max_deliver ∈ [1,100]`（默认 `10`）；连接凭证建议走 `ADMIN_FRAME_ASYNC_BROKER_URL`
- `async.enabled=true` ⇒ `db.driver ≠ memory`（Outbox 必须持久化）
- `env ∈ {prod, staging}` 且 `iam.mode=enforce` 且 `async.enabled=true` ⇒ `async.outbox.publish_in_api=false`（由 worker 独占投递，避免重复）
- `env` 为空或 ∈ {local, dev, test, staging, prod}
- `tracing.exporter` ∈ {none, otlp, stdout, noop}；`otlp` 时 `endpoint` 必填；`env=prod` 禁止 `stdout`

鉴权装配另见 SEC-04：`auth.mode` 未显式配置时按环境回退，生产（prod/staging）必须有可用的 JWT 配置。

**日志脱敏**：`Config.LogValue` / `DBConfig.LogValue` / `StorageConfig.LogValue` 提供
`slog.LogValuer`，DSN 只保留 scheme，JWT 密钥与云厂商 AK/SK 输出为 `***`。
禁止用 `%+v` / `json.Marshal(cfg)` 直接打印配置。

---

## 6. 多环境

| 环境 | `env` | 要求 |
|------|-------|------|
| local | local | 可用 `auth.mode=demo`、memory DB |
| dev / test | dev / test | 可销毁数据；禁止生产凭据 |
| staging | staging | 拓扑近似生产；密钥隔离；必须 `auth.mode=jwt`；多副本时显式 `id.node_id` 或 `statefulset`，幂等/限流用 `redis` |
| prod | prod | `auth.mode=jwt`；密钥走 KMS/环境变量；审计开启；多副本同上，`async.enabled=true` 且 `async.outbox.publish_in_api=false`，投递统一由 `cmd/worker` 承担（IAM enforce 强制 `async.enabled=true`） |

---

## 7. MUST NOT

- 将含真实 DSN / 云密钥的 `configs/*.yaml` 提交入库。
- 跳过 `Validate` 直接使用配置。
- 在 prod 打开 `auth.mode=demo`。
- 用业务代码魔法数替代可配置超时 / 地址（演示常量除外且不得用于生产路径）。

---

## 8. 本地与 CI

```bash
make check-config   # CFG-01/02/06
make test           # 含 config Validate / env 单测
make secrets        # gitleaks（密钥）
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | step/job 纳入 `config`（或与 `secrets` 同批） |
| MR | PR 模板「配置自检」 |

---

## 9. 例外

1. **临时本地 yaml 需分享结构**：只更新 `.example`，不共享含密钥的副本。
2. **配置中心**：引入时写 ADR，并更新本专文优先级表与加载代码。
3. **紧急热修**：24h 内补齐 `Validate` 与 example，轮换任何可能泄露的凭据。
4. **热更新**：`pkg/config.Watcher` 已具备能力，但当前所有进程只在启动期加载。
   启用前须定义可热更字段白名单（`addr`、`auth`、`storage.driver` 等需重建资源的字段必须排除）并写 ADR。

---

## 10. Review 清单

- [ ] example 已同步；无真实密钥入库
- [ ] 新配置项有默认值或 Validate；prod 相关互斥已覆盖
- [ ] 环境变量名已写入本专文或 example 注释
- [ ] `make check-config` 与相关单测通过

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `configs/*.yaml.example` | 入库样例 |
| `.gitignore`（`configs/*.yaml`） | CFG-02 |
| `internal/platform/config.go` | 业务字段 / 默认值 / `normalize` / `Validate`；加载委托 `pkg/config` |
| `pkg/config` | 通用 Loader / 严格 YAML / env 绑定 / 热更新 `Watcher` |
| `scripts/ci/check-config.sh` | CFG-01/02/06 |
| gitleaks | 密钥扫描 |
| 本文件 | 规范正文 |
