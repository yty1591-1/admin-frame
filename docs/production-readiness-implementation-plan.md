# 生产级能力缺口实施规划（Production Readiness Implementation Plan）

> **状态**：规划生效（v0.1，2026-10-22）；上游基线 `production-readiness-gaps.md` v0.2
> **定位**：把 `ENT-01`～`ENT-25` 的差距评估转化为**可执行、可验收、可回滚**的任务包；只规划平台级能力，不新增业务域功能
> **效力**：本文件的任务范围、验收标准与门禁对合入具有约束力；与 [`platform-remediation-plan.md`](./platform-remediation-plan.md) 同级
> **适用范围**：`internal/`、`pkg/`、`cmd/`、`migrations/`、`deploy/`、`tests/`、`scripts/ci/`、`.github/`、`docs/`
> **关联**：[`production-readiness-gaps.md`](./production-readiness-gaps.md)、[`ARCHITECTURE.md`](./ARCHITECTURE.md)、[`standards-roadmap.md`](./standards-roadmap.md)、[`platform-remediation-plan.md`](./platform-remediation-plan.md)、[`rbac-iam.md`](./rbac-iam.md)、[`CODE_STYLE.md`](./CODE_STYLE.md)、[`TESTING_STANDARDS.md`](./TESTING_STANDARDS.md)、[`CI_STANDARDS.md`](./CI_STANDARDS.md)、[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)、[`CONFIG_STANDARDS.md`](./CONFIG_STANDARDS.md)、[`DATA_STANDARDS.md`](./DATA_STANDARDS.md)、[`RELIABILITY_STANDARDS.md`](./RELIABILITY_STANDARDS.md)、[`OBSERVABILITY_STANDARDS.md`](./OBSERVABILITY_STANDARDS.md)、[`AUDIT_STANDARDS.md`](./AUDIT_STANDARDS.md)、[`API_STANDARDS.md`](./API_STANDARDS.md)、[`RELEASE_STANDARDS.md`](./RELEASE_STANDARDS.md)、[`RUNBOOK_STANDARDS.md`](./RUNBOOK_STANDARDS.md)、[`GIT_STANDARDS.md`](./GIT_STANDARDS.md)
> **执行方式**：每条 `ENT` 独立 MR；验收标准全绿 + `make ci` + 本条新增门禁通过 + 文档回写完成，方可标记关闭
> **最后更新**：2026-10-22
> **变更方式**：调整波次、范围或验收标准须同步更新「十二、状态跟踪」与「执行记录」，经架构 Owner Review；P0 关闭须附验收命令与输出

---

## 目录

