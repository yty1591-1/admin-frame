# platform / shared / security 三目录结构与工程规范评审

> **归档**：本文件已于 2026-11-07 归档，只读不再维护；现行结论以 [`ARCHITECTURE.md`](../../ARCHITECTURE.md) 为准，归档索引见 [`archive/README.md`](../README.md)。

> **状态**：评审完成（v1.0，2026-11-07）＋ P1 差距整改完成（v1.1，2026-11-07，见第四节）
> **范围**：`internal/platform/`、`internal/shared/`、`internal/security/`（含其子包）
> **方法**：仅基于仓库现有目录、源码与门禁脚本实测；不假设未出现的技术栈
> **关联**：[ARCHITECTURE.md](../../ARCHITECTURE.md)、[platform-architecture-review.md](./platform-architecture-review.md)、[scripts/ci/check-layering.sh](../../../scripts/ci/check-layering.sh)

---

## 零、实测基线

| 指标 | platform | shared | security | 合计 |
|---|---|---|---|---|
| 非测试 `.go` 文件 | 44 | 15 | 6 | 65 |
| 测试 `.go` 文件 | 28 | 7 | 7 | 42 |
| 非测试代码行 | 5891 | 1171 | 1067 | 8129 |
| 测试代码行 | 3633 | 730 | 1276 | 5639 |
| 子包数 | 9 | 5 | 0 | 14 |

`bash scripts/ci/check-layering.sh` 实测全绿（LAYER-01～09 全部 OK，platform 根包非测试文件 5/5）。

---

## 一、三个目录的定位、职责与协作关系

### 1.1 总览

| 目录 | 位置 | 一句话定位 | 层性质 |
|---|---|---|---|
| `internal/platform` | `internal/platform/` | 进程级运行时装配层：把配置、日志、指标、追踪、HTTP 服务、异步、存储装配成一个可运行进程 | 基础设施 / 装配（最外层，由 `cmd/*` 调用） |
| `internal/security` | `internal/security/` | 端别绑定的认证与授权**实现**：令牌校验、权限点判定、主体注入 | 横切实现（被 platform 调用，不反向依赖） |
| `internal/shared` | `internal/shared/` | 跨端、跨域共享的**契约**：错误码、响应信封适配、Principal、鉴权快照类型、审计事件模型 | 横切契约（最内层，可被所有层引用） |

三者与业务域（`internal/{identity,iam,tenant,audit,notification,featureflag}`）、可复用库（`pkg/`）、组合根（`cmd/`）共同构成"契约 → 实现 → 装配 → 组合"四段式。

### 1.2 `internal/platform/`：进程级装配层

**职责**：只做基础设施接线与进程生命周期管理，不含业务规则（`doc.go` 明确声明）。

**主要内容**（9 个子包 + 5 个根文件）：

| 位置 | 内容 | 规模 |
|---|---|---|
| `bootstrap.go` | `Run()` 七步装配、`Deps` 能力集合、`prepareInfra` / `serve`、优雅关闭 | 483 行 |
| `config/` | 平台级配置 schema、默认值、归一化、校验、脱敏、密钥解码、Vault/文件/环境变量密钥链 | 15 个非测试文件 |
| `httpserver/` | Server、中间件管线（固定 11 段顺序）、租户/配额守卫插入点、探针端点 | 5 个文件 |
| `async/` | 异步运行时：DB / NATS JetStream 双驱动、Outbox Relay、消费与指标 | 3 个文件 |
| `dependency/` | 出站依赖注册表：超时 / 重试 / 熔断 + 依赖指标 | 含 `observability/` 子包 |
| `secrets/` | 密钥提供者链：env → file → Vault | 4 个文件 |
| `storage/`、`observability/`、`lifecycle/`、`authn/` | 对象存储集合、日志、后台任务收集、鉴权中间件工厂 | 各 1～2 个文件 |
| `aliases.go` | 拆包后的导出兼容层（类型别名 + 常量转发） | 已删除（2026-11-07）：`cmd` 改为直接 import 子包 |
| `database.go`、`id.go`、`doc.go` | 数据库构造、分布式 ID、包说明 | — |

**协作关系**：被 `cmd/{admin-api,biz-api,c-api,worker,migrate}` 与 `cmd/internal/*runtime` 调用；向 `shared`（14 处）、`security`（5 处）、`pkg` 单向依赖；不 import 任何业务域实现。

