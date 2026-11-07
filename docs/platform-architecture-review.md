# internal/platform 架构评审报告

> **状态**：评审完成（v0.1，2026-11-07）＋ 整改完成并回写度量（v0.2，2026-11-07）
> **定位**：只评审 `internal/platform` 的结构、职责边界与装配方式；业务域实现、`pkg/` 库质量不在范围内
> **适用范围**：`internal/platform/`（含 `dependency/`、`secrets/` 子包）及其被 `cmd/*` 的使用方式
> **关联**：[ARCHITECTURE.md](./ARCHITECTURE.md)（§3.3、§5.6、§6.1）、[CODE_STYLE.md](./CODE_STYLE.md)（S-11）、[CONFIG_STANDARDS.md](./CONFIG_STANDARDS.md)、[scripts/ci/check-layering.sh](../scripts/ci/check-layering.sh)
> **最后更新**：2026-11-07
> **变更方式**：整改完成后须回写本文状态表，并同步下调 `check-layering.sh` 中的拆分基线；基线只减不增
> **目标读者**：技术负责人、后端工程师、架构 Reviewer、SRE

---

## 一、结论摘要

「不符合企业级 / 生产级架构」的判断**部分成立**：运行时能力（生命周期、超时、限流、幂等、追踪、探针）是达标的，问题集中在**配置层巨石**与**装配层隐式顺序**，且这两点已直接违反项目自己的规范性条款（ARCHITECTURE §5.6 的拆分触发条件 PLAT-15）。