1. [规划基线与目标](#一规划基线与目标)
2. [总体实施路线](#二总体实施路线)
3. [工程约束基座](#三工程约束基座)
4. [P0 任务包（ENT-01～ENT-08）](#四p0-任务包ent-01ent-08)
5. [P1 任务包（ENT-09～ENT-18）](#五p1-任务包ent-09ent-18)
6. [P2 任务包（ENT-19～ENT-25）](#六p2-任务包ent-19ent-25)
7. [门禁建设计划](#七门禁建设计划)
8. [里程碑与验收](#八里程碑与验收)
9. [文档回写矩阵](#九文档回写矩阵)
10. [风险登记与回滚](#十风险登记与回滚)
11. [协作与评审机制](#十一协作与评审机制)
12. [状态跟踪与执行记录](#十二状态跟踪与执行记录)
13. [附录](#附录)

---

## 一、规划基线与目标

### 1.1 上游输入

| 项 | 内容 |
|---|---|
| 差距评估 | [`production-readiness-gaps.md`](./production-readiness-gaps.md) v0.2（2026-10-22） |
| 代码基线 | `dad8fc0`（工作区干净；演示业务域已清理） |
| 已完成的平台整改 | `platform-remediation-plan.md` PLAT-01～PLAT-15 全部完成 |
| 已完成的 IAM | `rbac-iam.md` M0～M4.7；M5 生产加固进行中 |
| 现有门禁 | `make ci` 23 个 target；`scripts/ci/` 20 个检查脚本 |
| 现状结论 | 工程治理接近企业级，运行时能力仍是「单进程 + 内存 + 手工运维」骨架 |

### 1.2 规划目标

1. **可执行**：每个 `ENT` 拆成有明确产出、变更路径、验收命令的任务包，避免「大而空」的改造项。
2. **可验收**：每条验收标准必须能落到自动化用例、门禁脚本或 staging 演练记录，不接受「代码写完即完成」。
3. **可回滚**：每条能力上线前明确开关、回退路径与数据回滚方式，失败默认安全。
4. **不重复建设**：优先复用 `pkg/*` 原语、IAM 已验证的 Outbox / UnitOfWork / 缓存模式，以及现有 `internal/platform` 装配入口。
5. **不过度设计**：严格执行差距评估 §7.3「明确不做」，P2 仅在触发条件成立后启动。

### 1.3 成功判据

| 阶段 | 判据 | 目标等级 |
|---|---|---|
| **E1 可生产灰度** | `ENT-01`～`ENT-08` 全部关闭；staging 多副本可运行；真实身份、异步底座、审计、备份、告警、部署闭环；`make ci` + 新增门禁全绿 | L2 |
| **E2 可企业运营** | `ENT-09`～`ENT-18` 全部关闭；租户、通知、开关、数据范围、测试、发布、值班闭环 | L2+ |
| **E3 规模化与合规** | `ENT-19`～`ENT-25` 按触发条件落地；有 SLO、演练记录与容量模型 | L3 |

### 1.4 不在规划范围

| 项 | 原因 |
|---|---|
| 任何业务域功能（订单、文件、营销等） | 本规划只补平台底座；业务域另行立项 |
| 微服务拆分、服务网格、多活 | 当前规模不成立，触发条件见 `ARCHITECTURE.md` §5.6 与差距评估 §7.3 |
| 自建完整 IdP | 身份域只做账号映射与会话策略；优先复用组织 OIDC |
| ABAC 表达式引擎、组织架构树 | 差距评估 `ENT-25` 明确暂缓，先立 ADR |
| 低流量阶段引入 Kafka | 先用 DB Outbox 轮询满足可靠性，容量触发后再换 broker |

### 1.5 不可违背的既存约束

以下约束来自现有架构与标准，规划与实施均不得突破：

| 约束 | 来源 | 落地检查 |
|---|---|---|
| API 进程**禁止**自动执行数据库迁移 | `DATA_STANDARDS`、`check-migrations.sh` | 迁移只允许 `cmd/migrate` 与部署 Job 执行 |
| 生产 `iam.mode` 必须为 `enforce` | 项目硬约束、配置 `Validate` | `check-config` + 启动校验 |
| 高风险 IAM 直写在 `iam.approval.enabled=true` 时必须转审批单 | IAM M5 实现 | 单测 + 配置校验 |
| 生产 IAM 必须使用持久化数据库驱动 + Redis 缓存 + Outbox | 配置 `Validate` | 启动失败关闭 |
| 禁止明文密钥进入仓库、日志、镜像 | `SECURITY_STANDARDS` SEC-01/05 | gitleaks + 配置脱敏 + 镜像扫描 |
| 三端身份隔离（issuer / audience / 密钥） | `SECURITY_STANDARDS` SEC-04 | 跨端令牌必须 401 |
| `transport` 不得依赖 `internal/platform`；`platform` 不得依赖业务域 | `ARCHITECTURE.md` §6.1 | `check-layering` + depguard |
| 演示域代码不得回流 | 项目硬约束 | 新增包不得出现 `order` / `file` 演示语义 |

---

## 二、总体实施路线

### 2.1 四条主线

```text
主线 A｜运行时与身份：ENT-02 → ENT-03 → ENT-01 → ENT-04
主线 B｜交付与运维：  ENT-05 → ENT-06 → ENT-07 → ENT-08
主线 C｜企业运营：    ENT-09 → ENT-14 → ENT-12 → ENT-10 / ENT-11 / ENT-13 / ENT-15
主线 D｜质量与治理：  ENT-16 → ENT-17 → ENT-18 → ENT-19 ～ ENT-25
```

四条主线可并行，但同一波次内的依赖必须按 §2.3 的顺序合入。

### 2.2 波次划分

| 波次 | 范围 | 出口标准 | 备注 |
|---|---|---|---|
| **Wave 0 基座准备** | `ENT-16` 前置切片（`pkg/testkit` + `tests/integration` 骨架 + docker compose 依赖）、门禁脚本骨架、配置示例扩充 | 本地一条命令拉起 MySQL / Redis / MinIO；集成测试可运行；新增门禁可空跑 | 不改变运行时语义，先让后续任务「可验证」 |
| **Wave 1 运行时闭环** | `ENT-02`、`ENT-03`、`ENT-01`、`ENT-04` | 多副本 ID / 幂等 / 限流共享；通用 Outbox 与消费运行时；登录 / 刷新 / 登出 / 撤销；审计可查询 | 主线 A，E1 核心 |
| **Wave 2 交付闭环** | `ENT-05`、`ENT-06`、`ENT-07`、`ENT-08` | Helm 三环境可渲染；告警 / 看板 / SLO 代码化；密钥托管与轮换；备份与恢复演练 | 主线 B，可与 Wave 1 并行 |
| **Wave 3 企业运营** | `ENT-09`～`ENT-15` | 租户、数据范围、通知、开关、韧性、配额、数据生命周期 | 主线 C，E2 |
| **Wave 4 规模化治理** | `ENT-17`、`ENT-18`、`ENT-19`～`ENT-25` | CD、值班、供应链、边缘、容量、多区域按需 | 仅触发条件成立后启动 |

### 2.3 依赖关系

```text
ENT-16（前置切片）
   └─► 所有 P0 的验收自动化

ENT-02 多实例语义 ──┬─► ENT-03 异步底座 ──► ENT-10 通知投递
                    ├─► ENT-14 分布式限流 / 配额
                    └─► ENT-17 发布自动化

ENT-01 身份闭环 ────┬─► ENT-09 租户生命周期
                    └─► ENT-12 数据范围契约

ENT-05 部署制品 ────┬─► ENT-06 告警 / SLO
                    ├─► ENT-07 密钥托管
                    └─► ENT-08 备份 / 灾备

ENT-04 审计持久化 ──► ENT-15 数据生命周期（DSAR 与保留策略）
```

### 2.4 合入策略与开关

1. **一条 ENT 一个主 MR**：超过 800 行 diff 或跨 3 条主线时，拆为「契约 MR + 实现 MR」，契约先合。
2. **默认关闭**：所有新能力必须有配置开关，默认值保持现有行为；`prod` / `staging` 由 `Validate` 强制开启安全项。
3. **先影子后强制**：身份、审计、数据范围、幂等、限流按 `shadow → enforce` 两阶段推进，影子期只记录差异不拦截。
4. **不保留双写长期共存**：迁移完成后同一 MR 删除旧路径与旧开关，避免「两套语义」长期漂移。
5. **禁止跨任务借用开关**：每个开关只服务本条能力，命名带能力前缀（如 `identity.`、`async.`、`audit.`）。

### 2.5 任务关闭定义（Task DoD）

每条 `ENT` 关闭必须同时满足：

- [ ] 验收标准 `AC-ENT-xx-*` 全部通过，且有自动化用例或演练记录链接。
- [ ] `make ci` 全绿；本条新增门禁接入 CI 并验证「违规即失败」。
- [ ] 代码满足 `CODE_STYLE.md`（S-01～S-12）与分层约束。
- [ ] 新增配置有默认值、`Validate`、环境变量覆盖、样例与脱敏。
- [ ] 新增数据库变更具备 up / down、索引与回滚说明；API 进程不执行迁移。
- [ ] 新增对外接口有 OpenAPI、错误码、审计事件与指标。
- [ ] 文档回写完成（§9 矩阵），PR 描述含验收命令与输出。
- [ ] 回滚路径已验证：开关关闭或镜像回退后系统行为可恢复。

---

## 三、工程约束基座

### 3.1 新增包的目录与分层归属

| 能力 | 新增路径 | 分层职责 | 禁止事项 |
|---|---|---|---|
| 身份域 `ENT-01` | `internal/identity/{domain,app,infrastructure,transport}` | 账号、凭据、会话、MFA、登录审计 | 不得 import `internal/platform`；不得承载 IAM 权限规则 |
| 审计存储 `ENT-04` | `internal/auditlog/{app,infrastructure,transport}` | 审计落库、查询、导出、保留 | 事件模型继续复用 `internal/shared/audit` |
| 秘钥托管 `ENT-07` | `internal/platform/secrets/` | `Provider` 接口与 env/file/vault/kms 实现 | 接口定义在消费侧；实现不得被业务域直接引用 |
| 租户域 `ENT-09` | `internal/tenant/{domain,app,infrastructure,transport}` | 租户生命周期、配额、成员 | 不得复制 IAM 角色模型 |
| 通知中心 `ENT-10` | `internal/notification/{domain,app,infrastructure}` | 模板、偏好、投递编排、状态 | 渠道实现继续用 `pkg/sms` / `email` / `push` / `webhook` |
| 特性开关 `ENT-11` | `internal/featureflag/{domain,app,infrastructure,transport}` | 规则、灰度、审计、到期清理 | 不得读取业务域内部状态 |
| 异步运行时 `ENT-03` | `pkg/outbox`、`pkg/mq`、`internal/platform/async.go`、`internal/platform/async_nats.go` | Relay、消费注册、重试、DLQ、指标、JetStream 适配 | `pkg/` 不得 import `internal/` |
| 测试基座 `ENT-16` | `pkg/testkit/`、`tests/integration/`、`tests/e2e/` | 容器化依赖、测试数据、E2E 驱动 | 不得依赖固定端口、固定路径、真实云凭据 |

> 目录归属在实施时若需调整，须在本文件与 `ARCHITECTURE.md` §5 同 MR 更新；**禁止**继续把新能力塞进 `internal/platform`（PLAT-15 的拆分触发条件仍有效）。

### 3.2 代码规范（S-01～S-12）

| 约束 | 在本规划中的落地方式 |
|---|---|
| S-01 格式 | 每个 MR 执行 `make fmt-check`；禁止手调对齐 |
| S-02 命名 | 包名单数小写；`identity` 而非 `accounts`；实现 `mysqlAccountRepository`；事件 `AccountLocked` |
| S-03 签名 | 所有用例 / 仓储入口 `ctx context.Context` 第一参数，`error` 最后返回 |
| S-04 context key | 身份、租户、数据范围使用未导出 key 类型，禁止字符串 key |
| S-05 错误早返回 | 认证 / 审计失败路径一律早返回，禁止嵌套成功分支 |
| S-06 导入 | 禁止点导入；`pkg/storage/all` 类副作用导入必须注释 |
| S-07 接口位置 | `RevocationChecker`、`SecretProvider`、`OutboxStore` 接口定义在消费侧 |
| S-08 panic | 运行路径禁止 panic；仅允许 `Must*` 且须注释适用场景 |
| S-09 导出 | 禁止导出未导出类型；DTO 与领域对象分离 |
| S-10 忽略 error | `_ =` 必须注释原因；关键路径（审计、令牌撤销）禁止忽略 |
| S-11 复杂度 | 单函数 ≤ 80 行、圈复杂度建议 ≤ 15；认证 / 消费运行时逻辑拆分 |
| S-12 并发 | Relay / 消费者 goroutine 必须 `ctx` 可取消、退出可汇合；禁止请求路径裸飞 goroutine |

**测试文件**：表驱动优先；`t.Helper()`；断言失败信息包含输入与期望；集成测试必须可用 build tag 或目录隔离。

### 3.3 配置规范（CFG）

每条新增能力必须完成以下五件事，缺一不可：

1. 在 `internal/platform/config.go` 增加结构体字段与默认值。
2. 在 `Validate` 中校验枚举、范围、必填与生产强制项，使用 `errors.Join` 聚合。
3. 支持 `ADMIN_FRAME_<SECTION>_<KEY>` 环境变量覆盖。
4. 更新 `configs/*.yaml.example`，**不得**写入明文密钥。
5. 敏感字段实现 `LogValue` 脱敏；新增配置项写入 `CONFIG_STANDARDS.md` 变量表。

**生产强制项（由 `Validate` 拒绝启动）**：

| 能力 | 强制项 |
|---|---|
| 身份 | `auth.mode=jwt`、`identity.revocation.failure_mode=fail_closed`、A 端 `identity.mfa.admin_required=true` |
| 多实例 | 禁止默认 `NodeID=1`；`idempotency.driver=redis`；`rate_limit.driver=redis` |
| 异步 | `async.driver=db` 时 `async.outbox.publish_in_api=false`；`async.driver=nats` 时 `async.broker.url` 必须 `tls://`/`wss://`；`iam.mode=enforce` 时强制 `async.enabled=true` |
| 审计 | `audit.store=db`；`audit.retention_days ≥ 180`；`audit.critical_fail_closed=true` |
| 密钥 | `secrets.provider ∈ {vault, kms}`；禁止仅 env 注入生产密钥 |
| 备份 | `backup.enabled=true`；`backup.retention_days > 0` |

### 3.4 数据与迁移规范（DATA）

- 迁移文件命名沿用 `0000NN_<verb>_<object>.{up,down}.sql`，编号顺序占用，禁止跳号复用。
- 每张新表必须包含：主键、`created_at` / `updated_at`、必要唯一键与查询索引、字符集与排序规则与现有表一致。
- 审计类表只允许追加：应用账号不授予 `UPDATE` / `DELETE`；清理由独立保留期任务账号执行。
- 大表变更遵循「先加列可空 → 回填 → 加约束」三步，单条迁移不得长时间锁表。
- 迁移只由 `cmd/migrate` 或部署 Job 执行；API / worker 启动时只校验版本，不执行变更。
- 每个迁移必须有回滚说明；不可逆迁移须在 PR 描述标注并给出补偿方案。

### 3.5 审计规范（AUD）

- 新增敏感操作必须先在 `docs/audit/EVENTS.md` 登记事件目录，再实现。
- 事件字段遵循 `docs/audit/FIELDS.md`；禁止在 `attrs` 中放明文密码、令牌、身份证号等。
- 关键写入（角色 / 绑定 / 破玻璃 / 账号状态 / 密钥轮换 / 审计导出）采用「业务事务 + Outbox」；写入失败即业务失败。
- 查询与导出接口本身必须产生审计事件；导出需权限点 + 审批（如已启用审批）。
- 保留期 ≥ 180 天；法务保留（legal hold）优先于清理任务。

### 3.6 测试规范（TEST）

| 层级 | 范围 | 运行方式 | 门槛 |
|---|---|---|---|
| 单元测试 | `domain` / `app` / 中间件 / 配置 | `go test -race ./...` | 新域 `domain` 覆盖率 ≥ 70%，`identity` / `auditlog` / `platform` ≥ 80% |
| 集成测试 | 真实 MySQL / Redis / MinIO | `tests/integration/`，CI 独立 job | 关键路径必须有集成用例（登录、刷新、撤销、Outbox、审计查询） |
| 契约测试 | 三端 OpenAPI | `make check-openapi` + 新增 schema 断言 | 破坏性变更必须在 `oasdiff` 中显式声明 |
| E2E | 登录 → 核心写路径 → 查询 | `tests/e2e/`，nightly | 三端各 ≥ 1 条主链路 |
| 演练 | 多副本、故障注入、恢复 | staging 手工 + 记录归档 | 每季度至少 1 次 |

- 新增测试不得依赖固定端口、固定路径、真实云凭据；容器端口由运行时分配。
- 竞态敏感代码（Relay、限流、会话撤销）必须 `-race` 且包含并发用例。
- 覆盖率门槛只允许上调，下调须 Owner 批准并记录 ADR。

### 3.7 可观测与可靠性（OBS / REL）

- 每条新能力必须定义指标：至少包含请求量、错误量、时延或积压（RED / USE 择一）。
- 关键依赖调用必须有超时与重试策略；禁止无超时 HTTP 客户端（REL-01）。
- 限流拒绝统一返回 `42900`；依赖不可用返回 `50300`，并记录稳定错误码。
- 日志字段遵循 `OBS-01`：`trace_id`、`request_id`、`channel`、`service`、`duration_ms`。
- 新增告警必须同时更新 `docs/runbooks/ALERTS.md`，并附 runbook 链接。

### 3.8 发布与回滚（RELS）

- 镜像禁止 `latest`；staging / prod 使用 digest，镜像扫描通过方可发布。
- 迁移以 Job 在发布前执行，失败即阻断发布；发布顺序固定为「迁移 → worker → API 灰度 → 全量」。
- 每次发布必须提供回滚命令与数据回滚说明；`helm upgrade --atomic` 为默认策略。
- 高风险开关（认证强制、审计失败关闭、限流）必须具备「一键回退」的配置路径。

### 3.9 GIT 与 MR（GIT）

- 分支：`feature/ent-01-identity`、`fix/...`、`docs/...`；禁止长期个人分支。
- Commit：Conventional Commits，scope 使用 `identity`、`async`、`audit`、`deploy` 等。
- MR 描述必须包含：变更动机、影响范围、验收命令与输出、风险与回滚、文档回写清单。
- 至少 1 名非作者 Approve；P0 与安全相关变更需安全 / 架构 Reviewer。

### 3.10 安全（SEC）

- 所有新增端点默认拒绝，必须显式挂载认证与权限中间件。
- 令牌、密码、验证码、密钥不得进入日志与审计 `attrs`。
- 新增第三方依赖须通过 `make check-deps` 与 `check-license`；TOTP、Vault 客户端等须在 `deps/` 登记。
- 认证端点必须独立限流并具备防枚举响应（统一错误文案与时延）。

---

## 四、P0 任务包（ENT-01～ENT-08）

> P0 关闭标准：验收全绿 + staging 多副本验证 + 回滚方式明确 + 门禁生效。

### ENT-01 身份闭环：账号域与令牌生命周期

**状态**：`已完成（2026-10-23，阶段 1–5）`；领域模型、令牌与撤销语义、配置与迁移编号详见 [`identity-authn.md`](./identity-authn.md)。

**目标**：让平台能接入真实用户，令牌可刷新、可撤销、可失效，A 端具备 MFA，三端身份严格隔离。

**前置依赖**：`ENT-16` 前置切片（集成测试基座）、`ENT-02`（撤销状态需共享存储）。

**任务分解**

| 序号 | 任务 | 产出 | 验收要点 | 阶段 |
|---|---|---|---|---|
| T1 | 身份域建模 | `internal/identity/domain`：`Account`、`Credential`、`Session`、`MFAEnrollment`、`LoginAttempt`、状态机 | 状态迁移表驱动用例；禁用 / 锁定不可登录 | 1 |
| T2 | 持久化 | `identity_account`、`identity_credential`、`identity_session`、`identity_mfa`、`identity_login_attempt`、`identity_oauth_identity`；迁移 `000011_create_identity_tables`（`000009/000010` 已被 ENT-03 占用） | up/down 可执行；唯一键 `(channel, subject)`、`refresh_token_hash`、`(provider, provider_subject)` | 1 |
| T3 | 凭据与策略 | Argon2id（复用 `pkg/password`）、密码强度、失败锁定、登录限流 | 5 次失败锁定；统一错误文案；审计事件 | 1 |
| T4 | 令牌签发 | Access JWT + 一次性 Refresh Token；claims `sub/aud/iss/exp/iat/jti/ver/channel/merchant_id`；`pkg/jwt` 支持 `kid` 密钥环 | 三端 issuer / audience / 密钥隔离；`kid` 支持轮换，未知 `kid` 失败关闭 | 2 |
| T5 | 撤销 | Redis `authn:revoked:<jti>`（TTL = 剩余寿命）与 `authn:ver:<account_id>`；`security` 中间件注入 `RevocationChecker` | 禁用 / 登出 ≤ 60s 生效；Redis 不可用时按 `fail_closed` 返回 `50300` | 2 |
| T6 | 认证端点 | A / B / C 三端 `login`、`refresh`、`logout`；A 端 `mfa/setup|verify|disable`、`sessions` 列表与撤销 | OpenAPI 三端契约；刷新令牌一次性，重放触发全链撤销 | 2 / 4 |
| T7 | C 端登录 | 短信验证码（复用 `pkg/sms`）+ OAuth / OIDC 回调；验证码哈希存储、尝试次数限制 | 防枚举、防刷；验证码 5 分钟过期 | 4 |
| T8 | A 端 MFA 与 SSO | TOTP 绑定 / 校验（`pkg/totp`）；OIDC 接入预留（优先复用组织 IdP） | 未完成 MFA 不得获得管理令牌 | 3 |
| T9 | 审计接入（已完成） | `auth.login`、`auth.logout`、`auth.token.refresh`、`auth.token.reused`、`auth.token.revoked`、`auth.mfa.bound`、`auth.account.locked` | 事件目录、字段、Outbox 事务 | 5 |
| T10 | 与 IAM 边界（已完成） | 令牌只承载身份；角色 / 权限仍由 IAM `Authorizer` 解析；`Principal` 增补 `SessionID` / `TokenVersion` | 不在令牌中固化角色，避免撤销滞后 | 2 |

**配置规划**

```yaml
auth:
  mode: jwt
  expected_channel: admin
  jwt:
    alg: RS256
    issuer: https://identity.example.com
    audience: [admin]
    active_kid: "2026-10"
    leeway: 30s
    keys:
      - kid: "2026-10"
        public_key_file: /etc/admin-frame/jwt-2026-10.pub.pem
        private_key_file: /etc/admin-frame/jwt-2026-10.key.pem  # 仅签发进程挂载

identity:
  enabled: true
  access_ttl: 15m
  refresh_ttl: 168h
  session:
    idle_timeout: 72h
    max_per_account: 10
  revocation:
    driver: redis
    failure_mode: fail_closed     # fail_closed|fail_open
  password:
    min_length: 12
    max_length: 128
    require_complexity: true
  lockout:
    max_failures: 5
    window: 15m
    duration: 15m
  mfa:
    admin_required: true
    issuer: AdminFrame
    secret_key: ""                # 32 字节（raw/hex/base64），仅环境变量注入
  login_rate_limit:
    rps: 1
    burst: 5
  sms:
    enabled: false
    code_ttl: 5m
    max_attempts: 5
    resend_interval: 60s
  oauth:
    auto_provision: false
    providers: []                 # [{name, client_id, client_secret_env, auth_url, token_url, scopes}]
```

**验收与门禁**

- 映射 `AC-ENT-01-1`～`AC-ENT-01-5`，全部落到自动化用例。
- 集成用例：登录 → 刷新 → 登出 → 旧令牌 401；禁用账号 → 旧令牌 ≤ 60s 失效；刷新令牌重放 → 会话撤销。
- 三端契约：`api/{admin,biz,c}/openapi.yaml` 新增认证端点与错误码。
- 新增 `make check-authn`：生产配置禁止 demo、认证端点契约存在、撤销路径有测试引用。
- `make secrets`、`make check-config`、`make check-openapi` 必须通过。

**回滚与风险**

| 风险 | 缓解 | 回滚 |
|---|---|---|
| 令牌语义切换导致全端 401 | 先影子校验、按 channel 灰度 | 切回 `auth.mode=demo`（仅非 prod）或回退签发逻辑 |
| Redis 不可用导致登录不可用 | 读写分离超时、连接池隔离；`failure_mode` 可配置 | 临时切 `fail_open`（需安全审批） |
| 短信 / OIDC 供应商不稳定 | 超时 + 熔断 + 降级提示 | 关闭该登录方式开关 |
| 账号迁移遗漏 | 提供账号导入脚本与核对报告 | 回滚迁移并恢复备份 |

---

### ENT-02 多实例运行语义

**目标**：多副本部署下 ID 不冲突、幂等跨副本、限流为全局语义、后台任务与 API 隔离。

**状态**：`已完成`（2026-10-22，T1～T3、T5～T8；T4 的逐 operation 标注作为独立契约变更跟进）。

**前置依赖**：`ENT-16` 集成基座；`pkg/redis` 可用的生产实例。

**任务分解**

| 序号 | 任务 | 产出 | 验收要点 |
|---|---|---|---|
| T1 | NodeID 治理 | `id.node_id` + `ADMIN_FRAME_ID_NODE_ID` + StatefulSet ordinal 来源；`platform.Deps` 注入 `id.Generator` | 两副本不同 NodeID；prod 禁止默认 1 |
| T2 | ID 注入 | `cmd/internal/iamruntime/runtime.go` 删除 `NodeID: 1` 硬编码 | 全仓无固定节点号 |
| T3 | 幂等中间件 | `internal/platform` 统一挂载 `pkg/idempotency`，Redis Store | 同 key 跨副本同结果；指纹不一致返回 `40900` |
| T4 | 幂等契约 | 写接口声明 `Idempotency-Key`；OpenAPI 标注；未携带策略（必须 / 可选） | `check-idempotency` 扫描写路由 |
| T5 | 分布式限流 | `pkg/ratelimit` Redis 滑动窗口；`rate_limit.driver=redis` | 两副本共享配额；超限 `42900` |
| T6 | 分键策略 | `channel` / `tenant` / `user` / 路由四类 key；登录端点独立桶 | 单租户超限不影响其他租户 |
| T7 | 后台任务迁移 | IAM Outbox 发布器迁至 `cmd/worker`；API 仅写 | 滚动重启无事件丢失 |
| T8 | 多副本验收 | `tests/integration/multiinstance/` + compose 两副本 | AC-ENT-02-1～4 全部通过 |

**配置规划**

```yaml
id:
  node_id: 0
  node_id_source: env          # config|env|statefulset；registry 暂不支持

idempotency:
  enabled: true
  driver: redis                # memory|redis
  header: Idempotency-Key
  ttl: 24h
  required: false
  max_body_bytes: 1048576

rate_limit:
  enabled: true
  driver: redis                # memory|redis
  key: channel_tenant_user
  failure_mode: fail_open      # 登录端点固定 fail_closed
  rps: 50
  burst: 100
  idle_ttl: 10m
```

**验收与门禁**

- `AC-ENT-02-1`：两副本批量创建无重复 ID；`AC-ENT-02-2`：同 key 跨副本同结果。
- `AC-ENT-02-3`：共享配额、统一 `42900`；`AC-ENT-02-4`：滚动重启无事件丢失 / 无重复副作用。
- 新增 `make check-idempotency`：扫描三端写路由是否挂载幂等或声明唯一键。
- `make check-config` 增加生产 NodeID / Redis 强制校验。
- 多副本用例进入 CI 集成 job；本地可用 compose 复现。

**落地说明（2026-10-22）**

- T1/T2：`internal/platform/id.go` 从 `id.node_id` 或 StatefulSet ordinal 解析 NodeID，`platform.Deps.IDs` 统一注入；`iamruntime` 与 IAM 用例删除固定 `NodeID: 1`，缺生成器直接失败关闭。prod/staging 非 worker 未显式配置或使用 statefulset 时启动失败。
- T3：`pkg/idempotency.RedisStore` 以 Lua 原子完成 `Begin/Complete/Release`；`internal/platform/pipeline.go` 在 `RateLimit → Idempotency → Handler` 挂载，`40900` 冲突 / `40901` 处理中 / `50300` 存储不可用由 `httpx.FromError` 统一信封。
- T4：`Idempotency-Key` 已在 `api/openapi/components.yaml` 定义；平台级挂载覆盖三端写路由。逐 operation 标注延后，避免未经客户端协调的强制头升级。
- T5/T6：`pkg/ratelimit.RedisKeyed` 以 Redis `TIME` 实现跨副本令牌桶，`failure_mode` 默认 `fail_open`；分键支持 `channel / channel_user / channel_tenant_user / client`，超限继续 `42900` + `Retry-After`。
- T7：ENT-03 已改为平台统一异步基础设施（`async.*`）：`async.outbox.publish_in_api` 控制 API 侧是否内嵌 Relay，默认由 `cmd/worker` 的 `AsyncRuntime` 投递，退出时等待循环收敛；IAM 审计写入 `async_outbox`，指标通过 `AuditTopicObserver` 映射。
- T8：`tests/integration/multiinstance` 覆盖 NodeID 唯一性、跨副本幂等、共享限流与 outbox 重启不重投；`scripts/ci/check-idempotency.sh` 校验挂载、配置样例、固定 NodeID 与 OpenAPI 契约。
- 验证命令：`make fmt-check`、`go build ./...`、`go test ./...`、`make check-idempotency`、`make check-config`；Redis/MySQL 用例在未设置 `TEST_REDIS_ADDR` / `TEST_MYSQL_DSN` 时显式 skip。

**回滚与风险**

- 幂等 / 限流保留 `memory` 驱动开关，异常时单副本回退（容量下降但不阻断）。
- NodeID 变更需成对操作（先扩后缩），文档写明窗口内禁止同时重启全部副本。
- Outbox 迁移期间 API 与 worker 不得同时发布；用 `publish_in_api` 开关控制，发布完成后再关闭 API 侧。

---

### ENT-03 通用异步基础设施

**目标**：提供平台级 Outbox Relay、消费运行时与事件 envelope，让未来业务域声明式接入，不重复实现可靠性逻辑。

**前置依赖**：`ENT-02`（发布器独立进程与幂等存储）。

**实施状态**：`已完成（2026-10-22；T7 于 2026-10-24 补齐 NATS JetStream 适配器）`。T1～T8 已落地；`async.driver=nats` 为可选驱动，默认仍为 `db`。详细拆解见 [ENT-03 实施文档](../.trae/documents/ENT-03-async-infrastructure-implementation.md)。

**任务分解**

| 序号 | 任务 | 产出 | 验收要点 |
|---|---|---|---|
| T1 | 事件 envelope | `pkg/mq` 扩展 `event_id/event_type/version/tenant_id/occurred_at/trace_id/idempotency_key` | schema 校验；版本不兼容走新事件 |
| T2 | 通用 Outbox | `pkg/outbox`：Store 接口、`ClaimBatch` 租约、`MarkDone/MarkFailed/MoveToDLQ` | 事务提交后崩溃可续投 |
| T3 | Relay | 并发拉取、指数退避、最大尝试、死信、指标 | 积压 / 滞后 / 失败指标可见 |
| T4 | 消费运行时 | 注册 handler、至少一次、幂等键、并发度、优雅退出 | 重复投递无重复副作用 |
| T5 | DLQ | `async_dlq` 表 + 重放命令；毒消息告警 | `AC-ENT-03-3` |
| T6 | worker 装配 | `cmd/worker` 消费 API 写入的事件；生命周期与平台一致 | 跨进程消费验证 |
| T7 | broker 适配 | `internal/platform/async_nats.go` 实现 `pkg/mq.Broker`（JetStream、MsgId 去重、DLQ subject）；默认仍是 `db` 驱动 | 不引入 Kafka；容量触发再评估 |
| T8 | IAM 复用 | IAM Outbox 迁移到通用 Relay 或保持适配层 | 行为不回归；指标不丢失 |

**配置规划**

```yaml
async:
  enabled: false                # prod/staging + iam.mode=enforce 强制 true
  driver: db                    # 仅 db；nats/kafka 未实现，Validate 拒绝
  outbox:
    publish_in_api: false
    poll_interval: 1s
    batch_size: 100
    lease: 30s
    max_attempts: 10
  consumer:
    concurrency: 4
    visibility_timeout: 60s
    retry_backoff: exponential
```

**验收与门禁**

- `AC-ENT-03-1`：事务已提交、发布前杀进程，重启后事件仍投递。
- `AC-ENT-03-2`：重复投递无重复副作用；`AC-ENT-03-3`：毒消息进 DLQ 并告警。
- `AC-ENT-03-4`：worker 跨进程消费；`AC-ENT-03-5`：新增业务域接入无需修改平台核心。
- `make check-observability` 增加积压 / 滞后指标断言；集成 job 覆盖 Relay 与 DLQ。

**回滚与风险**

| 风险 | 缓解 | 回滚 |
|---|---|---|
| Relay 重复投递放大副作用 | 消费侧幂等键强制 | 暂停 Relay，切换为人工补偿 |
| 死信堆积无人处理 | DLQ 告警 + 重放 runbook | 关闭新消费注册，先清理积压 |
| 引入 broker 运维复杂度 | 默认 DB 驱动；broker 仅可选 | 切回 `async.driver=db` |

---

### ENT-04 审计持久化、查询与保留

**目标**：审计从「只写日志」升级为可查询、可导出、不可篡改、可保留 180 天的存储能力。

**前置依赖**：`ENT-03`（Outbox 异步落库）、`ENT-01`（登录审计事件）。

**任务分解**

| 序号 | 任务 | 产出 | 验收要点 |
|---|---|---|---|
| T1 | 表设计 | `audit_events`：事件字段 + `attrs JSON` + `prev_hash` / `hash`；迁移 `000010_create_audit_events` | 追加写；索引 `(occurred_at)`、`(actor_id, occurred_at)`、`(resource_type, resource_id)` |
| T2 | 写入链路 | `auditlog.Recorder` 经 Outbox 落库；关键操作同事务 | 写失败即业务失败 |
| T3 | 防篡改 | 应用账号无 `UPDATE/DELETE`；哈希链可选启用 | `AC-ENT-04-3` |
| T4 | 查询接口 | `GET /admin/v1/audit/events`：时间、主体、动作、资源、结果、分页 | 权限 `audit:read`；过滤正确 |
| T5 | 导出接口 | `POST /admin/v1/audit/events/export` 异步任务 + 下载；导出本身审计 | 导出范围与脱敏 |
| T6 | 保留与归档 | worker 日任务：到期归档 / 清理；legal hold 优先 | `AC-ENT-04-2` |
| T7 | 存储切换 | `audit.store=db`；`SlogEmitter` 保留为本地调试出口 | prod 禁止仅日志 |

**配置规划**

```yaml
audit:
  store: db                     # db|object
  retention_days: 180
  critical_fail_closed: true
  export:
    max_range_days: 31
    async: true
  legal_hold: false
```

**验收与门禁**

- `AC-ENT-04-1`～`AC-ENT-04-4` 全部通过；角色 / 绑定 / 破玻璃 / 登录 / 导出事件可查询。
- `make check-audit` 扩展为「事件目录 ↔ 代码 ↔ 查询契约」一致性检查。
- 保留期任务有 dry-run 模式，避免误删；清理动作写审计。
- 导出接口必须限流并记录导出人、范围、条数。

**回滚与风险**

- 审计存储故障时，`critical_fail_closed=true` 会拒绝关键写入；需提供「切回日志 + 告警」的应急开关（需安全审批）。
- 大表查询需分页 + 时间范围限制；无范围查询默认拒绝。
- 哈希链影响写入吞吐，默认关闭，按合规要求启用。

---

### ENT-05 部署制品与环境基线

**目标**：提供可重复部署的 Helm Chart、三套环境 values、完整探针 / 资源 / 安全上下文 / 迁移 Job。

**前置依赖**：`ENT-06` 规则文件路径需与 Chart 对齐；`ENT-07` Secret 注入方式需确定。

**任务分解**

| 序号 | 任务 | 产出 | 验收要点 |
|---|---|---|---|
| T1 | Chart 骨架 | `deploy/helm/admin-frame/{Chart.yaml,values.yaml,_helpers.tpl}` | `helm lint` 通过 |
| T2 | 工作负载 | 三端 API + worker Deployment；readiness `/readyz`、liveness `/healthz` | 探针参数可配置 |
| T3 | 网络 | Service + Ingress（`/admin` `/biz` `/c`）；NetworkPolicy 默认拒绝 | 仅允许 Ingress → API、API → DB/Redis |
| T4 | 弹性 | HPA（CPU + 自定义指标预留）、PDB、反亲和 / 拓扑分布 | 滚动更新不中断 |
| T5 | 安全 | 非 root、只读根文件系统、drop ALL、seccomp、ServiceAccount 最小权限 | `kube-linter` 无高危 |
| T6 | 配置与密钥 | ConfigMap + ExternalSecret / Vault 注入；镜像固定 digest | 渲染产物无明文密钥 |
| T7 | 迁移 Job | `migrate` Job 在 API 之前执行；失败阻断发布 | Job 使用独立 ServiceAccount |
| T8 | 环境基线 | `values-dev.yaml`、`values-staging.yaml`、`values-prod.yaml` | 三套 `helm template` 通过 |

**Chart 结构规划**

```text
deploy/helm/admin-frame/
├── Chart.yaml
├── values.yaml
├── values-dev.yaml
├── values-staging.yaml
├── values-prod.yaml
└── templates/
    ├── _helpers.tpl
    ├── configmap.yaml
    ├── serviceaccount.yaml
    ├── deployment-admin.yaml
    ├── deployment-biz.yaml
    ├── deployment-c.yaml
    ├── deployment-worker.yaml
    ├── service.yaml
    ├── ingress.yaml
    ├── hpa.yaml
    ├── pdb.yaml
    ├── networkpolicy.yaml
    ├── migrate-job.yaml
    └── servicemonitor.yaml
```

**验收与门禁**

- `AC-ENT-05-1`：三套 values `helm template` + `kubectl --dry-run=server`（staging）通过。
- `AC-ENT-05-2`：清单含探针、资源、HPA、PDB、NetworkPolicy、安全上下文。
- `AC-ENT-05-3`：无明文密钥；镜像 digest；`AC-ENT-05-4`：staging 部署 → 冒烟 → 回滚演练。
- 新增 `make check-deploy`：`helm template` + `kubeconform` / `kube-linter` + 明文密钥 / digest 断言。
- `check-deploy` 不纳入本地 `make ci`，由 CI `deploy` job 与发布流水线执行（见 §7 门禁分层）。

**回滚与风险**

- 默认 `helm upgrade --atomic --timeout`；失败自动回退上一 revision。
- 迁移 Job 失败时不触发 Deployment 更新；已执行迁移的回退需配套 down 脚本与备份。
- 严禁在 Chart 中写死环境差异；差异只允许出现在 values。

---

### ENT-06 告警规则、看板与 SLO 落地

**目标**：把 `ALERTS.md` 中的告警从文档变为可执行规则，并建立 SLO / 错误预算与看板。

**前置依赖**：`ENT-05`（ServiceMonitor / 部署位置）、`ENT-02/03`（限流与积压指标）。

**任务分解**

| 序号 | 任务 | 产出 | 验收要点 |
|---|---|---|---|
| T1 | 规则文件 | `deploy/observability/prometheus/rules/{api,iam,async,db,slo}.yaml` | `promtool check rules` 通过 |
| T2 | 告警映射 | `ALERTS.md` 每条告警有 `alert` 名、`for`、阈值、runbook 链接 | 三方一致 |
| T3 | SLO | 可用性 / P99 时延记录规则；多窗口燃烧率告警 | 错误预算可计算 |
| T4 | 看板 | `deploy/observability/grafana/dashboards/*.json`：RED、IAM、Async、DB | 三端与 worker 可见 |
| T5 | 路由与抑制 | `alertmanager.yaml`：P1 立即通知、P2 聚合、维护窗抑制 | 演练验证触达 |
| T6 | 故障注入验证 | staging 注入 5xx / DB 池耗尽 / Outbox 积压 | `AC-ENT-06-2` |
| T7 | Trace 后端 | OTLP Collector 接入说明；采样率与保留期 | 入口到 DB 可追踪 |

**验收与门禁**

- `AC-ENT-06-1`：每条 `ALERTS.md` 告警都有规则与 runbook。
- `AC-ENT-06-2`：三类故障可触发对应告警；`AC-ENT-06-3`：看板数据完整。
- `AC-ENT-06-4`：一次请求 trace 可从入口追到 DB / 外部依赖。
- 新增 `make check-alerts`：解析 `ALERTS.md` 表格、规则 `alert` 名、runbook 路径，三方不一致即失败。
- CI `observability` job 执行 `promtool` + 看板 JSON 校验；staging 执行故障注入。

**回滚与风险**

- 规则误报导致告警风暴：新规则先以 `severity=info` + `for` 较长观察，再升级 P1/P2。
- 看板与指标漂移：看板 JSON 入库，指标重命名必须同 MR 更新。
- SLO 目标需业务确认，未确认前只记录不告警。

---

### ENT-07 密钥管理与轮换

**目标**：密钥从静态注入升级为可托管、可轮换、可审计；JWT 轮换不停机。

**前置依赖**：`ENT-05`（Secret 注入方式）、`ENT-01`（`kid` 与多密钥）。

**任务分解**

| 序号 | 任务 | 产出 | 验收要点 |
|---|---|---|---|
| T1 | Provider 接口 | `internal/platform/secrets`：`Get` / `GetBytes` / `Close`；启动期拉取 | 失败拒绝启动 |
| T2 | 本地实现 | env / file provider（仅 local / dev / test） | prod 禁用 |
| T3 | 生产实现 | Vault / 云 KMS 适配；缓存与超时；密钥访问日志 | 依赖故障 fail fast |
| T4 | 配置接入 | DB DSN、Redis、对象存储、JWT、加密密钥统一走 Provider | 配置中不出现明文 |
| T5 | JWT 轮换 | `kid` 选取、重叠期、旧密钥退出；轮换 runbook | 轮换期间无中断 |
| T6 | DB / Redis 凭据 | 短期凭据或可轮换账号；轮换步骤文档化 | 无需改镜像 |
| T7 | 字段加密 | `pkg/crypto` KEK / DEK 用法；敏感 PII 加密存储 | 密钥经 KMS 包装 |

**验收与门禁**

- `AC-ENT-07-1`：轮换 JWT 密钥请求不中断，旧令牌重叠期可用。
- `AC-ENT-07-2`：DB 凭据轮换步骤可执行；`AC-ENT-07-3`：仓库 / 镜像 / 日志无密钥。
- `AC-ENT-07-4`：轮换与吊销 runbook 完成一次演练。
- `make secrets`、`make check-config`、发布 Checklist 必须通过。
- 新增 runbook：`docs/runbooks/key-rotation.md`、`docs/runbooks/key-compromise.md`。

**回滚与风险**

- Provider 不可用时禁止回落到环境变量（生产）；只允许显式应急开关并审计。
- 轮换顺序固定：新密钥发布 → 双密钥并存 → 新签发起用 → 旧令牌自然过期 → 旧密钥移除。
- 密钥删除前必须确认无在途令牌；删除动作需双人审批。

---

### ENT-08 备份、恢复与灾备演练

**目标**：建立可验证的备份、PITR 恢复与季度演练机制，明确 RPO / RTO。

**前置依赖**：`ENT-05`（CronJob / Job 部署）、`ENT-07`（备份加密密钥托管）。

**任务分解**

| 序号 | 任务 | 产出 | 验收要点 |
|---|---|---|---|
| T1 | 备份策略 | 全量 + binlog；保留期；加密；异地副本 | 备份失败告警 |
| T2 | 备份脚本 | `scripts/backup/{backup.sh,restore.sh,verify.sh}` | 幂等、可重入、日志可审计 |
| T3 | 部署编排 | `deploy/backup/cronjob.yaml`；独立 ServiceAccount 与最小权限 | 不与业务库账号共用 |
| T4 | 恢复流程 | 全量恢复、PITR、单租户数据修复 | `AC-ENT-08-1` |
| T5 | RPO / RTO | 明确目标（建议 RPO ≤ 5min、RTO ≤ 60min，待业务确认） | 写入 runbook |
| T6 | 对象存储保护 | 版本化 / 跨区复制按需启用 | 误删可恢复 |
| T7 | 演练机制 | 每季度一次；记录模板复用 `docs/runbooks/_TEMPLATE.md` | `AC-ENT-08-4` |

**验收与门禁**

- `AC-ENT-08-1`：staging 完成全量 + PITR 恢复，数据校验通过。
- `AC-ENT-08-2`：RPO / RTO 写入 runbook；备份失败有告警。
- `AC-ENT-08-3`：备份加密、访问受控、保留期生效。
- `AC-ENT-08-4`：演练记录归档，含耗时与问题清单。
- 新增 `make check-backup`：脚本、CronJob、runbook、演练模板存在且配置项完整。
- `check-runbooks` 增加备份 / 恢复 / 灾备条目。

**回滚与风险**

- 恢复演练使用独立环境，禁止在 staging 生产数据上直接覆盖。
- PITR 依赖 binlog 保留期，必须与备份保留期对齐。
- 单租户恢复需提供 SQL 模板与核对脚本，避免全库回滚。

---

## 五、P1 任务包（ENT-09～ENT-18）

> P1 关闭标准：功能上线 + 自动化用例 + 观测指标 + 文档回写。P1 不得在 Wave 1/2 出口前抢占资源。

| ID | 任务包 | 关键产出 | 依赖 | 验收摘要 |
|---|---|---|---|---|
| `ENT-09` | 租户模型与生命周期 | `internal/tenant`；`tenant` / `tenant_quota` / `tenant_member` 表；状态机 `pending → active → suspended → terminated`；与身份、IAM、缓存、令牌联动 | ENT-01、ENT-12 | 停用 ≤ 60s 拒绝访问；退出按策略处置；全链路审计 |
| `ENT-10` | 通知 / Webhook 投递编排 | `internal/notification`：模板、偏好、去重、限流、重试、DLQ、状态查询；复用 `pkg/sms|email|push|webhook` | ENT-03 | 至少一次且幂等；失败可重试 / 补偿；投递指标可见 |
| `ENT-11` | 特性开关与灰度 | `internal/featureflag`：规则、百分比灰度（按 subject 哈希）、审计、到期清理 | ENT-01 | 秒级开关；关闭即回退；无长期遗留 flag |
| `ENT-12` | 数据范围契约 | `Principal` 增加 scope；`internal/shared/authz` 提供 `EnforceMerchant` / `ScopeWhere`；仓储强制过滤 | ENT-01、ENT-09 | 跨租户读写被拒绝；越权自动化用例通过 |
| `ENT-13` | 依赖韧性与第三方集成 | `internal/platform/deps.go` 依赖注册表（超时 / 重试 / 熔断 / 舱壁）；回调验签 + 幂等 + 状态机模板 | ENT-03 | 下游故障快速失败；重复回调不重复处理；依赖指标可见 |
| `ENT-14` | 分布式限流与租户配额 | Redis 全局限流；`tenant_quota` 配额管理；按 channel / tenant / user / 接口分键 | ENT-02、ENT-09 | 多副本共享配额；统一 `42900`；配额变更可审计 |
| `ENT-15` | 数据生命周期与隐私合规 | PII 分类分级、保留策略、DSAR 导出 / 删除、法务保留、脱敏与最小化 | ENT-04、ENT-09 | 删除可验证；保留任务生效；导出经审批并审计 |
| `ENT-16` | 测试基础设施 | `pkg/testkit`（MySQL / Redis / MinIO）；`tests/integration`；`tests/e2e`；契约测试；负载基线 | — | CI 跑真实依赖集成；三端 E2E 主链路；压测基线登记 |
| `ENT-17` | 发布自动化与迁移门禁 | `.github/workflows/release.yml`：staging → prod 审批；迁移前置；金丝雀指标；自动 / 一键回滚 | ENT-02、ENT-05、ENT-16 | 发布可重复；迁移失败阻断；异常自动回滚 |
| `ENT-18` | 值班与事故管理 | on-call 排班、P1/P2 分级矩阵、升级路径、Postmortem 模板、Game Day | ENT-06 | 一次演练完成；P1 5 分钟内触达；复盘行动项跟踪 |

### 5.1 P1 关键设计约束

1. **租户是唯一隔离边界**：所有新的业务表必须带 `tenant_id`（或等价 `merchant_id`），并在仓储层强制注入，禁止依赖调用方自觉传参。
2. **身份不承载角色**：角色与权限始终由 IAM 解析；租户停用通过 `token_version` 递增 + 缓存失效实现快速生效。
3. **通知与业务解耦**：业务域只写 Outbox 事件，通知中心订阅；禁止在业务事务里同步调用外部渠道。
4. **开关默认关闭**：灰度百分比按 `hash(flag_key + subject_id)` 计算，保证同一主体稳定命中；flag 必须有 owner 与到期日。
5. **回调必须三件套**：验签、幂等键、状态机；三者缺一不得合入。
6. **配额与限流同一语义**：配额耗尽与限流超限统一 `42900`，错误文案区分「配额」与「频率」。
7. **DSAR 不可硬删**：默认匿名化 / 软删除 + 法务保留；物理删除需审批与审计。
8. **测试基座先行**：`ENT-16` 的 `testkit` 必须先于任何 P1 集成用例落地，避免每个域重复造轮子。

---

## 六、P2 任务包（ENT-19～ENT-25）

> P2 只在触发条件成立后启动；启动前必须完成对应前置评估，禁止「为了企业级而企业级」。

| ID | 能力 | 启动条件 | 前置评估 / 产出 |
|---|---|---|---|
| `ENT-19` | 供应链强化 | 对外发布或客户安全评审 | SBOM（Syft）、镜像签名（Cosign）、DAST 方案；`make check-sbom` |
| `ENT-20` | 边缘与传输安全 | 公网暴露或等保 / 渗透测试 | WAF / API Gateway 配置、mTLS 方案、安全头基线、C 端 Bot 防护 |
| `ENT-21` | 治理与文档运营 | 团队规模扩大 | CODEOWNERS、分支保护勾选、文档站点；`standards-roadmap.md` §四 |
| `ENT-22` | 容量与成本治理 | 进入放量阶段 | 负载模型、容量评估、成本标签、预算告警；压测基线升级 |
| `ENT-23` | 多区域 / 读副本 / 多活 | 可用性目标提升或多区域合规 | 只读副本、跨区备份、故障切换与演练 |
| `ENT-24` | 搜索 / 报表 / 数仓 | 运营分析或对账需求 | CDC / ETL、读模型、导出任务、BI 对接 |
| `ENT-25` | ABAC / 组织架构 | 权限复杂度明确 | 先立 ADR，再评估部门树、角色继承、条件策略 |

**P2 启动检查清单**

- [ ] 业务量或合规要求有书面依据，而非主观判断。
- [ ] 现有能力（DB Outbox、Redis 限流、单区域部署）已接近容量上限或有明确风险。
- [ ] 方案包含成本估算、运维复杂度评估与退出策略。
- [ ] 不引入与现有栈重复的组件（如已有 Redis 时不新增独立缓存集群）。

---

## 七、门禁建设计划

### 7.1 门禁分层

| 层级 | 执行位置 | 内容 | 失败策略 |
|---|---|---|---|
| **Tier A 本地 / `make ci`** | 开发机 + 每次 CI | 格式、lint、单测、race、配置、迁移、审计、发布、密钥等现有门禁 + 新增静态门禁 | 阻断合并 |
| **Tier B CI 专项 job** | GitHub / Gitee | `check-deploy`、`check-alerts`、`check-authn`、`check-idempotency`、`check-audit-store`、`check-backup`、OpenAPI 契约 | 阻断合并 |
| **Tier C staging / nightly** | staging 或定时任务 | 多副本集成、E2E、故障注入、备份恢复、密钥轮换演练 | 阻断发布 / 触发告警 |

> 设计原则：本地 `make ci` 不引入 helm / kubectl / 容器运行时等重依赖，避免开发机不可运行；重依赖检查进入 Tier B/C。

### 7.2 新增门禁清单

| 门禁 | 覆盖 | 检查内容 | 层级 | 实施任务 |
|---|---|---|---|---|
| `make check-authn` | ENT-01 | prod 禁止 demo；认证端点契约；撤销与锁定用例存在 | A/B | T6 / T9 |
| `make check-idempotency` | ENT-02 | 三端写路由挂载幂等或声明唯一键 | B | T4 |
| `make check-outbox` | ENT-03 | 事件 envelope 字段、Relay 指标、DLQ 表存在 | B | T2 / T5 |
| `make check-audit-store` | ENT-04 | 关键事件有存储与查询契约，非仅日志 | B | T2 / T4 |
| `make check-deploy` | ENT-05 | 三套 values 渲染、探针、资源、安全上下文、无明文密钥、digest | B | T8 |
| `make check-alerts` | ENT-06 | `ALERTS.md` ↔ 规则 ↔ runbook 三方一致 | B | T1 / T2 |
| `make check-secret-provider` | ENT-07 | 生产禁止 env-only；Provider 启动校验 | B | T4 |
| `make check-backup` | ENT-08 | 脚本、CronJob、runbook、演练模板、保留期配置 | B | T2 / T3 |
| `make check-e2e` | ENT-16 | 三端 E2E 存在且可运行 | C（nightly） | T3 |
| `make check-sbom` | ENT-19 | 镜像 SBOM / 签名产物 | C（P2） | — |

### 7.3 门禁实施顺序

1. **Wave 0**：新增脚本骨架 + `Makefile` target + CI job 占位；脚本在无产物时明确「SKIP」而非失败。
2. **每条 ENT 合入时**：脚本转为强校验，并在 CI 中验证「故意违规 → 失败」。
3. **E1 出口前**：Tier B 全部门禁进入分支保护必过项。
4. **E2 出口前**：Tier C 任务纳入 nightly 与发布流水线。

### 7.4 既有门禁扩展

| 既有门禁 | 扩展内容 |
|---|---|
| `check-layering.sh` | 新增 `internal/identity` / `internal/auditlog` / `internal/tenant` 不得依赖 `internal/platform` 的规则 |
| `check-config.sh` | 新增配置段必须出现在四份样例；生产强制项与 `Validate` 一致 |
| `check-migrations.sh` | 新增表必须有 up/down、索引、保留期说明；审计表禁止 UPDATE/DELETE 授权 |
| `check-test-gates.sh` | 新增域覆盖率门槛（`TEST_IDENTITY_COVER_MIN=80`、`TEST_AUDIT_COVER_MIN=80`） |
| `check-runbooks.sh` | 新增 `key-rotation`、`backup-restore`、`disaster-recovery`、`dlq-replay`、`oncall` 条目 |
| `check-observability.sh` | 新增积压 / 消费者滞后 / 配额指标断言 |
| `check-openapi.sh` | 新增认证、审计查询、租户、配额端点契约 |
| `check-release.sh` | 新增 digest、迁移 Job、回滚命令断言 |

---

## 八、里程碑与验收

### 8.1 波次出口

| 波次 | 出口条件 | 验收方式 |
|---|---|---|
| Wave 0 | compose 拉起依赖；`pkg/testkit` 可用；集成测试骨架跑通；新门禁可空跑 | 本地 + CI 集成 job |
| Wave 1 | ENT-01～04 关闭；多副本无 ID 冲突；登录 / 撤销 / 审计查询可用；Outbox 跨进程投递 | staging + 集成用例 |
| Wave 2 | ENT-05～08 关闭；三套 values 渲染；告警可触发；密钥轮换与备份恢复演练完成 | staging + 演练记录 |
| E1 | Wave 1 + Wave 2 全部出口；`make ci` + Tier B 全绿；回滚演练通过 | 架构 / 安全 / SRE 联合评审 |
| E2 | ENT-09～18 关闭；租户 / 通知 / 开关 / 数据范围 / 测试 / 发布 / 值班闭环 | 联合评审 + 客户验收场景 |
| E3 | P2 按触发条件落地；SLO 与容量模型有效 | 治理评审 |

### 8.2 staging 验收场景

所有 P0 关闭前必须在 staging 完成以下场景，记录命令、输出与耗时：

1. **身份**：登录 → 刷新 → 登出 → 旧令牌 401；禁用账号 → 60s 内拒绝；刷新令牌重放 → 会话撤销。
2. **多副本**：两副本批量写入无 ID 冲突；同 `Idempotency-Key` 跨副本同结果；共享限流配额。
3. **异步**：提交事务后杀进程 → 重启事件仍投递；毒消息进 DLQ 并告警。
4. **审计**：查询角色 / 绑定 / 登录 / 导出事件；保留期任务 dry-run；导出审计。
5. **部署**：`helm upgrade --atomic` → 冒烟 → 回滚；迁移 Job 失败阻断发布。
6. **可观测**：注入 5xx / DB 池耗尽 / 积压，告警触达；trace 从入口到 DB。
7. **密钥**：JWT 密钥轮换无中断；Provider 不可用时 fail fast。
8. **备份**：全量 + PITR 恢复；数据校验；演练记录归档。

### 8.3 验收证据要求

每个 `ENT` 关闭时在 PR / 执行记录中附：

- 验收命令原文与关键输出（脱敏）。
- 环境说明（本地 / CI / staging 副本数、依赖版本）。
- 回滚命令与执行结果。
- 新增门禁的「违规 → 失败」证明。
- 文档回写链接（standards / OpenAPI / runbook）。

---

## 九、文档回写矩阵

| 条目 | 需同步更新 |
|---|---|
| ENT-01 | `identity-authn.md`、`SECURITY_STANDARDS.md` §10.1、`API_STANDARDS.md`、`CONFIG_STANDARDS.md`、三端 OpenAPI、`docs/audit/EVENTS.md` |
| ENT-02 | `ARCHITECTURE.md` §11 / §18、`CONFIG_STANDARDS.md`、`RELIABILITY_STANDARDS.md`、部署清单 |
| ENT-03 | `RELIABILITY_STANDARDS.md`、`ARCHITECTURE.md` §15、`docs/audit/EVENTS.md`、`pkg-roadmap.md` |
| ENT-04 | `AUDIT_STANDARDS.md`、`docs/audit/FIELDS.md`、`docs/audit/EVENTS.md`、`API_STANDARDS.md` |
| ENT-05 | `RELEASE_STANDARDS.md`、`deploy/README.md`、`CI_STANDARDS.md`、`RUNBOOK_STANDARDS.md` |
| ENT-06 | `OBSERVABILITY_STANDARDS.md`、`RUNBOOK_STANDARDS.md`、`docs/runbooks/ALERTS.md` |
| ENT-07 | `SECURITY_STANDARDS.md` §10.3、`CONFIG_STANDARDS.md`、新增 runbook |
| ENT-08 | `DATA_STANDARDS.md` DATA-07、`docs/runbooks/` 新增恢复与灾备 |
| ENT-09～18 | 对应域文档、三端 OpenAPI、`TESTING_STANDARDS.md`、`PERFORMANCE_STANDARDS.md`、`ARCHITECTURE.md` §10 / §17 |
| 全部门禁 | `Makefile`、`.github/workflows/*.yml`、`.workflow/*.sh`、`scripts/ci/*`、PR 模板 |

> 规则：**文档回写与代码同 MR**；禁止「先合代码，后补文档」。上游 `production-readiness-gaps.md` 的状态表在条目关闭时同步更新。

---

## 十、风险登记与回滚

| 风险 | 影响 | 缓解 | 回滚 |
|---|---|---|---|
| P0 并行过多导致回归面失控 | 长期不可发布 | 每条 ENT 独立 MR；Wave 内按依赖顺序；默认开关关闭 | 单条 `git revert` + 关闭开关 |
| 身份切换导致全端 401 | 业务中断 | 影子校验、按 channel 灰度、双密钥并存 | 回退签发 / 校验版本 |
| Redis 成为单点 | 登录、限流、幂等不可用 | 连接池隔离、超时、降级策略；`failure_mode` 可配置 | 限流切 memory；撤销按审批切 `fail_open` |
| 审计 fail-closed 阻断写入 | 关键业务不可用 | 先观察模式；告警与应急开关 | 切回日志出口（需安全审批） |
| 迁移不可逆 | 数据丢失 | 先加后删、备份、down 脚本 | 恢复备份 + 反向迁移 |
| Helm / 规则与代码漂移 | 上线错误 | 清单入库 + `check-deploy` / `check-alerts` | 回退上一 revision |
| 门禁误报阻塞交付 | 开发效率下降 | 脚本先 SKIP 后强校验；白名单需注释与审批 | 临时豁免须 Owner 批准并限期修复 |
| 密钥 Provider 故障 | 服务无法启动 | 启动重试 + 明确失败日志；本地实现隔离 | 应急 env 注入（需审计与审批） |
| 备份恢复未验证 | 灾备失效 | 季度演练；恢复后数据校验 | 演练失败即阻断 E1 出口 |

**实施原则**

1. **失败默认安全**：认证、审计、密钥、迁移失败一律拒绝，不静默降级。
2. **先闭环后优化**：先做到「可运行 + 可观测 + 可回滚」，再谈性能与体验。
3. **可验证**：没有自动化用例或演练记录的能力，不算完成。
4. **不新增第二套语义**：同一能力只保留一个开关、一条主路径。
5. **文档同步**：关闭条目的同一 MR 回写标准、门禁与 OpenAPI。

---

## 十一、协作与评审机制

### 11.1 角色与职责

| 角色 | 职责 |
|---|---|
| 架构 Owner | 波次调整、跨域契约、ADR、例外审批 |
| 后端 Owner | 身份 / 异步 / 审计 / 租户等域的实现与验收 |
| SRE | 部署、可观测、备份恢复、演练与值班 |
| 安全 Reviewer | 认证、密钥、审计、权限与数据生命周期 |
| 产品 Owner | 租户套餐、SLO 目标、RPO / RTO 与合规需求确认 |

### 11.2 MR 拆分与评审

- 契约 MR：配置字段、OpenAPI、事件目录、数据库迁移、接口定义；**先合**。
- 实现 MR：领域逻辑、装配、测试；依赖契约已合入。
- 门禁 MR：脚本 + Makefile + CI job；可与实现同 MR，但必须证明「违规即失败」。
- 评审清单：§2.5 Task DoD + §3 工程约束 + §9 文档回写。

### 11.3 灰度与发布

1. 影子模式：新能力只记录 / 比对，不改变响应。
2. 单通道灰度：先 admin，再 biz，最后 c。
3. 单副本灰度：先 1 个副本开启，观察 24h（按指标而非固定时间）。
4. 全量：Tier B 门禁与 staging 验收通过后执行。

### 11.4 例外与豁免

- 任何 `nolint` 必须注明 linter 与原因；安全相关豁免需安全 Reviewer 批准。
- 覆盖率门槛、生产强制项不得永久豁免；临时豁免须在 PR 描述写明到期条件。
- 未按本规划执行的范围变更，须在「执行记录」登记并经架构 Owner 确认。

---

## 十二、状态跟踪与执行记录

> 状态：`待做` / `进行中` / `已完成` / `暂缓`。完成须附验收证据链接或命令输出。

| ID | 优先级 | 波次 | 状态 | 关闭判据（摘要） |
|---|---|---|---|---|
| ENT-01 | P0 | Wave 1 | 已完成（2026-10-23，阶段 1–5） | 登录 / 刷新 / 登出 / 撤销 / MFA 可用，禁用 ≤ 60s 生效；详见 [`identity-authn.md`](./identity-authn.md) |
| ENT-02 | P0 | Wave 1 | 已完成（2026-10-22） | 多副本 ID 唯一、幂等与限流共享；T4 逐 operation 标注延后 |
| ENT-03 | P0 | Wave 1 | 已完成（2026-10-22；T7 NATS JetStream 2026-10-24） | 通用 Outbox + 消费运行时 + DLQ + 积压指标；`async.driver=nats` 可选 |
| ENT-04 | P0 | Wave 1 | 待做 | 审计可查询 / 导出 / 保留 ≥ 180 天 |
| ENT-05 | P0 | Wave 2 | 待做 | Helm 三环境 + 探针 + 资源 + Secret 注入 + 迁移 Job |
| ENT-06 | P0 | Wave 2 | 待做 | 告警规则 / 看板 / SLO 代码化并演练 |
| ENT-07 | P0 | Wave 2 | 待做 | 密钥托管 + 不停机轮换演练 |
| ENT-08 | P0 | Wave 2 | 待做 | 备份 + PITR 恢复演练 + RPO/RTO |
| ENT-09 | P1 | Wave 3 | 待做 | 租户生命周期与 IAM / 令牌联动 |
| ENT-10 | P1 | Wave 3 | 待做 | 通知 / Webhook 投递编排与状态 |
| ENT-11 | P1 | Wave 3 | 待做 | 特性开关与灰度可运营 |
| ENT-12 | P1 | Wave 3 | 待做 | 数据范围在业务查询 / 写入强制 |
| ENT-13 | P1 | Wave 3 | 待做 | 依赖韧性接线 + 回调幂等模板 |
| ENT-14 | P1 | Wave 3 | 待做 | 分布式限流与租户配额 |
| ENT-15 | P1 | Wave 3 | 待做 | 数据分类 / 保留 / 导出 / 删除 |
| ENT-16 | P1 | Wave 0 / 3 | 待做（Wave 0 启动前置切片） | 集成矩阵 + E2E + 契约 + 压测 |
| ENT-17 | P1 | Wave 4 | 待做 | CD + 迁移门禁 + 金丝雀 / 回滚 |
| ENT-18 | P1 | Wave 4 | 待做 | on-call + 分级 + 复盘 + 演练 |
| ENT-19 | P2 | Wave 4 | 待做 | SBOM / 签名 / DAST |
| ENT-20 | P2 | Wave 4 | 待做 | WAF / 网关 / mTLS / 安全头 |
| ENT-21 | P2 | Wave 4 | 待做 | CODEOWNERS + 分支保护 + 文档站点 |
| ENT-22 | P2 | Wave 4 | 待做 | 容量模型 + 成本治理 |
| ENT-23 | P2 | Wave 4 | 待做 | 读副本 / 多区域 / 切换演练 |
| ENT-24 | P2 | Wave 4 | 待做 | CDC / 数仓 / 报表 |
| ENT-25 | P2 | — | 暂缓 | 先立 ADR，再评估 ABAC / 组织树 |

### 执行记录

| 日期 | 事项 | 状态 | 备注 |
|---|---|---|---|
| 2026-10-22 | 规划文档创建（v0.1） | 完成 | 基于差距评估 v0.2；拆分为 Wave 0～4 与 25 个任务包 |
| 2026-10-22 | ENT-02 多实例运行语义落地 | 完成 | NodeID 配置化 + Deps.IDs、Redis 幂等存储、Redis 令牌桶、`publish_in_api` / worker 发布器、multiinstance 集成用例与 `check-idempotency` 门禁；T4 逐 operation 标注单独跟进 |
| 2026-10-22 | ENT-03 通用异步基础设施落地 | 完成 | `pkg/outbox`（Store/Relay/Router/Deduper/DLQ）、`AsyncRuntime`、`async.*` 配置、IAM 完整迁移（迁移 000010）、`check-outbox` 门禁与 `async-dlq` runbook；集成用例覆盖 AC-ENT-03-1～5 |
| — | 后续条目关闭时在此追加 | — | 须附验收命令与输出、回滚结果、文档回写链接 |

---

## 附录

### 附录 A：任务包模板

新增或调整任务包时按以下结构填写，保证可执行与可验收：

```markdown
### ENT-xx 标题

**目标**：一句话说明关闭后获得的能力。

**前置依赖**：列出已关闭的 ENT 与基础设施条件。

**任务分解**
| 序号 | 任务 | 产出 | 验收要点 |

**配置 / 数据 / 接口变更**
- 配置段与默认值
- 迁移编号与表结构
- API 路径与权限点

**验收与门禁**
- AC 映射
- 命令与输出要求
- 新增门禁

**回滚与风险**
- 开关 / 回退路径
- 数据回滚
- 风险与缓解
```

### 附录 B：MR 描述模板

```markdown
## 变更动机
关联 ENT-xx；当前缺口与业务影响。

## 范围
- 变更文件 / 新增包
- 新配置项与默认值
- 新增迁移编号
- 新增 API / 权限点 / 审计事件

## 验收
- 命令：
- 输出摘要：
- 环境：local / CI / staging（副本数）
- 新增门禁的违规验证：

## 风险与回滚
- 风险：
- 开关：
- 回滚命令与数据回滚：

## 文档回写
- [ ] 标准文档
- [ ] OpenAPI
- [ ] 配置样例
- [ ] runbook / ALERTS
- [ ] 上游差距评估状态表
```

### 附录 C：新增 / 变更文件总览（规划态）

| 路径 | 类型 | 关联 |
|---|---|---|
| `internal/identity/**` | 新增 | ENT-01 |
| `internal/auditlog/**` | 新增 | ENT-04 |
| `internal/tenant/**` | 新增 | ENT-09 |
| `internal/notification/**` | 新增 | ENT-10 |
| `internal/featureflag/**` | 新增 | ENT-11 |
| `internal/platform/secrets/**` | 新增 | ENT-07 |
| `internal/platform/async.go` | 新增 | ENT-03 |
| `internal/platform/pipeline.go` | 变更 | ENT-02（幂等 / 分布式限流） |
| `internal/platform/config.go` | 变更 | 全部 |
| `internal/security/**` | 变更 | ENT-01（撤销检查、kid） |
| `internal/shared/auth/principal.go` | 变更 | ENT-12（scope） |
| `pkg/outbox/**` | 新增 | ENT-03 |
| `internal/platform/async_nats.go` | 新增 | ENT-03（T7 NATS JetStream 适配器） |
| `pkg/ratelimit/**` | 变更 | ENT-02 / ENT-14 |
| `pkg/testkit/**` | 新增 | ENT-16 |
| `cmd/worker/main.go` | 变更 | ENT-03 / ENT-08 / ENT-15 |
| `cmd/internal/iamruntime/runtime.go` | 变更 | ENT-02（NodeID / 发布器） |
| `migrations/000009_*` ～ `000015_*` | 新增 | 身份 / 审计 / 异步 / 租户 / 通知 / 开关 |
| `deploy/helm/admin-frame/**` | 新增 | ENT-05 |
| `deploy/observability/**` | 新增 | ENT-06 |
| `deploy/backup/**`、`scripts/backup/**` | 新增 | ENT-08 |
| `tests/integration/**`、`tests/e2e/**` | 新增 | ENT-16 |
| `scripts/ci/check-{authn,idempotency,outbox,audit-store,deploy,alerts,secret-provider,backup}.sh` | 新增 | §7 |
| `Makefile`、`.github/workflows/*.yml`、`.workflow/*.sh` | 变更 | §7 |

> 迁移编号为规划占位，实施时按实际合入顺序占用；若与其他分支冲突，须 rebase 后重新编号，禁止复用已合入编号。

### 附录 D：核对命令

```bash
# 基线与工作区
git rev-parse --short HEAD
git status --short

# 身份闭环是否落地
rg -n "func .*(Login|Logout|Refresh|Revoke|MFA)" internal/identity internal/security

# 多实例语义
rg -n "NodeID:\s*1" internal cmd
rg -n "idempotency|rate_limit.*driver" internal/platform

# 异步运行时
rg -n "ClaimBatch|MoveToDLQ|async_dlq" pkg internal cmd

# 审计存储与查询
rg -n "audit_events|audit:read|Retention" internal migrations

# 部署与告警制品
ls deploy/helm/admin-frame deploy/observability
helm template deploy/helm/admin-frame -f deploy/helm/admin-frame/values-staging.yaml >/dev/null
promtool check rules deploy/observability/prometheus/rules/*.yaml

# 门禁
make ci
make check-authn check-idempotency check-deploy check-alerts check-backup
```

> Windows PowerShell 下 `make` 等价命令为 `go test ./...`、`go vet ./...`、`gofmt -l .`；`scripts/ci/*.sh` 需在 Git Bash / WSL 中执行，或使用 `make gitee-job JOB=arch`。