**架构作用**：整个系统唯一的"进程骨架"提供者。它决定了配置从哪来、请求链路怎么串、依赖失败怎么退化、进程怎么优雅退出——即所有业务域共享的运行时契约。

### 1.3 `internal/security/`：认证授权实现层

**职责**：令牌校验、权限点判定、Principal 注入；**不含**角色→权限表（由 `cmd` 注入 `rbac.Checker`），**不含**登录/刷新/撤销的账号域逻辑。

**主要内容**（6 个非测试文件）：

| 文件 | 内容 | 行数 |
|---|---|---|
| `jwt.go` | JWTAuth：HS256/RS256、密钥轮换（多密钥只验不签）、iss/aud/exp 校验、撤销检查器注入 | 523 |
| `authz.go` | Authorizer：把 `pkg/rbac.Checker` 适配为 HTTP 权限中间件；off/shadow/enforce 三模式 | 316 |
| `snapshot.go` | `SnapshotLoader` 消费侧端口、`Mode` 解析、差异结构 | 104 |
| `demo.go` | 演示令牌解析（`admin.<id>` / `biz.<mid>.<id>` / `c.<uid>`） | 92 |
| `principal.go`、`doc.go` | context 注入辅助、包说明 | 32 |

**协作关系**：依赖 `shared/{auth,authz,apperr,apiresp}` 与 `pkg/{rbac,jwt,errors,contextx}`；被 `platform/authn` 与 `platform/bootstrap` 调用；**不** import `platform` 或任何业务域（LAYER-03 实测通过）。

**架构作用**：安全边界的唯一落点。它把 `pkg` 层的通用能力（JWT 原语、RBAC 判定器）收敛为"按端别绑定"的中间件，并通过 `SnapshotLoader` 端口实现与 `internal/iam` 的解耦——IAM 变化不必改安全层。

### 1.4 `internal/shared/`：跨端共享契约层

**职责**：定义跨端、跨域共享的类型与最小适配，避免各域各自造轮子。

**主要内容**（5 个子包，15 个非测试文件）：

| 子包 | 内容 | 性质 |
|---|---|---|
| `apperr/` | `AppError` + 稳定业务数字码表（如 40400），227 行 | 契约（类型 + 码表） |
| `apiresp/` | apperr → 统一响应信封的适配；`Envelope` 为 `pkg/httpx.Envelope` 别名，74 行 | 适配（薄转发） |
| `auth/` | `Principal`、`Channel` 常量、`PermissionMiddleware` 签名，83 行 | 纯契约 |
| `authz/` | `Snapshot`、`Scope` 类型与 context 传递，279 行 | 纯契约 |
| `audit/` | `Event`、字段字典、`actions.go` 事件目录 + **Emitter / SQLStore / OutboxEmitter 实现**，473 行 | 契约 + 实现（混合） |

**协作关系**：被 98 个文件引用（其中 `shared/audit` 被全部 7 个 `cmd/internal/*runtime` 与 `internal/audit/app` 引用）；只依赖 `pkg/*` 与自身子包；不依赖 `platform` 与任何业务域（LAYER-04 实测通过）。

**架构作用**：语义统一器。"错误码怎么编号、响应信封长什么样、Principal 有哪些字段、审计事件怎么定义"这些跨域问题只在这里回答一次；同时它是业务域与 `pkg` 之间的缓冲带——业务域写 `apperr.NotFound()`，不必知道底层用 `pkg/errors` 的哪个 Code。

### 1.5 依赖流向（实测）

```
cmd/{admin-api,biz-api,c-api,worker,migrate}
cmd/internal/{appconfig,identityruntime,iamruntime,tenantruntime,auditruntime,notificationruntime,featureflagruntime}
        │  组合根：读配置 + 构造业务依赖 + 注册路由
        ▼
internal/platform  ──►  internal/security  ──►  internal/shared  ──►  pkg/*
        │                     │                      ▲
        └─────────────────────┴──────────────────────┘
        │
        ✗ 不依赖 internal/<业务域>（LAYER-02 通过）
internal/<业务域>/{domain,app,infrastructure,transport}
        └─ transport 只依赖 shared 契约，不碰 platform（LAYER-01 通过）
```

方向一致性是本项目最扎实的一点：**契约在内、实现在中、装配在外、组合在顶**，且由脚本硬门禁守护。

---

## 二、八维度评估