> **v0.2 复评（2026-11-07）**：下表 3 项「不达标」均已整改关闭，逐项证据见[第十节](#十整改执行状态2026-11-07)。
> 复评实测：`platform` 根包非测试文件 **5** 个（≤ 5 基线）、根包最大文件 483 行；全部子包最大文件 256 行
> （`async/async_nats.go` 622 行已拆为 `async_nats.go` 256 + `async_nats_consumer.go` 252 + `async_nats_message.go` 143；
> `secrets/vault.go` 619 行已拆为 `vault.go` 419 + `vault_helpers.go` 211）。
> `check-layering.sh` LAYER-01～10 全绿，且 LAYER-08 已改为递归覆盖子包。

| 维度 | 判断 | 说明 |
|---|---|---|
| 运行时可靠性 | 达标 | `defer` 清理完整、无 `os.Exit`、端口先绑定后打日志、优雅关闭可配 |
| 依赖方向 | 达标 | `check-layering.sh` LAYER-01～10 全绿，platform 未 import 任何业务域实现 |
| 职责边界 | ~~不达标~~ → **达标**（v0.2） | 域配置已下沉 `internal/<domain>/config`；`GuardPort` / `MembershipPort` 已下沉 `internal/tenant/app`（见 P0-2、P1-4） |
| 包内聚与拆分 | ~~不达标~~ → **达标**（v0.2） | 根包 5 个非测试文件 / 最大 483 行；子包最大 256 行（见 P0-1、P2-7） |
| 装配可演进性 | ~~不达标~~ → **达标**（v0.2） | `Run` 拆为 7 个显式步骤，前置依赖逐项校验（见 P0-3） |
| 可测试性 | 达标 | 生产 5891 行 / 测试 3633 行（28 个测试文件），关键路径均有单测 |

---

## 二、评审依据与度量

评审依据为仓库内已生效的规范性条款，而非通用口味：

- ARCHITECTURE §5.6 L406-L413：`platform` 只放配置、日志、指标、追踪、探针、通用中间件、鉴权装配、进程生命周期；「不承载业务语义」；单包非测试文件 > 20 **或**单文件 > 500 行**必须**拆分到 `internal/platform/{config,observability,httpserver,authn}`。
- ARCHITECTURE §6.1 L434：`platform → shared、security、pkg`，不得反向依赖业务域。
- CODE_STYLE S-11：控制函数体与复杂度，超限须拆分或 MR 说明。

度量（2026-11-07，v0.1 评审时 `internal/platform` 顶层；v0.2 复评值见末列）：

| 指标 | 实测（v0.1） | 规范阈值 | 结论 | v0.2 复评 |
|---|---|---|---|---|
| 非测试 `.go` 文件（根包） | 21 | ≤ 20 | 超限 1 | **5**（达标） |
| 非测试代码行（含子包） | 6357 | — | — | 5891 |
| 单文件最大行数 | 3375（`config.go`） | ≤ 500 | 超限 6.7 倍 | **483**（`bootstrap.go`，达标） |
| 第二超限文件 | 620（`async_nats.go`） | ≤ 500 | 超限 | **419**（`secrets/vault.go` 拆分后，达标） |
| 子包超限文件 | 未统计（门禁盲区） | ≤ 500 | 未拦截 | 0（LAYER-08 已递归覆盖） |
| 配置结构体数（`config.go`） | 47 | — | 单文件承担全部域配置 | 已按域下沉，平台段独立文件 |
| 测试代码行 | 5011（29 文件） | — | 测试密度合理 | 3633（28 文件，生产 5891 行） |

---

## 三、问题清单

### P0-1 配置层单文件巨石

**证据**：[config.go](../internal/platform/config/config.go) 3375 行，内含 47 个 `*Config` 结构体，同时承担 schema 定义、默认值、归一化、校验、敏感字段脱敏、密钥解码、SAML 证书解析。

单一文件内并存六类职责：

| 职责 | 位置 |
|---|---|
| schema（47 结构体） | `config.go` 全文 |
| 默认值 | `applyDefaults` / `applyAuditDefaults` / `applyIdentityDefaults` / `applySAMLDefaults` / `applyAsyncDefaults` / `applyIAMDefaults` |
| 归一化 | `normalize` / `normalizeIAMMode` / `normalizeIAMConstraintMode` |
| 校验 | `Validate` + 20 个 `validateXxx` |
| 脱敏 | `maskIfSet` / `redactDSN` / `LogValue` |
| 密钥与证书解析 | `DecodeKeys` / `decodeMFAKey` / `parseSAMLKeyPair` |

**影响**：新增任一域配置都要改同一个文件，code review 面过宽、合并冲突高发；任何一次改动都可能影响与它无关的域；单测只能整包跑（`config_*_test.go` 11 个文件合计 3135 行，占 platform 测试量 63%）。

**违反**：ARCHITECTURE §5.6 拆分触发条件 1（PLAT-15）；S-11。

### P0-2 平台层承载业务语义

**证据**：

- 域配置内嵌在平台 schema：`IdentitySubject/MFA/SAML/OAuth/SMS/Session/Password/Lockout`、`IAMCertification/Constraints/Breakglass/Approval`、`Audit`、`Tenant`（`config.go` L95-L1082）。
- 跨配置段耦合：`AuditConfig.EffectiveStore(dbDriver)`（L177）让审计出口依赖 `db.driver`。
- 协议解析进入平台层：`parseSAMLKeyPair`（L3184）在 platform 内解析 X.509 与 RSA 私钥。

**影响**：配置 schema 成为事实上的「全项目公共契约」，`cmd/*` 通过 `deps.Config` 直接访问任意域配置，域内演进必须改平台层；`platform` 与 `identity`/`iam` 的边界只能靠 review 维持。

**违反**：ARCHITECTURE §5.6「不承载业务语义」、§6.1 依赖方向意图。

### P0-3 装配顺序隐式、god function

**证据**：[bootstrap.go](../internal/platform/bootstrap.go#L115-L349) 的 `Run` 为 235 行（文件 416 行），`Options` 5 个回调（`NewSessionValidator` / `NewTenantGuard` / `NewQuotaGuard` / `NewAuthorizer` / `Register`），`Deps` 18 个字段。顺序约束全部写在注释里：`Async` 必须先于 identity 运行时（L231）、`NewTenantGuard` 必须先于 identity（L242）、`NewSessionValidator` 必须先于 `NewAuthMW`（L264）、配额守卫必须包在租户守卫内层（L287）。

**影响**：

- 顺序无编译期约束，重排/新增阶段只能靠人记住注释；错序的后果是运行期语义错误（如「已停用租户仍消耗配额」）而不是启动失败。
- 每新增一个横切能力（如新的守卫/新的运行时）都必须修改这个 416 行函数，并新增一个 `Options` 字段，`Options` 正在演变为新的 god object。
- 回调内构造的依赖与 `Run` 内构造的依赖共享隐式可变状态（`deps.X` 被多次写入），难以单测整体装配链。

**违反**：S-11；ARCHITECTURE §2「显式优于隐式」。

### P1-4 业务语义端口定义在平台层，绕过门禁

**证据**：`TenantGuard`（[tenantguard.go](../internal/platform/httpserver/guards.go)）、`TenantMembership`（L29）、`QuotaGuard`（[quotaguard.go](../internal/platform/httpserver/guards.go)）定义在 platform，实现位于 `cmd/internal/tenantruntime` 与 `internal/tenant/app`。

**影响**：ENT-09 / ENT-14 的判定语义（租户状态、成员关系、API 配额）以「端口」形式固化在平台层，平台层事实上定义了租户域契约；而 `check-layering.sh` 的 LAYER-02 只检查 import，**不检查平台层是否定义了业务语义**，因此这类违规不会被门禁发现。

### P1-5 配置对象承载隐藏可变状态

**证据**：`Config.secretErr` 未导出字段（[config.go](../internal/platform/config/config.go)），注释说明原因是「Loader 的 Normalize 钩子无法返回错误」，错误在 `Validate` 阶段汇总。

**影响**：配置对象变成「可携带错误的状态机」，`LoadConfig` 成功返回后对象内部仍可能持有待汇报错误；并发复用或部分字段访问时错误语义不清。根因是 `pkg/config` 的钩子签名不支持返回 error，属于「用隐状态弥补依赖库缺陷」。

### P1-6 同名 `httpx` 双包，调用方需二选一

**证据**：[internal/shared/apiresp/response.go](../internal/shared/apiresp/response.go) 与 [pkg/httpx/envelope.go](../pkg/httpx/envelope.go) 曾同为 `package httpx`；[pipeline.go](../internal/platform/httpserver/pipeline.go#L11-L20) 同时 import 二者（`httpx` + `pkghttpx`），`middleware.go` 与 `pipeline.go` 内混用。

**影响**：两个包名相同、职责重叠（信封写入 vs apperr 适配），新代码容易选错入口；错误映射策略实际分散在三处（`apperr`、`pkg/errors`、`pkg/httpx`）。

### P2-7 配置语义与运行期语义混用

**证据**：`AuditConfig.EffectiveStore`、`IAMConfig.Enabled`、`TracingConfig.IsEnabled`、`RedisConfig.Enabled` 等「派生运行期决策」的方法挂在配置结构体上，部分还依赖其他配置段（`db.driver`）。

**影响**：配置结构体同时承担数据载体与策略判定，校验与运行语义无法分别测试；跨段派生逻辑（审计出口随 DB 驱动变化）在配置文件层面不可见。

### P2-8 装配期依赖注册表与 pkg 原语职责重叠

**证据**：`internal/platform/dependency/registry.go` 组合 `pkg/circuitbreaker` + `pkg/retry` + 指标，提供 `Caller.Do`；`pkg/httpclient`、`pkg/webhook` 也各自实现重试/超时。

**影响**（较低）：出站调用的弹性策略存在两套入口，规范只约束了「登记表」一侧（`check-deps.sh`），`pkg/httpclient` 的调用路径未纳入统一登记，指标口径可能分裂。

---

## 四、符合项（不应在整改中被破坏）

| 项 | 证据 |
|---|---|
| 生命周期与优雅关闭 | [server.go](../internal/platform/httpserver/server.go)：先 `Listen` 再打日志，`signal.NotifyContext` + `Shutdown` 超时可配 |
| 资源释放确定性 | [bootstrap.go](../internal/platform/bootstrap.go#L150-L189) 全部 `defer` 关闭，错误经命名返回值汇总，无 `os.Exit` |
| 中间件下沉 pkg | [middleware.go](../internal/platform/httpserver/middleware.go) 仅做配置映射与委托；[pipeline.go](../internal/platform/httpserver/pipeline.go) 明确固定顺序 |
| 能力装配收敛 | `Deps` 只暴露平台能力（配置/日志/指标/追踪/存储/DB/Redis/ID/守卫/健康检查），未暴露 repo/bus |
| 基础设施适配薄 | [infra.go](../internal/platform/infra.go#L248-L281)（`OpenDatabase` / `OpenRedis` 纯映射）、`observability/logger.go`（16 行）均为纯映射 |
| 门禁可执行 | `make check-layering` 已在 `make ci` 中，LAYER-01～06 有实际拦截力 |

---

## 五、目标形态

按 ARCHITECTURE §5.6 L413 的既定目标，而非另立新方案：

```text
internal/platform/
├── config/          # 仅核心段：env/service/channel/addr/http/log/db/redis/storage/tracing/auth/id/idempotency/rate_limit/cors/secrets
├── observability/   # logger, tracing, metrics, db_metrics, dependency(含 observability 适配)
├── httpserver/      # server, endpoints, pipeline, middleware, 通用守卫装配
├── authn/           # JWT/demo 鉴权装配
├── async/           # AsyncRuntime + outbox relay + broker 适配（nats）
└── secrets/         # 现有子包，保持
```

域配置迁出平台层，落在各自域：

```text
internal/identity/config/   # subject/mfa/saml/oauth/sms/session/password/lockout...
internal/iam/config/        # 快照缓存/certification/constraints/breakglass/approval/limits
internal/audit/config/      # store/retention/query/export
internal/tenant/config/     # 处置时间线/配额
```

`platform.Config` 只保留聚合结构与「加载 + 绑定 + 编排校验」，不再内置任何域判定方法。

---

## 六、整改路线

分三期，每期独立可合入；每期结束时同步下调门禁基线（只减不增）。

### 阶段 1：拆文件，不改 API（低风险）

1. `config.go` 按域拆为 `config_core.go`、`config_identity.go`、`config_iam.go`、`config_audit.go`、`config_tenant.go`、`config_async.go`、`config_storage.go`、`config_validation.go`、`config_redact.go`，先保持 `package platform` 与全部导出符号不变。
2. `bootstrap.go` 的 `Run` 拆为 `prepareInfra` / `buildGuards` / `buildAuthN` / `buildPipeline` / `serve` 五个命名步骤（仍由 `Run` 顺序调用），把每个顺序约束变成「步骤函数内部的前置条件检查 + 明确注释」。
3. 门禁更新：`LAYER_LINE_BASELINE` 清空，`PLATFORM_MAX_FILES` 不变（拆文件不增包）。

**验收**：`make check-layering` 输出无 BASELINE 行；`go build ./...`、`make test-ci` 全绿；`git diff` 中无导出符号变更。

### 阶段 2：拆包，保持导出兼容（中风险）

1. 按第五节建立子包；`internal/platform` 保留 `doc.go` + 转发层（`type Config = config.Config` 等别名），兼容一个版本。
2. `async_nats.go` 迁入 `platform/async`。
3. 门禁更新：`PLATFORM_MAX_FILES` 下调到 ≤ 10（仅剩聚合与转发文件）。

**验收**：`make check-layering`、`make check-config`、`make check-outbox`、`make check-resilience` 全绿；`cmd/*` 无编译改动。

### 阶段 3：域配置下沉 + 显式装配（需 ADR，破坏性）

1. 提交 ADR：域配置归属与 `deps.Config` 迁移路径（`deps.Config.Identity` → `deps.IdentityConfig` 或域 runtime 自持）。
2. 平台层删除域配置结构体与域判定方法；`AuditConfig.EffectiveStore` 改为装配期决策（由 `cmd` / 域 runtime 计算）。
3. `TenantGuard` / `TenantMembership` / `QuotaGuard` 端口下沉到 `internal/tenant/app`；平台只保留「鉴权链上可插入一个 `func(http.Handler) http.Handler`」的通用装配点。
4. 装配顺序改为显式声明（每个阶段声明 `Requires` 或在构造函数中返回前置依赖），消除「注释约定顺序」。
5. 门禁更新：`LAYER_SEMANTIC_BASELINE` 清空。

**验收**：`make ci` 全绿；ADR 已合入；`check-layering` 三条新规则无 BASELINE 输出。

---

## 七、门禁补齐（本次已落地）

[check-layering.sh](../scripts/ci/check-layering.sh) 新增三条规则，使 PLAT-15 从文档条款变为可执行门禁：

| 规则 | 检查内容 | 失败条件 |
|---|---|---|
| LAYER-07 | `internal/platform` 顶层非测试 `.go` 文件数 | > 21（基线，目标 ≤ 20） |
| LAYER-08 | 每个非测试文件行数 | > 500 且不在 `LAYER_LINE_BASELINE` |
| LAYER-09 | 平台层是否定义租户/配额/商户语义 `interface` | 出现不在 `LAYER_SEMANTIC_BASELINE` 的新端口 |

基线登记（只减不增，整改时逐条删除）：

| 基线项 | 当前值 | 移除条件 |
|---|---|---|
| `PLATFORM_MAX_FILES` | 4（阶段 2 为 7，阶段 3 为 5） | 兼容层 `aliases.go` 删除后再次下调；只减不增 |
| `LAYER_LINE_BASELINE` | 空 | 阶段 1 拆分 `config.go` 后已清空 |
| `LAYER_SEMANTIC_BASELINE` | 空 | 阶段 3 端口下沉到 `internal/tenant/app` 后已清空 |

基线策略遵循「显式登记、禁止静默忽略」：未登记的违规直接失败，登记项在每次运行时打印 `BASELINE` 行，确保债项始终可见。

---

## 八、风险与需 ADR 的点

| 风险 | 说明 | 缓解 |
|---|---|---|
| 域配置下沉是破坏性变更 | `cmd/*` 与各 runtime 均通过 `deps.Config.Xxx` 读取域配置 | 阶段 2 用类型别名保留路径一个版本；阶段 3 走 ADR 并提供迁移清单 |
| 隐性顺序改显式会暴露历史时序假设 | 例如「配额守卫必须在租户守卫内层」 | 阶段 1 只做命名拆分，不改顺序；阶段 3 为每个顺序假设补一条单测 |
| 门禁基线可能被后续 MR 上调 | 规范只写了「必须拆分」，没写「基线不可上调」 | 已在脚本头注释与本报告第七节明确「只减不增」，并在 review checklist 中核对 |
| `httpx` 双包改名影响面 | `internal/shared/httpx` 被 transport 大量引用 | 已更名为 `internal/shared/apiresp`，全量引用更新，无转发别名 |

---

## 九、附录：度量口径

- 行数：`[System.IO.File]::ReadAllLines()` 计数（含空行），与 `check-layering.sh` 的 `awk 'END{print NR}'` 口径一致。
- 文件计数：`internal/platform` 顶层 `*.go` 排除 `*_test.go`；子包（`dependency/`、`secrets/`）不计入本报告的包体积指标。
- 测试行数：`internal/platform/*_test.go` 合计 5011 行。
- 配置测试分布：`config_*_test.go` 共 11 个文件、3135 行，占 platform 测试总量 63%。

---

## 十、整改执行状态（2026-11-07）

阶段 1～3 已按第六节路线实施，决策记录见 [ADR-0011](adr/0011-platform-boundary-convergence.md)。

| 项 | 状态 | 证据 |
|---|---|---|
| P0-1 `config.go` 巨石 | 完成 | 拆为 `internal/platform/config/` 18 个文件，单文件 ≤ 500 行；`LAYER_LINE_BASELINE` 已清空 |
| P0-2 平台承载业务语义（配置） | 完成 | 域配置迁入 `internal/{identity,iam,audit,tenant}/config` 并自持默认值/校验；`platform/config` 只保留平台段加载与校验，`cmd/internal/appconfig` 负责组合装配 |
| P0-3 `Run` god function / 隐式顺序 | 完成 | `Run` 拆为 `prepareInfra → buildAsync → buildGuards → buildAuthN → buildAuthorizer → buildRouter → serve`；步骤内显式检查前置依赖 |
| P1-4 业务语义端口在平台层 | 完成 | `GuardPort` / `MembershipPort` 下沉到 `internal/tenant/app`；平台只保留 `httpserver` 通用守卫中间件与 `AfterAuth`；`LAYER_SEMANTIC_BASELINE` 已清空 |
| P1-5 `Config.secretErr` | 完成 | `pkg/config` 增加 `Normalize` 钩子并包装为 `ErrInvalidConfig`；`normalize` / `resolveSecrets` 直接返回 error，删除 `secretErr` 隐状态 |
| P1-6 `httpx` 双包 | 完成 | `internal/shared/httpx` 更名 `internal/shared/apiresp`，全仓 33 处引用更新，无转发别名 |
| P2-7 / P2-8 | 完成 | `platform/dependency` 拆为 `spec` / `registry` / `caller` / `breaker` / `http`，`Registry.HTTP` 复用 `pkg/httpclient` 重试并叠加熔断；配置测试按归属迁入域包与 `cmd/internal/appconfig` |
| 域配置完全自持 | 完成 | `platform.Config` 移除 `Identity` / `IAM` / `Audit` / `Tenant` 字段；`cmd/internal/appconfig` 负责组合加载与编排校验；各域 runtime 显式接收域配置；平台顶层域配置别名已删除 |

兼容性：本批为阶段 3 的破坏性切换，不再保留域配置类型别名；`platform` 顶层只保留核心段别名，`cmd/*` 统一经 `cmd/internal/appconfig` 装配，域 runtime 通过构造参数接收域配置。

### 10.2 横切目录复评追加整改（v0.2，2026-11-07）

复评对象：[platform-shared-security-review.md](./platform-shared-security-review.md) 的差距清单 P1 项。

| 差距 | 状态 | 证据 |
|---|---|---|
| G1 `shared/audit` 混入落库 / 投递实现 | 完成 | 出口实现迁至 `internal/platform/audit`（`emitter.go` / `sql.go` / `outbox.go`）；`shared/audit` 只剩 `Event` + 字段字典 + `Emitter` 端口，import 列表无 `pkg/{mq,outbox,retry,db}`；`check-audit.sh` 新增 AUD-03b 契约纯度断言，15 个调用点更新 |
| G2 `pkg/testkit` 反向 import `internal/identity` | 完成 | MySQL 夹具迁至 `internal/testdb`（`MySQL` / `Migrate`），`pkg/testkit` 只保留不感知业务的 env / Redis / 对象存储 / 仓库根；14 个测试文件改 `testdb.*`；`check-layering.sh` 新增 LAYER-10 断言 `pkg` 不得 import `internal/*` |
| G3 分层门禁覆盖不全 | 完成 | LAYER-08 改为递归遍历全部子包（阈值 500 行，基线为空）；`async_nats.go`(622)、`secrets/vault.go`(619) 已拆分；`.golangci.yml` 增加 depguard 四条规则（LAYER-01/03/04/10），已用探针文件证伪拦截生效 |
| G5 数据范围字面值靠注释同步 | 完成 | `internal/iam/domain/scope_contract.go` 在 `init` 中断言 `DataScopeType` 与 `shared/authz` 常量逐字一致，漂移即启动失败 / 测试失败 |

同时补充：[ARCHITECTURE.md](./ARCHITECTURE.md) §3.4 新增选层决策表（pkg / shared / platform / 域），消除「近义两层」的选层歧义（差距 G4）。

残留（未在本批处理）：`configs/` 仅 `prod/` 一套样板，环境差异见 CONFIG_STANDARDS 的环境矩阵（差距 G8，已在文档层面关闭）。差距 G7 已完成：`platform/aliases.go` 兼容层删除，`cmd` 侧改为直接 import 子包，`PLATFORM_MAX_FILES` 降至 4。