| 维度 | 判断 | 关键依据 |
|---|---|---|
| 分层清晰度 | **达标** | 四段式职责在 `doc.go` 与 ARCHITECTURE §5.6/§6.1 双处声明；LAYER-01～06 实测全绿；`platform` 根包已从 21 文件 / 3375 行拆到 4 文件 / 498 行 |
| 模块边界 | **基本达标** | `security` 通过 `SnapshotLoader` 端口与 IAM 解耦；`platform` 通过 `Deps` 只暴露能力不暴露 repo；**但** `shared/audit` 混入 SQL 与 Outbox 实现，边界外溢 |
| 职责单一 | **达标** | 单文件最高 622 行（`async_nats.go`）；`authn/` 仅 44 行只做模式分派；`apiresp` 74 行只做转发，未见 god object |
| 命名与依赖管理 | **基本达标** | 包名语义清晰、全量 `doc.go` 中文声明依赖方向；`deps/` 有许可证/漏洞/replace 白名单；**但**存在 `pkg/errors↔shared/apperr`、`pkg/httpx↔shared/apiresp`、`pkg/rbac↔shared/authz` 三组近义命名，且 `pkg/testkit` 反向 import `internal/identity` |
| 可维护性 | **达标** | 拆包已完成、兼容层已删除；`cmd` 直接 import `platform/<subpkg>`，平台根只保留装配入口 |
| 可测试性 | **达标** | 测试代码 5639 行 ≈ 生产代码 69%；`security` 测试行数（1276）超过生产行数（1067）；三目录均有 `testsupport_test.go` / `testhelpers_test.go`；`go test -race` + coverprofile |
| 配置与环境隔离 | **基本达标** | 密钥不落配置文件，走 env/file/Vault 链；有 `config_redaction_test.go` 校验启动日志不泄密；`auth.mode=demo` 在 `env=prod` 时启动即失败；**但** `configs/` 仅有 `prod/` 一套样板，无 dev/staging 目录 |
| 文档及部署就绪度 | **基本达标** | 20 份硬约束专文 + ADR + runbook + `deploy/helm` + 三套 values + 迁移 Job + `make ci` 40+ 检查；**但** `docs/archive/reviews/platform-architecture-review.md` 仍描述拆包前状态，未回写 |

### 逐项说明（只在需要证据时展开）

**分层清晰度**：这是本轮最强项。`check-layering.sh` 不只检查 import 字符串，还检查"包体积基线只减不增"（`PLATFORM_MAX_FILES=4`）和"platform 不得定义业务语义端口 / 函数字段"（`Tenant` / `Quota` / `Merchant` 命名的 `interface` 与 struct `func` 字段，LAYER-09 / 09b）。把架构约束写成可执行断言、并禁止扩容基线，是企业级治理的标志性做法。

**模块边界的两处漏点（均已于 2026-11 关闭）**：一是 `shared/audit` 契约与实现混层（G1，实现已迁至 `internal/platform/audit`）；二是 `platform.Deps` 的 `VerifyTenant` / `IsTenantMember` 用函数签名绕过 LAYER-09 正则（已移除，绑定由 `cmd/internal/runtimebind.Tenant` 在装配层显式传递，并新增 LAYER-09b 拦截 struct `func` 字段）。

**依赖管理**：`go.mod` 无 `replace`（`deps/replace-allowlist.txt` 默认为空），许可证白名单 + 漏洞例外带到期日 + SBOM，这套供应链治理超出多数团队水平。扣分点在于**分层检查是 shell grep 而非编译期规则**——ARCHITECTURE §463 自己建议用 depguard，`.golangci.yml` 目前启用了 gosec/gocritic/revive 等 8 个 linter，但没有 depguard。

**可测试性**：三目录测试密度高且类型齐全（单测 + internal 测试 + `testsupport` 夹具）。缺口是三件套本身没有独立覆盖率阈值——`check-test-gates.sh` 的 70% 门槛只作用于 `internal/identity/transport/admin`。

---

## 三、结论

### 3.1 明确结论

**符合**（企业级 / 生产级基线达标）。

判断依据：四层职责与依赖方向有文档定义且由 CI 硬门禁实测通过；单包/单文件规模已被压到规范阈值内；测试密度、供应链治理、部署产物（Helm / 迁移 Job / 备份 CronJob）齐备。这套结构可以作为团队内新业务域的参照实现。

但需限定：若以"零遗留债"为尺度，则**两个横切目录处于"部分符合"**——`internal/shared` 的契约纯度、`pkg` 与 `internal` 的依赖方向各有一处实质性偏差（G1、G2）。这两项不影响当前可运行性，但会随域数量增长放大。

### 3.2 差距清单

| 编号 | 差距 | 证据 | 风险 | 建议 |
|---|---|---|---|---|
| **G1** | `shared` 契约层混入实现 | `shared/audit/` 含 `sqlstore.go`(100) + `outbox.go`(103) + `emitter.go`(55) + `actions.go`(111)，并 import `pkg/{mq,outbox,retry}`；与 ARCHITECTURE §458「只放类型，不放实现」冲突 | 任何引用 `shared/audit` 的域都被拖入异步栈依赖；契约层随存储实现变动而变动，98 个引用方受影响 | 拆为两处：`shared/audit` 只留 `Event` / 字段字典 / `Emitter` 接口；写入实现（SQLStore / OutboxEmitter / MultiEmitter）迁至 `internal/platform/audit` 或 `pkg/audit`。验收：`shared/audit` 的 import 列表只剩标准库与 `shared/apperr` |
| **G2** | `pkg` 反向依赖 `internal` 业务域 | `pkg/testkit/mysql.go:17-18` import `internal/identity/bootstrap` 与 `internal/identity/infrastructure/persistence` | `pkg` 丧失"可独立复用 / 可独立发布"属性；测试工具链与业务迁移绑定，未来抽库或换仓即断 | `testkit` 的 MySQL 夹具迁至 `internal/testkit` 或 `tests/support`；`pkg/testkit` 只保留不感知业务的通用夹具。验收：`grep -rn "admin-frame/internal/" pkg` 无非测试命中 |
| **G3** | 分层门禁覆盖不全 | `check-layering.sh` 的 LAYER-07/08 只遍历 `internal/platform/*.go`（根包），子包不受约束；实际 `async/async_nats.go` 622 行、`secrets/vault.go` 619 行均 > 500 行阈值却通过 | 门禁给"绿"的假安全感，子包可无痛膨胀回巨石 | LAYER-08 改为递归遍历（`find ${PLATFORM_DIR} -name '*.go'`）；`.golangci.yml` 增加 depguard 规则，把六条分层规则编译期化 |
| **G4** | 近义命名与薄适配层双份维护 | `shared/apiresp`(74 行，转发 + 别名 `pkg/httpx`)、`shared/apperr`(227 行，在 `pkg/errors` 上挂数字码)、`pkg/rbac`↔`shared/authz`+`security/authz`、`pkg/config`↔`platform/config` | 新人选层成本高；同一个语义有两处定义，改一处漏一处即漂移 | 在 `docs/ARCHITECTURE.md` 增"选层决策表"（何时写 `pkg`、何时写 `shared`）；给 `apiresp` 加契约测试锁定 `Envelope` JSON 字段与 `pkg/httpx` 一致 |
| **G5** | 跨层字面值靠注释同步 | `shared/authz` 的 `ScopeAll/Merchant/Self/MerchantSet` 与 `internal/iam/domain.DataScopeType` 靠注释约定"不得改名" | 改名时无编译期报错，鉴权范围静默失效——安全影响 | 在 `iam/domain` 侧加 `var _ = [1]struct{}{}[authz.ScopeAll - ScopeAll]` 式编译期断言，或以代码生成从单一来源产出两侧常量 |
| **G6** | 架构文档滞后于代码 | `docs/archive/reviews/platform-architecture-review.md` 仍写"21 个非测试文件、`config.go` 3375 行、评审未整改"；实际已拆为 5 文件、`config.go` 50 行，基线同步下调至 5（commit 924bd4c / 3921abe，2026-11-07） | 评审结论失真，后续决策基于过期事实 | 回写该文状态表与度量表；在 CI 增加"基线与文档一致"检查（`PLATFORM_MAX_FILES` 与文档数字比对） |
| **G7** | 兼容层未定下线窗口 | `platform/aliases.go` 157 行别名；`cmd` 中 124 处 `platform.X` 引用 | 拆包收益被过渡层稀释，两个入口并存 | 明确"下个 minor 版本移除别名"，把 `cmd` 直接 import 子包；移除后 `PLATFORM_MAX_FILES` 可再降至 4 |
| **G8** | 环境样板单一 | `configs/` 下只有 `prod/`（admin/biz/c/worker + modules/*.yaml.example + env.example） | 新人搭本地环境需自行推导 env 覆盖矩阵 | 增 `configs/dev/`、`configs/staging/` 模板，或在 `docs/CONFIG_STANDARDS.md` 补"环境差异矩阵" |

### 3.3 优先级

- **P1（本迭代）**：G1、G2、G3 —— 都是"门禁看不见但结构会腐化"的类型。
- **P2（下迭代）**：G4、G5、G6、G8 —— 认知与治理成本，不阻塞交付。
- **P3**：G7 —— 纯清理，随下次拆包一并做。

### 3.4 一句话总结

三目录的分层设计、依赖方向与门禁体系已经达到企业级水准，**结论为"符合"**；真正需要修的不是"怎么分层"，而是**两处边界渗漏（`shared` 装实现、`pkg` 装业务）与一处门禁盲区（子包不受体积约束）**——把这三处补上，这套结构就能支撑域数量翻倍。

---

## 四、整改执行状态（v1.1，2026-11-07）

| 差距 | 优先级 | 状态 | 落地内容与验收证据 |
|---|---|---|---|
| **G1** `shared` 装实现 | P1 | **已关闭** | 出口实现迁至 `internal/platform/audit/{emitter,sql,outbox}.go`；`shared/audit` 只剩 `Event` / `actions` / `Emitter` 接口。验收：`grep -rE 'pkg/(mq\|outbox\|retry\|db)' internal/shared/audit` 为空；`check-audit.sh` 新增 **AUD-03b** 契约纯度断言（现为 OK）；15 个调用点改 `platformaudit.*`；`go test ./internal/{platform,shared}/audit/...` 通过 |
| **G2** `pkg` 反向依赖 | P1 | **已关闭** | MySQL 夹具迁至 `internal/testdb`（`MySQL` / `Migrate`），`pkg/testkit` 只留 env / Redis / 对象存储 / 仓库根；14 个测试文件改 `testdb.*`。验收：`grep -rn 'admin-frame/internal/' pkg` 为空；`check-layering.sh` 新增 **LAYER-10** 断言（现为 OK） |
| **G3** 门禁覆盖不全 | P1 | **已关闭** | LAYER-08 改为 `find` 递归全部子包（基线为空）；`async_nats.go`(622)→ 256+252+143 三文件，`secrets/vault.go`(619)→ 419+211。`.golangci.yml` 增 depguard 四条规则（LAYER-01/03/04/10），`files` 排除 `_test.go` 与脚本口径一致。验收：把阈值临时降到 200 行能报出 8 个超限文件（证伪有效）；探针文件在 `pkg/` 与 `transport/` 下均被 depguard 拦截 |
| **G4** 近义命名歧义 | P2 | **已关闭** | `ARCHITECTURE.md` §3.4 新增选层决策表（pkg / shared / platform / 域），并写明 `apiresp` 是 `pkg/httpx` 的类型别名、`apperr` 建在 `pkg/errors` 之上、`rbac+authz+security` 是三层协作 |
| **G5** 字面值靠注释同步 | P2 | **已关闭** | `internal/iam/domain/scope_contract.go`：`init` 断言 `DataScopeType` 与 `shared/authz` 常量逐字一致。验收：把 `ScopeSelf` 改成 `self_only` 后测试立即 `panic: 数据范围契约不一致`，已还原 |
| **G6** 评审文档滞后 | P2 | **已关闭** | `platform-architecture-review.md` 升 v0.2：状态行、结论摘要表（3 项不达标 → 达标）、度量表新增「v0.2 复评」列；新增 §10.2 记录本批复评整改 |
| **G7** 兼容层无下线窗口 | P3 | **已关闭** | `platform/aliases.go` 已删除；`cmd` 与测试改为直接 import 子包，`PLATFORM_MAX_FILES` 降至 4；`WithTenantGuard` / `WithQuotaGuard` 迁入 `platform/httpserver`，`NewAsyncRuntime` 留在平台根装配层 |
| **G8** 环境样板单一 | P2 | **已关闭** | `CONFIG_STANDARDS.md` §6.1 新增「环境差异矩阵」：11 个键在 local/dev/staging/prod 的取值 + 派生本地配置的做法 + `.gitignore` 只允许 `*.yaml.example` 入库 |

复验命令与结果：`go build ./...`、`go vet ./...`、`gofmt -l` 全空；`go test ./internal/platform/... ./internal/shared/... ./internal/security/... ./internal/iam/domain/...` 全部 ok；`check-layering.sh` LAYER-01～10 全绿；`check-audit.sh` 含 AUD-03b 全绿。
