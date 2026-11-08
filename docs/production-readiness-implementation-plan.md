# 生产级能力缺口实施规划（Production Readiness Implementation Plan）

> **状态**：规划生效（v0.1，2026-10-22）；上游基线 `production-readiness-gaps.md` v0.2  
> **定位**：把 `ENT-01`～`ENT-25` 的差距评估转化为**可执行、可验收、可回滚**的任务包；只规划平台级能力，不新增业务域功能  
> **效力**：本文件的任务范围、验收标准与门禁对合入具有约束力；与 [`platform-remediation-plan.md`](./archive/platform/platform-remediation-plan.md) 同级  
> **适用范围**：`internal/`、`pkg/`、`cmd/`、`migrations/`、`deploy/`、`tests/`、`scripts/ci/`、`.github/`、`docs/`  
> **关联**：[`production-readiness-gaps.md`](./production-readiness-gaps.md)、[`ARCHITECTURE.md`](./ARCHITECTURE.md)、[`standards-roadmap.md`](./standards-roadmap.md)、[`platform-remediation-plan.md`](./archive/platform/platform-remediation-plan.md)、[`rbac-iam.md`](./rbac-iam.md)、[`CODE_STYLE.md`](./CODE_STYLE.md)、[`TESTING_STANDARDS.md`](./TESTING_STANDARDS.md)、[`CI_STANDARDS.md`](./CI_STANDARDS.md)、[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)、[`CONFIG_STANDARDS.md`](./CONFIG_STANDARDS.md)、[`DATA_STANDARDS.md`](./DATA_STANDARDS.md)、[`RELIABILITY_STANDARDS.md`](./RELIABILITY_STANDARDS.md)、[`OBSERVABILITY_STANDARDS.md`](./OBSERVABILITY_STANDARDS.md)、[`AUDIT_STANDARDS.md`](./AUDIT_STANDARDS.md)、[`API_STANDARDS.md`](./API_STANDARDS.md)、[`RELEASE_STANDARDS.md`](./RELEASE_STANDARDS.md)、[`RUNBOOK_STANDARDS.md`](./RUNBOOK_STANDARDS.md)、[`GIT_STANDARDS.md`](./GIT_STANDARDS.md)  
> **执行方式**：每条 `ENT` 独立 MR；验收标准全绿 + `make ci` + 本条新增门禁通过 + 文档回写完成，方可标记关闭  
> **最后更新**：2026-11-07（ENT-09～19 状态回写、归档记录链接修正；规划结构与验收判据仍为 v0.1）  
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

| 项        | 内容                                                                                |
| -------- | --------------------------------------------------------------------------------- |
| 差距评估     | [`production-readiness-gaps.md`](./production-readiness-gaps.md) v0.2（2026-10-22） |
| 代码基线     | `dad8fc0`（工作区干净；演示业务域已清理）                                                         |
| 已完成的平台整改 | `platform-remediation-plan.md` PLAT-01～PLAT-15 全部完成                               |
| 已完成的 IAM | `rbac-iam.md` M0～M4.7；M5 生产加固进行中                                                  |
| 现有门禁     | `make ci` 23 个 target；`scripts/ci/` 20 个检查脚本                                      |
| 现状结论     | 工程治理接近企业级，运行时能力仍是「单进程 + 内存 + 手工运维」骨架                                              |

### 1.2 规划目标

1. **可执行**：每个 `ENT` 拆成有明确产出、变更路径、验收命令的任务包，避免「大而空」的改造项。
2. **可验收**：每条验收标准必须能落到自动化用例、门禁脚本或 staging 演练记录，不接受「代码写完即完成」。
3. **可回滚**：每条能力上线前明确开关、回退路径与数据回滚方式，失败默认安全。
4. **不重复建设**：优先复用 `pkg/*` 原语、IAM 已验证的 Outbox / UnitOfWork / 缓存模式，以及现有 `internal/platform` 装配入口。
5. **不过度设计**：严格执行差距评估 §7.3「明确不做」，P2 仅在触发条件成立后启动。

### 1.3 成功判据

| 阶段            | 判据                                                                               | 目标等级 |
| ------------- | -------------------------------------------------------------------------------- | ---- |
| **E1 可生产灰度**  | `ENT-01`～`ENT-08` 全部关闭；staging 多副本可运行；真实身份、异步底座、审计、备份、告警、部署闭环；`make ci` + 新增门禁全绿 | L2   |
| **E2 可企业运营**  | `ENT-09`～`ENT-18` 全部关闭；租户、通知、开关、数据范围、测试、发布、值班闭环                                  | L2+  |
| **E3 规模化与合规** | `ENT-19`～`ENT-25` 按触发条件落地；有 SLO、演练记录与容量模型                                        | L3   |

### 1.4 不在规划范围

| 项                  | 原因                                              |
| ------------------ | ----------------------------------------------- |
| 任何业务域功能（订单、文件、营销等） | 本规划只补平台底座；业务域另行立项                               |
| 微服务拆分、服务网格、多活      | 当前规模不成立，触发条件见 `ARCHITECTURE.md` §5.6 与差距评估 §7.3 |
| 自建完整 IdP           | 身份域只做账号映射与会话策略；优先复用组织 OIDC                      |
| ABAC 表达式引擎、组织架构树   | 差距评估 `ENT-25` 明确暂缓，先立 ADR                       |
| 低流量阶段引入 Kafka      | 先用 DB Outbox 轮询满足可靠性，容量触发后再换 broker             |

### 1.5 不可违背的既存约束

以下约束来自现有架构与标准，规划与实施均不得突破：

| 约束                                                      | 来源                                     | 落地检查                           |
| ------------------------------------------------------- | -------------------------------------- | ------------------------------ |
| API 进程**禁止**自动执行数据库迁移                                   | `DATA_STANDARDS`、`check-migrations.sh` | 迁移只允许 `cmd/migrate` 与部署 Job 执行 |
| 生产 `iam.mode` 必须为 `enforce`                             | 项目硬约束、配置 `Validate`                    | `check-config` + 启动校验          |
| 高风险 IAM 直写在 `iam.approval.enabled=true` 时必须转审批单         | IAM M5 实现                              | 单测 + 配置校验                      |
| 生产 IAM 必须使用持久化数据库驱动 + Redis 缓存 + Outbox                 | 配置 `Validate`                          | 启动失败关闭                         |
| 禁止明文密钥进入仓库、日志、镜像                                        | `SECURITY_STANDARDS` SEC-01/05         | gitleaks + 配置脱敏 + 镜像扫描         |
| 三端身份隔离（issuer / audience / 密钥）                          | `SECURITY_STANDARDS` SEC-04            | 跨端令牌必须 401                     |
| `transport` 不得依赖 `internal/platform`；`platform` 不得依赖业务域 | `ARCHITECTURE.md` §6.1                 | `check-layering` + depguard    |
| 演示域代码不得回流                                               | 项目硬约束                                  | 新增包不得出现 `order` / `file` 演示语义  |

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

| 波次               | 范围                                                                                      | 出口标准                                                        | 备注                   |
| ---------------- | --------------------------------------------------------------------------------------- | ----------------------------------------------------------- | -------------------- |
| **Wave 0 基座准备**  | `ENT-16` 前置切片（`pkg/testkit` + `tests/integration` 骨架 + docker compose 依赖）、门禁脚本骨架、配置示例扩充 | 本地一条命令拉起 MySQL / Redis / MinIO；集成测试可运行；新增门禁可空跑              | 不改变运行时语义，先让后续任务「可验证」 |
| **Wave 1 运行时闭环** | `ENT-02`、`ENT-03`、`ENT-01`、`ENT-04`                                                     | 多副本 ID / 幂等 / 限流共享；通用 Outbox 与消费运行时；登录 / 刷新 / 登出 / 撤销；审计可查询 | 主线 A，E1 核心           |
| **Wave 2 交付闭环**  | `ENT-05`、`ENT-06`、`ENT-07`、`ENT-08`                                                     | Helm 三环境可渲染；告警 / 看板 / SLO 代码化；密钥托管与轮换；备份与恢复演练               | 主线 B，可与 Wave 1 并行    |
| **Wave 3 企业运营**  | `ENT-09`～`ENT-15`                                                                       | 租户、数据范围、通知、开关、韧性、配额、数据生命周期                                  | 主线 C，E2              |
| **Wave 4 规模化治理** | `ENT-17`、`ENT-18`、`ENT-19`～`ENT-25`                                                     | CD、值班、供应链、边缘、容量、多区域按需                                       | 仅触发条件成立后启动           |

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

| 能力             | 新增路径                                                                                 | 分层职责                                 | 禁止事项                                             |
| -------------- | ------------------------------------------------------------------------------------ | ------------------------------------ | ------------------------------------------------ |
| 身份域 `ENT-01`   | `internal/identity/{domain,app,infrastructure,transport}`                            | 账号、凭据、会话、MFA、登录审计                    | 不得 import `internal/platform`；不得承载 IAM 权限规则      |
| 审计存储 `ENT-04`  | `internal/auditlog/{app,infrastructure,transport}`                                   | 审计落库、查询、导出、保留                        | 事件模型继续复用 `internal/shared/audit`                 |
| 秘钥托管 `ENT-07`  | `internal/platform/secrets/`                                                         | `Provider` 接口与 env/file/vault/kms 实现 | 接口定义在消费侧；实现不得被业务域直接引用                            |
| 租户域 `ENT-09`   | `internal/tenant/{domain,app,infrastructure,transport}`                              | 租户生命周期、配额、成员                         | 不得复制 IAM 角色模型                                    |
| 通知中心 `ENT-10`  | `internal/notification/{domain,app,infrastructure}`                                  | 模板、偏好、投递编排、状态                        | 渠道实现继续用 `pkg/sms` / `email` / `push` / `webhook` |
| 特性开关 `ENT-11`  | `internal/featureflag/{domain,app,infrastructure,transport}`                         | 规则、灰度、审计、到期清理                        | 不得读取业务域内部状态                                      |
| 异步运行时 `ENT-03` | `pkg/outbox`、`pkg/mq`、`internal/platform/async.go`、`internal/platform/async_nats.go` | Relay、消费注册、重试、DLQ、指标、JetStream 适配    | `pkg/` 不得 import `internal/`                     |
| 测试基座 `ENT-16`  | `pkg/testkit/`、`tests/integration/`、`tests/e2e/`                                     | 容器化依赖、测试数据、E2E 驱动                    | 不得依赖固定端口、固定路径、真实云凭据                              |

> 目录归属在实施时若需调整，须在本文件与 `ARCHITECTURE.md` §5 同 MR 更新；**禁止**继续把新能力塞进 `internal/platform`（PLAT-15 的拆分触发条件仍有效）。

### 3.2 代码规范（S-01～S-12）

| 约束               | 在本规划中的落地方式                                                                     |
| ---------------- | ------------------------------------------------------------------------------ |
| S-01 格式          | 每个 MR 执行 `make fmt-check`；禁止手调对齐                                               |
| S-02 命名          | 包名单数小写；`identity` 而非 `accounts`；实现 `mysqlAccountRepository`；事件 `AccountLocked` |
| S-03 签名          | 所有用例 / 仓储入口 `ctx context.Context` 第一参数，`error` 最后返回                            |
| S-04 context key | 身份、租户、数据范围使用未导出 key 类型，禁止字符串 key                                               |
| S-05 错误早返回       | 认证 / 审计失败路径一律早返回，禁止嵌套成功分支                                                      |
| S-06 导入          | 禁止点导入；`pkg/storage/all` 类副作用导入必须注释                                             |
| S-07 接口位置        | `RevocationChecker`、`SecretProvider`、`OutboxStore` 接口定义在消费侧                    |
| S-08 panic       | 运行路径禁止 panic；仅允许 `Must*` 且须注释适用场景                                              |
| S-09 导出          | 禁止导出未导出类型；DTO 与领域对象分离                                                          |
| S-10 忽略 error    | `_ =` 必须注释原因；关键路径（审计、令牌撤销）禁止忽略                                                 |
| S-11 复杂度         | 单函数 ≤ 80 行、圈复杂度建议 ≤ 15；认证 / 消费运行时逻辑拆分                                          |
| S-12 并发          | Relay / 消费者 goroutine 必须 `ctx` 可取消、退出可汇合；禁止请求路径裸飞 goroutine                    |

**测试文件**：表驱动优先；`t.Helper()`；断言失败信息包含输入与期望；集成测试必须可用 build tag 或目录隔离。

### 3.3 配置规范（CFG）

每条新增能力必须完成以下五件事，缺一不可：

1. 在 `internal/platform/config/config.go` 增加结构体字段与默认值。
2. 在 `Validate` 中校验枚举、范围、必填与生产强制项，使用 `errors.Join` 聚合。
3. 支持 `ADMIN_FRAME_<SECTION>_<KEY>` 环境变量覆盖。
4. 更新 `configs/*.yaml.example`，**不得**写入明文密钥。
5. 敏感字段实现 `LogValue` 脱敏；新增配置项写入 `CONFIG_STANDARDS.md` 变量表。

**生产强制项（由 `Validate` 拒绝启动）**：

| 能力  | 强制项                                                                                                                                                               |
| --- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 身份  | `auth.mode=jwt`、`identity.revocation.failure_mode=fail_closed`、A 端 `identity.mfa.admin_required=true`                                                             |
| 多实例 | 禁止默认 `NodeID=1`；`idempotency.driver=redis`；`rate_limit.driver=redis`                                                                                              |
| 异步  | `async.driver=db` 时 `async.outbox.publish_in_api=false`；`async.driver=nats` 时 `async.broker.url` 必须 `tls://`/`wss://`；`iam.mode=enforce` 时强制 `async.enabled=true` |
| 审计  | `audit.store=db`；`audit.retention_days ≥ 180`；`audit.critical_fail_closed=true`                                                                                   |
| 密钥  | `secrets.provider ∈ {vault, kms}`；禁止仅 env 注入生产密钥                                                                                                                  |
| 备份  | `backup.enabled=true`；`backup.retention_days > 0`                                                                                                                 |

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

| 层级   | 范围                          | 运行方式                                | 门槛                                                               |
| ---- | --------------------------- | ----------------------------------- | ---------------------------------------------------------------- |
|      |                             |                                     |                                                                  |
| 单元测试 | `domain` / `app` / 中间件 / 配置 | `go test -race ./...`               | 新域 `domain` 覆盖率 ≥ 70%，`identity` / `auditlog` / `platform` ≥ 80% |
| 集成测试 | 真实 MySQL / Redis / MinIO    | `tests/integration/`，CI 独立 job      | 关键路径必须有集成用例（登录、刷新、撤销、Outbox、审计查询）                                |
| 契约测试 | 三端 OpenAPI                  | `make check-openapi` + 新增 schema 断言 | 破坏性变更必须在 `oasdiff` 中显式声明                                         |
| E2E  | 登录 → 核心写路径 → 查询             | `tests/e2e/`，nightly                | 三端各 ≥ 1 条主链路                                                     |
| 演练   | 多副本、故障注入、恢复                 | staging 手工 + 记录归档                   | 每季度至少 1 次                                                        |

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
| T2 | 持久化 | `identity_account`、`identity_credential`、`identity_session`、`identity_mfa`、`identity_login_attempt`、`identity_oauth_identity`；迁移 `000001_create_identity_tables` | up/down 可执行；唯一键 `(channel, subject)`、`refresh_token_hash`、`(provider, provider_subject)` | 1 |
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
- T3：`pkg/idempotency.RedisStore` 以 Lua 原子完成 `Begin/Complete/Release`；`internal/platform/httpserver/pipeline.go` 在 `RateLimit → Idempotency → Handler` 挂载，`40900` 冲突 / `40901` 处理中 / `50300` 存储不可用由 `httpx.FromError` 统一信封。
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
| T1 | 表设计 | `audit_events`：事件字段 + `attrs JSON` + `prev_hash` / `hash`；迁移 `000006_create_audit_events` | 追加写；索引 `(occurred_at)`、`(actor_id, occurred_at)`、`(resource_type, resource_id)` |
| T2 | 写入链路 | `auditlog.Recorder` 经 Outbox 落库；关键操作同事务 | 写失败即业务失败 |
| T3 | 防篡改 | 应用账号无 `UPDATE/DELETE`；哈希链可选启用 | `AC-ENT-04-3` |
| T4 | 查询接口 | `GET /admin/v1/audit/events`：时间、主体、动作、资源、结果、分页 | 权限 `audit:read`；过滤正确 |
| T5 | 导出接口 | `POST /admin/v1/audit/events/export` 异步任务 + 下载；导出本身审计（同步 `GET …/export` 保留） | 导出范围与脱敏 |
| T6 | 保留与归档 | worker 日任务：先写归档对象再按 ID 删除；legal hold / dry-run 优先 | `AC-ENT-04-2` |
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
  实测记录见 [`audit-export-verify.md`](./archive/verify/audit-export-verify.md)：HTTP 202→轮询→下载 7 行与库内 `COUNT(*)` 对账一致，
  12 行到期数据分 3 批归档后删除、归档对象行数 = 删除行数。
- `make check-audit` 扩展为「事件目录 ↔ 代码 ↔ 查询契约」一致性检查：
  `audit_export_check.py`（路由 ↔ OpenAPI ↔ 状态枚举三向比对）与
  `audit_immutability_check.py`（保留期仓储之外禁止 DELETE / 涉及 `audit_event` 的文件禁止 UPDATE）。
- 集成用例：`tests/integration/audit` 四个用例（导出端到端、卡死任务回收、归档后删除、查询过滤）在
  真实 MySQL + MinIO 上通过；全量集成套件 6 个包全绿。
- 保留期任务有 dry-run 模式，避免误删；清理动作写审计。
- 导出接口必须限流并记录导出人、范围、条数。

**回滚与风险**

- 审计存储故障时，`critical_fail_closed=true` 会拒绝关键写入；需提供「切回日志 + 告警」的应急开关（需安全审批）。
- 大表查询需分页 + 时间范围限制；无范围查询默认拒绝。
- 哈希链影响写入吞吐，默认关闭，按合规要求启用。

---

### ENT-05 部署制品与环境基线

**目标**：提供可重复部署的 Helm Chart、三套环境 values、完整探针 / 资源 / 安全上下文 / 迁移 Job。

**实施状态**：`制品已入库（2026-10-24）`。T1～T7 完成：Chart、三套 values、探针/资源/安全上下文、HPA/PDB、
NetworkPolicy、ServiceMonitor、migrate Job（含 `migrate` 镜像与 migrations 内置）；T8 的 staging
`kubectl --dry-run=server` 与滚动/回滚演练待在目标环境执行。门禁：`make check-deploy`
（CI job `deploy` 安装 Helm 后执行）。

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

**实施状态**：`规则/看板已入库（2026-10-24）`。T1～T4、T7 完成：`api/iam/async/db/slo/release`
规则组、四张 Grafana 看板、OTel Collector 与采样策略；T5 提供 Alertmanager 基线（接收端待接入）。
门禁：`make check-alerts`（CI `deploy` job 安装 promtool）。

T6 已完成（2026-11-02，kind 真集群 + 单体 Prometheus v3.1.0，规则与仓库同源）：
三类注入全部实触发 —— `API5xxRateHigh`（5xx 比率 0.99）、`DBPoolExhaustion`（in_use/max=6/6）、
`AsyncOutboxBacklog`（pending=1200），且故障撤销后告警均自动退回 inactive。
演练记录与复现脚本：[alert-drill-ent06.md](./archive/verify/alert-drill-ent06.md)、
`scripts/observability/alert-drill.sh`（`5xx` / `db-pool` / `outbox` 三个子命令）。
本轮同时修掉两个 P1：DB 池打满时 `/metrics` 采集阻塞导致**告警自噬**（`db_pool_*` 指标消失、
`DBPoolExhaustion` 永远不响），以及 `db.max_open_conns` 配置项缺失（池大小无法按环境调整）。

看板侧已完成（2026-11-03，kind + 单体 Grafana 11.6.0）：37 个面板查询错误 0、空数据 0，
Grafana 数据源健康、5 张看板加载成功。新增 `scripts/observability/check-dashboards.py`
（逐面板查真实 Prometheus）与静态门禁 `scripts/ci/dashboard_metrics_check.py`
（看板指标名必须在代码/规则里有定义，已并入 `check-observability.sh`）。
本轮修掉三个看板相关缺陷：IAM 快照缓存观测器未接线（缓存面板恒空）、shadow 模式不记录
鉴权决策（决策面板恒空）、RED 看板 5xx 比率在无错误时是空面板。
记录见 [observability-dashboard-verify.md](./archive/verify/observability-dashboard-verify.md)。

`AC-ENT-06-4`（trace 追到 DB）已完成（2026-11-03，kind + Jaeger 1.57 + Collector Contrib 0.104.0）：
一次 `GET /admin/v1/audit/events` 的 trace 为 `GET /admin/v1/audit/events` → `sql.conn.query`
（`db.system=mysql`，带 SQL 语句），父子链完整。交付 `pkg/db.OpenTraced`（otelsql 包装驱动，
仅在传入 TracerProvider 时启用）、`tracing.RoutePatternMiddleware`（把 ServeMux 命中模式回填到
入口 span 名与 `http.route`）、`scripts/observability/{install-kind-otel.sh,check-trace-chain.py}`。
本轮同时修掉两个「配置写了但从没被执行」的缺陷：Collector 的 `retry:` 非法键、telemetry 端口双绑，
两者都会让 Collector 起不来——即整条 trace 链路默认是断的。
记录见 [trace-chain-verify.md](./archive/verify/trace-chain-verify.md)。

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
| T3 | 生产实现 | Vault KV v1/v2 适配（HTTP 直连，不引 SDK）；缓存与超时；密钥访问日志 | 依赖故障 fail fast |
| T3b | 云 KMS 适配（延后） | 云厂商 KMS SDK；与 T7 字段加密的 KEK 包装合并评估 | 触发条件：确定云厂商且 T7 上线 |
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

**实施状态**：`脚本/CronJob/runbook 已入库（2026-10-24）`。T1～T3、T5～T7 完成：
`scripts/backup/{backup,restore,verify}.sh`、`deploy/backup/cronjob.yaml`、`backup-restore.md`
（RPO ≤ 5min / RTO ≤ 60min 为基线，待业务确认）；T4/AC-ENT-08-1 的 staging 全量 + PITR 恢复演练待执行。

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
| `ENT-09` | 租户模型与生命周期 | `internal/tenant`；`tenant` / `tenant_quota` / `tenant_member` 表；状态机 `pending → active → suspended → terminated`；与身份、IAM、缓存、令牌联动 | ENT-01、ENT-04 | 停用 ≤ 60s 拒绝访问；退出按策略处置；全链路审计 |
| `ENT-10` | 通知 / Webhook 投递编排 | `internal/notification`：模板、偏好、去重、限流、重试、DLQ、状态查询；复用 `pkg/sms|email|push|webhook` | ENT-03 | 至少一次且幂等；失败可重试 / 补偿；投递指标可见 |
| `ENT-11` | 特性开关与灰度 | `internal/featureflag`：规则、百分比灰度（按 subject 哈希）、审计、到期清理 | ENT-01 | 秒级开关；关闭即回退；无长期遗留 flag |
| `ENT-12` | 数据范围契约 | `Principal` 增加 scope；`internal/shared/authz` 提供 `EnforceMerchant` / `ScopeWhere`；仓储强制过滤 | ENT-01、ENT-09 | 跨租户读写被拒绝；越权自动化用例通过 |
| `ENT-13` | 依赖韧性与第三方集成 | `internal/platform/deps.go` 依赖注册表（超时 / 重试 / 熔断 / 舱壁）；回调验签 + 幂等 + 状态机模板 | ENT-03 | 下游故障快速失败；重复回调不重复处理；依赖指标可见 |
| `ENT-14` | 分布式限流与租户配额 | Redis 全局限流；`tenant_quota` 配额管理；按 channel / tenant / user / 接口分键 | ENT-02、ENT-09 | 多副本共享配额；统一 `42900`；配额变更可审计 |
| `ENT-15` | 数据生命周期与隐私合规 | PII 分类分级、保留策略、DSAR 导出 / 删除、法务保留、脱敏与最小化 | ENT-04、ENT-09 | 删除可验证；保留任务生效；导出经审批并审计 |
| `ENT-16` | 测试基础设施 | `pkg/testkit`（MySQL / Redis / MinIO）；`tests/integration`；`tests/e2e`；契约测试；负载基线 | — | CI 跑真实依赖集成；三端 E2E 主链路；压测基线登记 |
| `ENT-17` | 发布自动化与迁移门禁 | `.github/workflows/release.yml`：staging → prod 审批；迁移前置；金丝雀指标；自动 / 一键回滚 | ENT-02、ENT-05、ENT-16 | 发布可重复；迁移失败阻断；异常自动回滚 |
| `ENT-18` | 值班与事故管理 | on-call 排班、P1/P2 分级矩阵、升级路径、Postmortem 模板、Game Day | ENT-06 | 一次演练完成；P1 5 分钟内触达；复盘行动项跟踪 |

> 依赖修正（2026-11-02）：`ENT-09` 原写依赖 `ENT-01、ENT-12`，与 `ENT-12` 依赖 `ENT-09` 互为前置，形成无法启动的环。
> 按 §5.1 约束 1「租户是唯一隔离边界」，租户表必须先落地，数据范围强制才有所依附，故 `ENT-09` 改为依赖 `ENT-01、ENT-04`
> （验收项「全链路审计」需要可查询的审计出口）。执行顺序固定为 `ENT-09 → ENT-12`。

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
| `make check-secret-provider` | ENT-07 | 生产禁止 env-only；Provider 启动校验 | A（已实现 2026-11-02，下沉到 `make ci`） | T4 |
| `make check-backup` | ENT-08 | 脚本、CronJob、runbook、演练模板、保留期配置 | B | T2 / T3 |
| `make check-e2e` | ENT-16 | 三端 E2E 存在且可运行 | C（nightly） | T3 |
| `make check-load-baseline` | ENT-16 | 压测登记表 ↔ 执行器 ↔ 产物三方对账；孤儿产物；压测不得进默认门禁；k6 阈值与 `identity-slo.md` 一致 | A（已实现 2026-11-04） | T3 |
| `make check-migration-gate` | ENT-17 | 迁移命名/配对、序号连续、append-only、expand-only、NOT NULL 需 DEFAULT、down 可逆、dirty 探测 | A（静态）+ C（含 DSN 探测） | ENT-17 |
| `make check-oncall` | ENT-18 | 事故/值班文档齐备；矩阵 P1 确认 ≤5 分钟且列齐；ALERTS/规则级别都在矩阵内；P1→pager、P2/P3→ticket 路由可解析；复盘模板与台账字段完整；演练 ≥3 场景且引用真实 runbook；交接清单五项；README 无「待填」；Alertmanager 占位地址必须有 open 跟踪项 | A（已实现 2026-11-05） | T3 |
| `make check-sbom` | ENT-19 | SB-01 keyless 前提/`id-token`；SB-02 SBOM 按 digest 且 CycloneDX；SB-03 artifact 与缺文件即失败；SB-04 签名对象为 digest 且晚于构建；SB-05 attestation 类型与 predicate；SB-06 工具版本钉扎；SB-07 验签早于首次 upgrade；SB-08 `verify` 是 staging 的硬依赖；SB-09 验签脚本无后门；SB-10 无 `continue-on-error`/`ignore-tlog`/`|| true`；SB-11 文档与 Checklist | A（已实现 2026-11-05） | T3 |

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
| ENT-07 | `SECURITY_STANDARDS.md` §5、`ARCHITECTURE.md` §10.3、`CONFIG_STANDARDS.md`、新增 runbook |
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
| ENT-04 | P0 | Wave 1 | 完成（2026-11-03：T1～T7 全部交付，`AC-ENT-04-1`～`4` 达成；异步导出任务表 + worker 执行 + 下载，到期「先归档后删除」；`check-audit` 增路由↔OpenAPI↔状态枚举与追加写两项断言） | 审计可查询 / 导出 / 保留 ≥ 180 天 |
| ENT-05 | P0 | Wave 2 | 完成（2026-11-02）——T1～T8 全部落地并在 kind 真集群验证：三套 values `server-side dry-run` 通过（2026-11-08 复核更新：dev 13 / staging 25 / prod 33 资源；离线渲染须带 `--api-versions monitoring.coreos.com/v1`）；真部署 4 端全部 Ready、`/readyz` 五类检查 ok；`helm rollback` 成功；迁移失败时发布被阻断且 API Pod 未被替换。过程中修掉 4 个既有缺陷：`iam.mode: off` 被 YAML 解析成布尔 false（c/worker CrashLoop）；迁移 Job 原为 post-install hook（与 API 并行启动）；ServiceMonitor 无 CRD 检查；worker 未注入 `deps.IDs`（审计保留服务构造失败）。新增 `values-kind.yaml` 与 `docs/deploy-kind-verify.md` 使验证可重复 | Helm 三环境 + 探针 + 资源 + Secret 注入 + 迁移 Job |
| ENT-06 | P0 | Wave 2 | 完成（2026-11-03）——AC-ENT-06-1～4 全部达成：规则 / 看板 / SLO 代码化，kind 真集群注入 5xx、DB 池耗尽、Outbox 积压三类故障均实触发且撤销后自动恢复；37 个看板面板查询错误 0 / 空数据 0；入口到 DB 的 trace 实测贯通（`GET /admin/v1/audit/events` → `sql.conn.query`）。过程中修掉 6 个缺陷：`/metrics` 采集阻塞导致的告警自噬、`db.max_open_conns` 缺失、IAM 快照缓存观测器未接线、shadow 模式不记录鉴权决策、Collector `retry:` 非法键与 telemetry 端口双绑、入口 span 名退化 `GET unknown`。**生产剩余为运维配置项**：Alertmanager 接收端接入 | 告警规则 / 看板 / SLO 代码化并演练 |
| ENT-07 | P0 | Wave 2 | 完成（2026-11-03：T1～T4 全部交付。T3 生产实现以 **Vault HTTP API** 落地，零新增依赖，故不触发 DEP-06 评审——`internal/platform/secrets/vault.go` 自动探测 KV v1/v2，支持单对象（一次请求取全部密钥）与逐键（`<prefix>/<name>` + `value_field`）两种布局，带 TTL 缓存、5s 超时、自定义 CA、令牌文件注入；403 / 5xx / 解析失败一律**硬错误**，`Chain` 只在 `ErrNotFound` 时回落，单对象整体缺失也 fail fast。`secrets.vault.*` 已接入配置，prod 禁显式令牌；`check-secret-provider` 增 8 条断言（含「禁止引入 Vault SDK」与 fail-fast 用例存在性）。**未做**：云 KMS 适配（T7 字段加密需真实云环境时再评估）、不停机轮换演练待 staging） | 密钥托管 + 不停机轮换演练 |
| ENT-08 | P0 | Wave 2 | 完成（2026-11-02）——T1～T6 全部落地：`backup.sh` 加 `--source-data=2` 记录快照位点并写 `<备份>.pos` sidecar（随备份上对象存储，缺失即失败）；`restore.sh` 实现 `PITR_STOP_POS`（字节精确）/ `PITR_STOP_DATETIME`（秒级）两种截断，支持跨 binlog 文件回放，全部参数在覆盖目标库前预检；G1 binlog 归档（5 分钟 CronJob + db-ops 镜像）已端到端验证。PITR 实测：误删未被重放、备份后增量全部找回，27 表结构一致。**生产剩余为配置项而非开发项**：填 `BINLOG_S3_URI`、账号授 `REPLICATION SLAVE` + `RELOAD` + `REPLICATION CLIENT` | 备份 + PITR 恢复演练 + RPO/RTO |
| ENT-09 | P1 | Wave 3 | 完成（2026-11-05，审核流补齐）——T1～T6 交付：`internal/tenant` 四层 + `tenant` / `tenant_member` / `tenant_quota` 三表（迁移 000008 / 000011）+ 权限点 `tenant:read` / `tenant:write`；状态机 `pending → active → suspended → terminated`（终止终态、`version` 乐观锁、停用/终止强制原因）；A 端 11 个端点（租户 / 成员 / 配额）+ `admin.yaml` 7.2.0；成员停用代替删除、最后一名 owner 受保护、配额 `-1` 不限（执行归 ENT-14）；**B 端停用生效**由 `app.Guard` + `platform.WithTenantGuard` 承担（30s 缓存，fail closed），`000010` 回填存量商户。runbook：[tenant-suspend.md](./runbooks/tenant-suspend.md)；决策：[ADR-0006](./adr/0006-tenant-as-isolation-boundary.md)。**后续归属**：终止数据处置 → ENT-15（已完成 2026-11-04：PurgeService + tenant.data_purge 审计）；数据范围强制 → ENT-12（已完成 2026-11-04）；**配额执行（42900）已完成（ENT-14，2026-11-04：席位 T-13 内联 + api.rps 令牌桶 QuotaEnforcer）**；**审核流（2026-11-05 补齐）**：`tenant_approval` append-only 事件表（迁移 000023）+ 四个端点（`GET/POST /tenants/{id}/approval`、`.../approve`、`.../reject`）+ `tenant:approve` 权限（迁移 000024）；`pending → active` 在事务内要求「最新审批为 approved 且 ≥1 名在册 owner」，提交人不得审批自己的申请（违反留 failure 审计）；驳回必填原因、可重提且历史保留；门禁 `check-tenant` 增 TN-04（审批前置存在且先于 owner 校验、路由/权限/文档一致）。退出数据处置由 ENT-15 完成（2026-11-04）。**ENT-09 验收缺口全部关闭**。整改见 [tenant-enterprise-review.md](./archive/tenant/tenant-enterprise-review.md)（B1 修复 3 个 P0；B2（审计 Outbox + 会话撤销 + 登录链路）与 B3 全部完成——T-11 元数据 / T-12 开通前置 / T-13 席位 / T-14 邀请流 / T-15 条件更新 / T-22 current_tenant / T-24 套餐，契约 admin 7.6.0 + biz 6.0.0；B4 治理完成——T-16 指标、T-17 域门禁 check-tenant（已入 make ci）、T-19 状态与 ADR 更正、T-20 runbook、T-23 数据生命周期；T-18 IAM 联动已完成（ADR-0007：成员变更同事务同步商户级模板角色绑定，迁移 000015 存量回填）） | 租户生命周期与 IAM / 令牌联动 |
| ENT-10 | P1 | Wave 3 | **主体完成（2026-11-04，NT-01～NT-10 十个任务包全部落地）**——`internal/notification` 五层 + 迁移 000016～000019（五表 / 邀请模板种子 / 权限点 / 回执索引）；编排管道（幂等续跑 → 过期 → 黑名单偏好 fail closed → 频控 → 熔断发送 → 主备降级 → 单调收敛，预算 6/4/2）；失败 `retry.Permanent` 进 DLQ 保留载荷，Replay 补偿；租户邀请邮件端到端接入（ADR-0007 事务内 fail closed）；回执回调公网端点（HMAC + nonce，密钥缺失拒启）；API 面 admin 8.0.0 / biz 6.1.0 + 权限 `notification:read/write`；`check-notification` 门禁入 make ci；ADR-0008 + runbook；DirectSender 同步旁路受控门面（identity 热路径不迁移，例外固化 ADR-0008 §3）。决策与遗留：[ADR-0008](./adr/0008-notification-event-driven.md)、[notification-center-plan.md](./archive/plans/notification-center-plan.md)（v1.2）。**遗留**：MySQL 集成用例（归 ENT-16）、生产渠道驱动装配（依赖渠道凭据，配置项非开发项） | 通知 / Webhook 投递编排与状态 |
| ENT-11 | P1 | Wave 3 | **主体完成（2026-11-04，FF-01～FF-08 全部落地）**——`internal/featureflag` 五层：域模型（key 正则 / 治理状态机 enabled→disabled→expired 终态 / permanent 与 expires_at 二选一）；评估引擎（kill switch 无条件优先 → 规则按序 → default 回退；SHA-256 万分比稳定分桶，同主体稳定、上调只增不减、subject 缺失 fail closed；属性测试锁定单调性与均匀性）；两级 TTL 缓存（kill switch 5s / 规则 30s，判据「秒级开关」）；管理面 CRUD + 乐观锁 + `featureflag.*` 六动作审计（Outbox 优先 fail closed）；到期治理任务（worker 周期扫描 + 元审计）；API admin 8.1.0 + 权限 `featureflag:read/write`（000021 四处同步）；`check-featureflag.sh` 门禁入 make ci（key 命名 / EVENTS 对账 / 幽灵 key / 到期断言）；ADR-0009（进程内评估 + TTL，不引外部平台）+ runbook；首个调用方 = 租户邀请邮件灰度开关（消费方端口 + noop 注入，evaluator 未注入沿用 ENT-10 语义）。**遗留清账**：MySQL 集成用例已补（2026-11-04，`tests/integration/featureflag`：规则往返 / CAS / 治理落库 / ListExpired 扫描） | 特性开关与灰度可运营 |
| ENT-12 | P1 | Wave 3 | 完成（2026-11-04）——`internal/shared/authz` 补齐执行原语：`ScopeWhere`（范围 → SQL 谓词，`Clause` 生成 `IN` 片段）、`Snapshot.EnforceMerchant`、`EnforceFromContext`（快照缺失放行，RequirePermission 兜底），越界统一 `ErrScopeDenied` → 40300；业务接线：租户域全部写用例经 `mutableTenant` 汇聚点强制 + 成员/配额/邀请三个读路径，通知 transport（查询/代发目标租户）接线；仓储强制过滤：通知域两实现对空租户键 fail closed + SQL 钉死 `tenant_id = ?`。越权自动化用例三处（authz 单测 / 租户 app 越权读写 / 通知 handler 403）；`make check-authz` 入 make ci（AZ-01～04 四项对账）。决策记录：rbac-iam.md §4.4 第 7 条（self 范围 v1 租户维度执行）。**后续归属**：iam / audit 等其余域的仓储织入随各自域演进逐批接入（原语已就绪） | 数据范围在业务查询 / 写入强制 |
| ENT-13 | P1 | Wave 3 | 完成（2026-11-04）——`internal/platform/dependency` 依赖注册表：`Spec{Name, Timeout, Retry, Breaker}` 装配期登记（名全局唯一、低基数指标标签），`Caller.Do` 执行链 = 超时兜底（5s 默认）→ 熔断快速失败（`pkg/circuitbreaker`，业务拒绝类错误经 `IsSuccessful` 不计入）→ 受控重试（`pkg/retry`，对齐 REL-03 只重试显式 Retryable 错误 + `Permanent` 终止非幂等）；指标 `dependency_call_total{name,result: ok/error/breaker_open}` + `dependency_retry_total`（bootstrap 构造注入 `Deps.Dependencies`）。回调模板三件套固化到 RELIABILITY_STANDARDS §5.1（验签 / 幂等吸收 / 状态机单调，参照实现 = notification callback）；`check-resilience.sh` 门禁入 make ci（RS-01 注册表接线 / RS-02 回调三件套 / RS-03 裸出站 HTTP 扫描，白名单 oauth+Vault 已端口化）。测试：注册表 6 用例（重复登记 / 未登记快速失败 / 结果三分类观测 / 业务错误不熔断 / 重试+Permanent / 超时强制） | 依赖韧性接线 + 回调幂等模板 |
| ENT-14 | P1 | Wave 3 | 完成（2026-11-04）——`tenant/app.QuotaEnforcer` 配额执行器：限流型资源（`api.rps` 约定键）按分键令牌桶扣减（键 `quota:{tenant}:{resource}[:{dimension}]`，channel/tenant/user/接口分键齐备），计数型资源 `Check` 静态校验；上限解析 配额行→默认值→不限（30s TTL 缓存防配额表热点），仓储/后端故障 fail-open（Redis 后端可用 failure_mode=fail_closed 收紧）；超限统一 42900 + Retry-After。多副本共享：`pkg/ratelimit.RedisKeyed` 增 `AllowNWithParams` 键级参数执行（Lua 原子不变，配额按租户异容量），tenantruntime 装配 Redis 后端；HTTP 守卫 `platform.WithQuotaGuard`（接口反转，包在 TenantGuard 内层——已停用租户不消耗配额判定），biz-api 注入。席位计数执行 T-13 已内联（同口径）。配额变更审计 T-13 已有（`tenant.quota_change`）。测试：执行器 5 用例（限流/不限与默认/Check/fail-open/维度分键）+ 守卫 4 用例（放行/429+Retry-After/fail-open/平台级跳过） | 分布式限流与租户配额 |
| ENT-15 | P1 | Wave 3 | **主体完成（2026-11-04）**——PurgeService 五步证据链（扫描 ListDueForPurge → 导出证据校验（无 manifest 绝不裸删，SHA 一致性复核）→ 按边界顺序删除（租户四表 / IAM 商户角色与绑定 / identity 商户账号，级联 RESTRICT 表经 join 清理）→ `tenant.data_purge` 审计（EVENTS.md 登记）→ 处置报告）；迁移 000022 `tenant_export_manifest`（D-05 导出证据）；`tenant.retention.*` 配置（hold/export/batch/dry_run + validate 边界）；worker 周期任务（1h）；PII 分类分级登记 `docs/data-inventory.md`（P0–P3 六域全量登记 + DSAR 路径）；审计保留/归档/导出复用 ENT-04 既有件；测试：app 5 用例（证据铁律/审计字段/失败中止/证据损坏）+ 集成 2 用例 | 数据分类 / 保留 / 导出 / 删除 |
| ENT-16 | P1 | Wave 0 / 3 | 完成（`pkg/testkit`（MySQL / Redis / Storage / Repo）+ `tests/integration/{async,iam,identity,multiinstance,smoke,tenant,notification,featureflag}` + `tests/load` 已落，2026-11-04 补齐通知 / featureflag 两域 MySQL 集成矩阵并偿还 ENT-10/11 挂账；同日补 `tests/contract`（OpenAPI ↔ 实现路由双向漂移检测，静态无依赖随 CI 全量跑；上线即修复 biz.yaml auth/login 断链与 3 条 B 端角色模板路由漏登，biz.yaml 6.2.0）；`tests/e2e` 三端主链路（真实二进制 × MySQL8 × Redis，探针 → 未认证拒绝 → 引导账号首登强制改密票据链 → 租户创建/读取 → 通道隔离，`make check-e2e` nightly，单轮 ~16s，含口令轮换幂等）；**压测基线登记已补（2026-11-04）**：`tests/e2e/load_baseline_test.go`（真实二进制 × MySQL × Redis，固定到达率压已认证读路径，产物 JSON 落 `tests/load/artifacts/`）+ 登记表 [perf/LOAD_BASELINES.md](./perf/LOAD_BASELINES.md)（本机 50/100/200/500 rps 四档实测 + staging k6 三场景待执行，不伪造数值）+ `make load-baseline`（手动）+ `make check-load-baseline`（登记一致性门禁，入 `make ci`；压测本身按 PERF-06 不进默认门禁）。**ENT-16 完成**） | 集成矩阵 + E2E + 契约 + 压测 |
| ENT-17 | P1 | Wave 4 | 完成（2026-11-04）——`.github/workflows/release.yml` 八阶段：`plan`（semver 校验）→ `preflight`（`check-migration-gate` + `check-release`）→ `digests`（按 `sha256:` 解析，禁止浮动 tag）→ `staging`（Environment 审批 + `--atomic --wait` + 冒烟 + 可选部署后 schema 探测）→ `canary`（独立 release 单副本 + 金丝雀冒烟 + 指标门禁）→ `promote`（全量 + 冒烟 + 生产指标门禁）→ `rollback`（卸载金丝雀 + `helm rollback <release> 0` + 回滚后冒烟）→ `notice`（关闭发布开关时的显式说明）。迁移门禁 `scripts/ci/check-migration-gate.sh`（MG-01～07：命名配对 / 序号连续 / append-only / expand-only / NOT NULL 需 DEFAULT / down 可逆或声明 / dirty 探测），已入 `make ci`；`values-canary.yaml`（独立 release、单副本、不跑迁移、无 Ingress/HPA/PDB）；`release-smoke.sh`（探针 / 版本一致 / 未认证 401）；`release-canary-gate.sh`（流量下限 + 5xx + p95，缺 `PROM_URL` fail closed）；`check-release.sh` 扩 RELS-09～12 并逐条证伪（少 `--atomic`、金丝雀开迁移、门禁脚本去掉 fail-closed、迁移门禁移到部署之后均被拦）。**遗留**：staging/production 集群凭据与 `RELEASE_ENABLED` 属仓库配置，未配置时流水线走 `notice` 并写明原因 | CD + 迁移门禁 + 金丝雀 / 回滚 |
| ENT-18 | P1 | Wave 4 | 完成（2026-11-05）——`docs/incident/` 四件套 + `docs/oncall/ROTATION.md`：分级矩阵（P1 确认 ≤5 分钟、首次通报 ≤15 分钟、更新 30 分钟、L2 15 分钟升级；「告警级别 ≠ 事故级别」可升可降须记录）；响应流程（检测→定级→声明→缓解→恢复→关闭→复盘七阶段，IC/OL/Comms/Scribe 四角色、`INC-YYYYMMDD-NN` 标识、时间线七个必备标记、关闭六条件、发布冻结与回滚优先）；复盘模板（三段耗时 + 5 Whys + 可验证行动项）；行动项台账（6 条 open，覆盖 pager 占位地址、值班入口、DLQ 重放进度告警、fail-closed 阈值、staging 实战、每月呼叫测试）；排班交接（周轮换主备、5 分钟接管、五项交接清单、每月 paging 自检）；Game Day 三场景（G1 金丝雀回滚 / G2 DLQ 重放 / G3 撤销存储 fail-closed）+ 首次桌面推演记录（离线自检 3×7 断言全 PASS，实测 1584/1631/1650 ms，**未伪造实战耗时**）。工具：`paging_probe.py`（自主解析 Alertmanager 路由树，识别占位地址）、`game-day-drill.sh`（剧本依赖离线自检）、`check-oncall.sh`（OC-01～08 入 `make ci`，9 条证伪）。**挂账**：staging 实战演练（AI-2026-11-05-05）与 pager Webhook 替换（AI-01）属环境配置与实战项 | on-call + 分级 + 复盘 + 演练 |
| ENT-19 | P2 | Wave 4 | 完成（2026-11-05，SBOM + 签名 + 部署前验签；DAST 归 ENT-20）——`image.yml`：Trivy CycloneDX SBOM 按 **digest** 生成并上传 artifact（`if-no-files-found: error`）、cosign keyless 签 digest、SBOM 以 `cosign attest --type cyclonedx` 挂到同一 digest，工具版本钉扎（`COSIGN_VERSION`）；`release.yml` 新增 `verify` job 并于**首次 helm upgrade 之前**调用 `scripts/ci/verify-supply-chain.sh`（身份=本仓库 image.yml、issuer=GitHub Actions、拒绝 tag 引用与通配身份、校验 SBOM 载荷内容），`staging` 的 `needs` 含 `verify`；门禁 `check-sbom.sh`（SB-01～11）入 `make ci` 与 arch 作业，13 条证伪；本地等价演练 `supply-chain-rehearsal.sh` 11/11 PASS（本地 registry + 密钥对，不含 OIDC）。**挂账**：keyless 首次真跑（SC-01）、私有 registry referrers（SC-02）、SLSA provenance（SC-03）见 [release/supply-chain.md](./release/supply-chain.md) | SBOM / 签名 / 部署前验签（DAST 另立） |
| ENT-20 | P2 | Wave 4 | 待做（无 WAF / 网关 / mTLS 配置；触发条件：公网暴露或等保测评） | WAF / 网关 / mTLS / 安全头 |
| ENT-21 | P2 | Wave 4 | 待做（无 `.github/CODEOWNERS`；分支保护待管理员勾选） | CODEOWNERS + 分支保护 + 文档站点 |
| ENT-22 | P2 | Wave 4 | 待做（无容量模型 / 成本标签；触发条件：放量） | 容量模型 + 成本治理 |
| ENT-23 | P2 | Wave 4 | 待做（单区域单库；触发条件：可用性目标提升） | 读副本 / 多区域 / 切换演练 |
| ENT-24 | P2 | Wave 4 | 待做（无 CDC / ETL；触发条件：运营分析或对账） | CDC / 数仓 / 报表 |
| ENT-25 | P2 | — | 暂缓 | 先立 ADR，再评估 ABAC / 组织树 |


### 执行记录

| 日期 | 事项 | 状态 | 备注 |
|---|---|---|---|
| 2026-10-22 | 规划文档创建（v0.1） | 完成 | 基于差距评估 v0.2；拆分为 Wave 0～4 与 25 个任务包 |
| 2026-10-22 | ENT-02 多实例运行语义落地 | 完成 | NodeID 配置化 + Deps.IDs、Redis 幂等存储、Redis 令牌桶、`publish_in_api` / worker 发布器、multiinstance 集成用例与 `check-idempotency` 门禁；T4 逐 operation 标注单独跟进 |
| 2026-10-22 | ENT-03 通用异步基础设施落地 | 完成 | `pkg/outbox`（Store/Relay/Router/Deduper/DLQ）、`AsyncRuntime`、`async.*` 配置、IAM 完整迁移（已并入 `000005` 基线）、`check-outbox` 门禁与 `async-dlq` runbook；集成用例覆盖 AC-ENT-03-1～5 |
| 2026-11-02 | 全量状态核对（代码 vs 状态表） | 完成 | 逐条比对仓库实现，修正 8 条滞后状态：`ENT-04` 待做→进行中（`identity_audit_event` + `shared/audit/sqlstore.go` 已落，T4～T7 仍缺）、`ENT-12` / `ENT-14` / `ENT-16` 待做→进行中（`shared/authz`、`pkg/ratelimit`、`pkg/testkit` + 集成用例已落）、`ENT-05/06/08` 补齐「仅剩 staging 演练」口径、`ENT-07/18` 标注已就绪的代码切片。核对命令：`ls internal/ pkg/ tests/`、`grep -rn "audit_events\|tenant" migrations/`、`find . -name release.yml` |
| 2026-11-02 | ENT-04 审计查询 / 导出 / 保留落地 | 部分完成 | 新增 `internal/audit`（domain/app/infrastructure/transport）与 `cmd/internal/auditruntime`；`GET /admin/v1/audit/events`（时间窗必填、31 天上限、`audit:read`）、`GET /admin/v1/audit/events/export`（CSV 流式、`max_rows` 超限拒绝、导出记 `data.export`）；worker 侧 `audit.retention` 分批清理 + `dry_run` / `legal_hold` 与 `audit.retention_purge` 元审计；迁移 `000006` 把 `identity_audit_event` 重命名为 `audit_event` 并新增 action / channel 索引。验证：`go build ./...`、`go test ./internal/audit/... ./internal/platform/...`、`golangci-lint run ./internal/audit/...`（0 issues）、`make check-audit / check-layering / check-transport / check-config / check-migrations` 通过。未含：异步导出任务（复用 ENT-10 编排）、哈希链（T3 可选项目前仍关闭） |
| 2026-11-02 | ENT-07 密钥 Provider 基础层 | 部分完成 | 新增 `internal/platform/secrets`：`Provider`（Get / GetBytes / Close）、`Resolver.Require`（启动期批量取密钥，缺失即失败且错误只含密钥名）、`Chain`（显式顺序组合，非 NotFound 不降级）、`EnvProvider`（名单映射 `<PREFIX>NAME`）、`FileProvider`（子目录布局、去尾随换行、拒绝目录穿越 / 绝对路径 / 盘符）；补 `docs/runbooks/key-rotation.md`、`key-compromise.md`（此前在计划中被引用但文件不存在，RB-02/RB-03 现已通过）。验证：`go test ./internal/platform/secrets/...`、`golangci-lint run ./internal/platform/secrets/...`（0 issues）、`make check-runbooks / check-doclinks` 通过。未含：T3 Vault / KMS 适配（需新增依赖，按 DEP 走评审）、T4 配置接入（`db.dsn` / JWT / 摘要密钥统一走 Provider） |
| 2026-11-02 | ENT-07 T4 密钥托管接入 | 部分完成 | `internal/platform/config_secrets.go` 新增 `secrets.*` 配置段（`providers` / `env.prefix` / `file.dir` / `allow_env_fallback`）与 `secretTargets` 映射表（db_dsn / auth_jwt_secret / auth_jwt_secrets / identity_hash_key / identity_mfa_secret_key / redis_password / storage_s3_*）；`normalize` 阶段解析并**覆盖**配置文件与环境变量取值，解析错误经 `secretErr` 汇入 `Validate`；`LoadDBConfig` 走同一 Provider，避免「服务端读文件、迁移仍读 env」的分裂。Helm 新增 `secretMounts`（projected，defaultMode 0400）投影到 `/var/run/secrets/admin-frame`，工作负载与迁移 Job 同时挂载。新增 `scripts/ci/check-secret-provider.sh` 与 `make check-secret-provider`（已并入 `make ci`）：校验抽象存在、已接启动路径、prod 样例声明 file 来源、Helm 挂载文件、样例无明文。验证：`go test ./internal/platform/...`（含 9 个新用例）、`go build ./...`、`make check-config / check-deploy / check-secret-provider / check-doclinks / check-runbooks` 通过；门禁经「先证伪」确认能报出缺失（初次运行报 4 项，补齐后 PASS）。未含：T3 Vault / KMS 适配（需 DEP 评审）、identity.sms / OAuth 凭证的 Provider 映射（identity 默认关闭，开启时补） |
| 2026-11-02 | ENT-08 本地恢复演练 | 部分完成 | 环境：Docker `mysql:8.0`（8.0.46，`ROW` binlog）+ 6 迁移 + 1000 审计 + 200 绑定。实测：备份 41KB/4s、`verify --load` 3s、全量恢复 3s、加密备份 4s、加密恢复 6s；27 表行数/列数/索引数与 `CHECKSUM TABLE` 内容全部一致（0 不一致）；恢复后 `migrate status` 为 `current=000006 dirty=false pending=none`；锁与 `RESTORE_CONFIRM` 保护有效。**修复 4 个真实缺陷**：① `backup.sh` 只认 `mysql://`、项目 DSN 直接失败 → 新增 `scripts/backup/dsn.sh` 统一解析；② `restore.sh`/`verify.sh` 把 DSN 当库名传给 `mysql`（`ERROR 2002`）→ 拆成 `--host/--port/--user` + 库名；③ 加密失败残留完整明文 `.gz` → 失败时删除明文与残片；④ `trap rm -rf` 失败改变退出码 → `\|\| true`。以上固化进 `make check-backup` 并做证伪（移除 `source` 后门禁报错）。**未关闭**：G1 无 binlog 归档作业（RPO 实际 24h，未达 5min）；G2 全备未记录 binlog 位点（账号无 `RELOAD`）；PITR 回放未执行（镜像缺 `mysqlbinlog`）。详见 `docs/runbooks/backup-restore.md`「演练记录」 |
| 2026-11-02 | ENT-08 G1 binlog 归档与运维镜像 | 部分完成 | 新增 `deploy/backup/Dockerfile`（`mysql:8.0-debian` 取客户端 → `debian:bookworm-slim`，只拷 `mysql`/`mysqlbinlog`/`mysqldump` + aws cli v2 按 `TARGETARCH` 安装，脚本随镜像分发，非 root 10001 运行，实测 545MB）；新增 `scripts/backup/binlog-archive.sh`（复用 `dsn.sh`，`FLUSH BINARY LOGS` 封口 + 跳过活跃文件 + S3 幂等 + 大小一致校验 + 拒绝覆盖不同大小对象）；`deploy/backup/cronjob.yaml` 增加 `admin-frame-binlog-archive`（`*/5`，与全量备份共用 `db-ops` 镜像）。端到端验证（MySQL 8.0.46 `ROW` + MinIO）：首次归档 4 文件 3.08MB、二次运行幂等（归档 1 / 跳过 5）、8 个对象 `sha256sum -c` 通过、**仅用归档 binlog 回放到空白实例后 27 表/5 绑定/3 审计/20 菜单/30 权限/schema v6 与源库逐项一致**。门禁新增 4 条断言并证伪（改 CronJob 名后报错）。未含：生产 `BINLOG_S3_URI` 与账号权限（运维配置）、PITR 按时间点截断（待 staging 真实时间点） |
| 2026-11-02 | ENT-08 收口：G2 位点记录 + PITR 截断回放 | 完成 | `backup.sh` 加 `--source-data=2`，从 dump 头解析快照位点并写 `<备份>.pos`（key=value，随备份上传 S3）；解析失败默认硬失败（`BACKUP_REQUIRE_POSITION=1`），避免产出「存在但无法前滚」的备份。`restore.sh` 新增 `pitr_precheck`（参数互斥/格式、`.pos` 可用性、`mysqlbinlog` 存在、`SHOW BINARY LOGS` 起止文件存在且有序）与 `pitr_replay`（按文件逐个回放，起始文件带 `--start-position`、目标文件带 `--stop-position`/`--stop-datetime`），并新增 `PITR_CONFIRM` 独立确认。实测（MySQL 8.0.46 ROW binlog + db-ops 镜像）：跨文件回放 000004→000005 共 14 s，误删的 10 条 baseline 未被重放、备份后 5 条绑定 + 3 条审计全部找回、27 表结构一致；`--stop-datetime` 模式同样通过。失败路径（参数互斥/缺 `.pos`/缺确认/binlog 已 purge）均验证为**在覆盖目标库之前**拒绝（canary 行未被清除）。演练中修掉 3 个自身缺陷：`if ! "$var" =~ re` 在 `[[ ]]` 外被当作执行命令；参数校验原在恢复之后（会先毁库再报错）；门禁断言可被注释行假绿（改为 `code_has` 只匹配非注释行）。权限实测结论：`--source-data=2` 需 `RELOAD` + `REPLICATION CLIENT` 两项（缺任一项 mysqldump 直接非 0 退出）。验证：`make check-backup`（含 8 条新断言 + 3 条证伪）、`check-doclinks`、`check-runbooks` 通过 |
| 2026-11-02 | ENT-05 收口：kind 真集群部署验证 | 完成 | 环境：`kindest/node:v1.34.3`（kind 0.30.0）+ `prometheus-operator-crds` + 集群内 MySQL/Redis。三套 values 的 `helm install --dry-run=server` 全通过（dev 13 / staging 24 / prod 32 资源，staging/prod 各含 1 个 ServiceMonitor）。真部署：迁移 Pod `13:14:26` → API Pod `13:14:30`（先后 4 s），4 端全部 Ready，`/readyz` 返回 `db/storage/redis/iam_db/iam_cache` 五项 ok；`helm upgrade`（replicas 1→2）→ `helm rollback` 成功；把 DSN 指向不存在主机后 `upgrade --rollback-on-failure` 报 `pre-upgrade hooks failed`，API Pod AGE 不变、`/readyz` 仍 ok（迁移失败阻断发布已验证）。**修掉 4 个既有缺陷**：① `iam.mode: off` 裸值被 YAML 1.1 解析成布尔 false，c 端与 worker CrashLoop（8 处加引号）；② 迁移 Job 原为 `post-install` hook，与 API 并行启动 → 改 `pre-install,pre-upgrade`，新增 `migrate-config.yaml`(-10)，ServiceAccount 改 pre-hook(-20)（否则 Job 卡在 Running 且无 Pod：serviceaccount not found）；③ `ServiceMonitor` 缺 CRD 检查 → 加 `.Capabilities.APIVersions.Has` + 可读 fail 信息 + `allowMissingCRD` 开关 + NOTES.txt；④ `cmd/worker` 自建 `platform.Deps` 漏 `IDs`，审计保留服务报 `requires an id generator` → 补 `NewIDGenerator`。新增 `make check-deploy` 断言 6 组（hook 顺序/权重、CRD 检查、YAML 布尔陷阱），三条已证伪。验证：`go build ./...`、`make check-deploy`、`check-doclinks` 通过 |
| 2026-11-03 | ENT-07 T3 Vault 生产实现（HTTP，零新增依赖） | 完成 | 原判定「T3 需走 DEP 依赖评审」的根因是引入 Vault/KMS SDK 属新增直接依赖（DEP-06）；本次改为直接调 Vault HTTP API（`net/http` + 仓库内 `pkg/httpclient`），`go.mod` 无任何变化，评审面消失。新增 `internal/platform/secrets/vault.go`（KV v1/v2 自动探测并记忆、单对象 / 逐键两种布局、`objectFields` 对象级缓存使缺字段也不重复拉取、5s 超时、自定义 CA、`token` / `token_file` / `token_env` 三级令牌来源、访问日志只输出 mount/path/字段数）；404 与 403/5xx 严格分离——逐键模式 404→`ErrNotFound`（允许 Chain 继续），单对象整体缺失、鉴权失败、传输/解析失败→硬错误（fail fast，禁止回落 env）。`config_secrets.go` 增 `secrets.vault.*` 与 `SecretProviderVault`，prod 拒绝显式令牌（SEC-05），`LogValue` 只输出令牌来源不输出取值。`check-secret-provider.sh` 增 8 条断言：vault.go 存在、`NewVaultProvider`、`X-Vault-Token`、config 注册 vault、**禁止 import Vault SDK**、`chain.go` 仅按 `ErrNotFound` 回落、`errVaultNotFound` 区分、两个 fail-fast 用例存在。同时修掉 §10.3 引用漂移（该节在 `ARCHITECTURE.md` 而非 `SECURITY_STANDARDS.md`）。验证：`go test ./internal/platform/secrets/ ./internal/platform/` 全通过（新增 8 个 vault 用例 + 3 个配置用例）、`make check-secret-provider` 通过、`go build ./...` 通过、`git diff go.mod go.sum` 为空 |
| 2026-11-03 | ENT-09 T1～T4 租户生命周期（A 端切片） | 部分完成 | 新增 `internal/tenant`：`domain`（`Tenant` 实体、`Status` 四态状态机、`Filter`、`Repository` 契约；非法迁移返回原实体而不是零值，避免调用方拿到空结构）、`app`（创建 / 查询 / 列表 / 迁移用例，`tenant.*` 审计，领域错误统一翻译 400/404/409）、`infrastructure/persistence`（memory + MySQL，`UPDATE … WHERE version = ?` 做 CAS，0 行时区分「不存在」与「版本冲突」）、`transport/{rest,admin}`（6 个端点，读 `tenant:read` / 写 `tenant:write`）。迁移：`000008` 建 `tenant` 表（状态 + 名称索引、`version` 乐观锁、`suspended_at` / `terminated_at` 留痕）、`000009` 种入两个权限点并授予 `system_admin`（同步 `internal/iam/perm.go`、`catalog`、`docs/iam/PERMISSIONS.md` 四处）。契约：`api/openapi/{paths,schemas}/admin/tenant.yaml` + `admin.yaml` 7.1.0（纯增量）。装配：`cmd/internal/tenantruntime` 接入 `admin-api`，DB 可用走 MySQL、内存模式走 memory（审计退化为结构化日志）。决策：[ADR-0006](./adr/0006-tenant-as-isolation-boundary.md)（`tenant.id` 即 `merchant_id`；不建映射表、不加 `tenant.code`；不预留 member/quota 死表）。验证：`go test ./internal/tenant/...`（domain 8 组、app 8 个、persistence 4 个、transport 7 个用例）、`go test ./internal/iam/catalog/`（权限四方一致）、`golangci-lint` 0 issues、`make check-migrations / check-openapi / check-audit / check-layering / check-doclinks` 通过。未含：停用 ≤60s 生效（需状态缓存与令牌联动）、`tenant_member` / `tenant_quota`、B 端成员接口 |
| 2026-11-03 | ENT-09 T5 停用生效链路（守卫 + 回填） | 部分完成 | 新增 `app.Guard`：按 `merchant_id` 查状态，30s 正缓存 / 5s 负缓存，**fail closed**（仓储故障返回错误→503，不缓存、不放行），`Invalidate` 供同进程迁移即时失效；`Verify` 把「明确拒绝」与「无法判定」分开，避免数据库抖动被误报成停用。platform 新增 `TenantGuard` 接口与 `WithTenantGuard`（包在鉴权中间件内层，`merchant_id` 为空直接放行），`Options.NewTenantGuard` 在 AuthMW 组装前注入；biz-api 接入，admin / c 端不接。`app.Service` 迁移成功后调用 `Invalidator`。`migrations/000010` 回填：把 `iam_role` / `iam_role_binding` / `identity_account` 中非空 `merchant_id` 登记为 `active` 租户（幂等、不覆盖已存在记录）——守卫开启后未登记商户会被拒，必须先回填。新增 runbook [tenant-suspend.md](./runbooks/tenant-suspend.md)（含 403/503 区分与回填步骤）。验证：单测（守卫 9 组 + 中间件 5 组 + 失效 1 组）、**真实 MySQL 集成用例 2 个通过**（pending 拒绝 / active 放行 / 停用后本进程立即拒绝、副本 TTL 内放行、TTL 到期拒绝；回填迁移幂等且状态为 active）、`golangci-lint` 0 issues、`make check-migrations / check-runbooks / check-doclinks / check-layering` 通过。未含：成员与配额表、终止数据处置 |
| 2026-11-03 | ENT-09 收口：成员与配额 | 完成 | 迁移 `000011` 建 `tenant_member`（PK `(tenant_id, subject_id)`、停用不删除、`idx_tenant_member_role_status` 支撑 owner 计数）与 `tenant_quota`（PK `(tenant_id, resource)`、`limit_value=-1` 表示不限），外键级联到 `tenant`。domain 增 `Member`（owner/admin/member，角色与状态枚举）与 `Quota`（资源键限定 `a-z0-9._-`，防指标基数爆炸），「至少一名在册 owner」由应用层保证（SQL 无法表达跨行不变量）。仓储 memory + MySQL 双实现，`INSERT … ON DUPLICATE KEY UPDATE` 幂等且**不重置 `created_at`**；成员列表按 `FIELD(role,…)` 排序使 owner 在前。用例：`MemberService`（Add / List / SetRole / SetStatus，重复添加 409、停用后可复活并标 `reactivated`）、`QuotaService`（Set / List）；终态租户拒绝任何成员与配额变更。接口：5 个新路由，读沿用 `tenant:read`、写沿用 `tenant:write`（不另开 `tenant:member:*`，避免两个授权面）；`admin.yaml` 7.2.0 纯增量；`EVENTS.md` 增 5 个动作。验证：单测（成员 5 组 + 配额 1 组 + HTTP 2 组）、**真实 MySQL 集成用例 3 个全通过**（含成员排序 / 停用过滤 / 末位 owner 保护 / 配额不限量）、`golangci-lint` 0 issues、`check-openapi / check-migrations / check-audit / check-layering / check-doclinks / check-runbooks` 通过 |
| 2026-11-04 | ENT-16 收尾：压测基线登记 | 完成 | 补齐 ENT-16 最后一项缺口（此前「缺压测基线登记」）。执行器：`tests/e2e/load_baseline_test.go`——复用 E2E 的进程编排（真实二进制 × MySQL 8 × Redis 7），以**固定到达率**（令牌间隔发放，闭路循环会被服务端时延反向钳制）压已认证读路径 `GET /admin/v1/tenants?page_size=20`，覆盖鉴权 → 权限快照 → 数据范围谓词 → 仓储 SQL → 信封序列化；预热 5s 丢弃、连接池 = 2×到达率、不走代理；产物 JSON 记录分位 / 成功率 / 错误数 / Go 版本 / CPU 数。**实测（DEV-01：Windows 11 / 12 vCPU / Go 1.26.10 / docker MySQL 8 + Redis 7 / loopback）**：50 rps → p50 3.17 / p95 6.37 / p99 8.78 ms；100 rps → 6.26 / 8.99；200 rps → 6.03 / 8.94；500 rps → 11.14 / 44.56（max 164ms，尾部拉长而非整体抬升）。样本 1000/1995/4000/7476，成功率 100%、5xx=0。判读：50～200 rps 近似平权（无排队），拐点在 200→500 之间且形态指向客户端与服务端争抢 CPU，应用层不是瓶颈；容量规划按 DB 连接预算 + 副本数推进（ENT-22 接手）。**没有压登录**：Argon2 成本随 CPU 核数剧烈波动，登录 / 刷新 / MFA 的 SLO 只登记 staging k6 读数（三行登记为「待执行」并留 `-` 产物位，**不填估算值**）。登记表 [perf/LOAD_BASELINES.md](./perf/LOAD_BASELINES.md)（环境规格 / 判读 / 复现命令 / 回归口径 p95 恶化 >30% 可疑 / MUST NOT）。门禁 `scripts/ci/check-load-baseline.sh`（入 `make ci`）六条：登记行格式与执行器真实性、通过行必须有产物且 `samples>0 / errors=0 / success_rate≥0.99`、孤儿产物、**压测不得进默认门禁**（按独立 target 词匹配，`check-load-baseline` 不误伤）、待执行行产物必须为 `-`、k6 阈值与 `identity-slo.md` §1 数值一致。三条已证伪（孤儿产物 / 把 `load-baseline` 塞进 ci / k6 阈值与 SLO 偏移）。验证：`make load-baseline` 四档实跑通过、`make check-load-baseline` 通过、`go vet ./tests/e2e/`、`golangci-lint` 0 issues |
| 2026-11-04 | 门禁债清偿（压测基线落地时暴露） | 完成 | 为让 `make ci` 真正全绿，修掉 5 类历史欠账：① **domain 覆盖率**——audit/domain 64.3%→100%、featureflag/domain 67.6%→91.2%、notification/domain 56.7%→78.5%、tenant/domain 35.3%→94.9%（TEST-02 门槛 70%）；新增 6 个测试文件，覆盖成员/邀请/配额/套餐/导出证据/投递状态机/分桶单调性与均匀性，并补 `SeatLimitError.Unwrap` 与哨兵互斥断言。② **gitleaks**——`tests/e2e` 三处 JWT 字面量改为 `strings.Repeat` 构造（`e2eJWTSecret`），历史提交向量按既有惯例进 `.gitleaks.toml` 白名单并注明原因。③ **审计动作缺失**——`auth.tenant_switch` 只存在于 identity 本地常量，未进 `shared/audit` 目录与 `KnownActions`（AUD 铁律 + check-authn 失败）；补 `ActionAuthTenantSwitch` 并改用目录常量。④ **门禁静默失败**——`check-authn.sh` 的 `grep \| head \| sed` 在无匹配时被 `set -e` 直接终止，导致「常量缺失」表现为「无输出退出」；补 `\|\| true` 并证伪（删常量后能打印 ERROR）。⑤ **OpenAPI 契约断链**——B 端 paths 跨引 `schemas/admin`（notification / role templates），bundle 出现 Role / VersionInfo / SubjectTypePath 同名不同源冲突；下沉两份 schema 到 `schemas/biz`、修正 biz `Role.scope_type` 枚举（`admin`→`platform`，与 `iam/domain.RoleScopeType` 对齐），并新增 API-13 门禁（B/C 端不得引用 `schemas/admin`）；bundle 工具改为「先展开后比较 + 规范化 JSON 比较 + 忽略 example」，不再把注释/示例差异误判成冲突。验证：`make ci`（GOPROXY=goproxy.cn）全绿、`make check-e2e` 通过、`make load-baseline` 四档通过 |
| 2026-11-04 | ENT-17 发布自动化与迁移门禁 | 完成 | `release.yml`（plan/preflight/digests/staging/canary/promote/rollback/notice）：迁移门禁先于任何 `helm upgrade`；镜像按 digest 部署；staging 与 production 均走 Environment 审批；`--atomic --wait` 覆盖全部三次 upgrade；指标门禁通过后才放量，失败由 `rollback` job 兜底（卸载金丝雀 + `helm rollback 0` + 回滚后冒烟）。迁移门禁 `check-migration-gate.sh` 七条（MG-01～07），已入 `make ci`，并逐条证伪：`DROP COLUMN`、`ADD COLUMN NOT NULL` 无默认值、删除既有迁移、缺 down 均被拦。金丝雀：`values-canary.yaml` 渲染实测 1 Deployment / replicas=1 / 0 Job / 0 Ingress / 0 HPA，digest 钉住；独立 release 规避 `Deployment.spec.selector` 不可变问题。冒烟 `release-smoke.sh` 与金丝雀门禁 `release-canary-gate.sh` 用本地假服务端验证：绿色放行、版本不符/5xx 超限/p95 超限/零流量均报错，`PROM_URL` 缺失时 fail closed。`check-release.sh` 扩 RELS-09～12（含 `--atomic` 计数校验，剔除注释行避免假通过）并证伪四条。文档回写：`RELEASE_STANDARDS` §5（流水线表 / 金丝雀细则 / 回滚）、`runbooks/release-rollback.md`（自动化处置 + 六步人工命令）、`release/CHECKLIST.md`。验证：`make check-release`、`make check-migration-gate`、`actionlint release.yml`（0 findings）、`make ci` 全绿 |
| 2026-11-05 | ENT-18 值班与事故管理 | 完成（tabletop；staging 实战挂账） | 分级：`SEVERITY_MATRIX.md` 以「用户影响 + 数据/安全后果」判级，P1 确认 ≤5 分钟 / 首报 ≤15 分钟 / 更新 30 分钟 / L2 15 分钟升级，明确「告警级别是初始级别、事故级别可升可降且须记录理由」，并把审计丢失、破玻璃授权、撞库批量锁定纳入 P1（与 `ALERTS.md` 现有 P1 对齐）。流程：`RESPONSE.md` 七阶段 + IC/OL/Comms/Scribe，时间线七个必备标记可算出检测/确认/缓解三段耗时，关闭六条件，P1 发布冻结与「回滚优先、迁移不回头」。复盘与台账：模板含三段耗时与 5 Whys，行动项禁止不可验证项；台账 6 条 open（pager 占位地址、值班入口、DLQ 重放进度、fail-closed 阈值、staging 实战、每月呼叫测试），ID 与字段由门禁校验。值班：`oncall/ROTATION.md` 周轮换主备（5 分钟接管）、五项交接清单、每月 `paging-probe` 自检、L1→L2→L3 阶梯；`runbooks/README.md` 去掉「待填」并指向事故入口。演练：`GAME_DAY.md` G1/G2/G3（+可选 G4 PITR），首次 tabletop 记录 `drills/2026-11-05-tabletop.md`，离线自检 3 场景 ×7 断言全 PASS（实测 1584/1631/1650 ms），**检测/确认/缓解耗时标注 N/A**（未注入真实故障，不伪造）。工具：`scripts/ops/paging_probe.py`（自实现路由树解析：matchers `= != =~ !~`、`continue`、首匹配下沉；占位地址检测；`--post` 演练投递）、`scripts/ops/game-day-drill.sh`（剧本依赖离线自检）、`scripts/ci/check-oncall.sh`（OC-01～08）。验证：`make check-oncall` 通过；9 条证伪（P1 时限放宽、P1 路由改 ticket、模板缺根因、场景引用不存在 runbook、README 待填、PR 模板缺事故自检、占位地址未跟踪、演练记录缺缓解用时、G2 命令改名）均报错；`make ci` 全绿 |
| 2026-11-05 | ENT-09 审核流（关闭最后一项验收缺口） | 完成 | 设计取舍：审批用 **append-only 事件表**（`tenant_approval`，迁移 000023）而不是单行状态列——驳回后可重提，单行会覆盖「上一轮为什么被驳回」；当前态由事件序列派生（`DeriveApproval`），`latest` 决定 draft/submitted/approved/rejected。开通前置：`pending → active` 在**事务内**先校验最新审批为 approved，再校验 ≥1 名在册 owner（顺序由 TN-04 锁定），两者互不替代。职责分离两道：权限层 `tenant:approve` 与 `tenant:write` 分开授予（迁移 000024 只授 system_admin），领域层拒绝「提交人审批自己的申请」并以 `outcome=failure` 留痕。API：`GET/POST /admin/v1/tenants/{id}/approval`、`.../approve`、`.../reject`（admin.yaml 8.2.0，TN-02 路由↔契约 22 条全对账）。审计：`tenant.approval.submit/approve/reject` 入 EVENTS.md（TN-01 19 条对账）。测试：domain 5 组（派生/提交人跟踪/前置/校验/动作语义）、app 4 组（开通门禁全链路、驳回重提留痕、重复提交与终态、404）、MySQL 集成 2 组（真实库落库与倒序、外键拒绝未知租户）；既有 14 处开通用例统一补 `approveForTest`，审计原子性用例改为「只让 activate 审计失败」以保留原意图。守卫兜底：`Options.Approvals` 未注入时用进程内实现，保证「未审批不能开通」不会因漏装配而失效。验证：`make check-tenant`（TN-01～04）、`go test ./internal/tenant/...`、MySQL 集成实测通过 |
| 2026-11-05 | ENT-19 供应链强化（SBOM + 签名 + 部署前验签） | 完成（keyless 首次真跑挂账） | 缺口定性：digest 只保证「内容不变」，不保证「来源可信」——有 registry 写权限即可推同 tag 新 digest，冒烟照样通过。设计：SBOM 与签名均以 **digest** 为单位（签 tag = TOCTOU），keyless（OIDC→Fulcio）不引入长期私钥；验签放在 `digests` 之后、首次 `helm upgrade` 之前，并把 `verify` 写进 `staging` 的 `needs`（不写就能被绕过）。交付：`image.yml`（SBOM/上传/签名/attest + `id-token: write`）、`release.yml`（verify job）、`scripts/ci/verify-supply-chain.sh`（拒绝 tag 引用与通配身份、固定 issuer、校验 SBOM 载荷）、`scripts/ci/check-sbom.sh`（SB-01～11，入 make ci 与 arch）、`scripts/ci/supply-chain-rehearsal.sh`、`docs/release/supply-chain.md`。验证：`make check-sbom` 全绿；13 条证伪（去 id-token / SBOM 改 table / SBOM 按 tag 生成 / 缺文件不报错 / 签 tag / attestation 无类型 / 无 predicate / trivy 用 master / 不验签 / staging 去掉 verify / 允许 tag 验签 / 允许通配身份 / verify 软失败）均报错；本地演练 11/11 PASS（含错误公钥、未签名 digest、tag 重指向三类反例）。**明确不做**：DAST（归 ENT-20，需公网或等保触发）；keyless 真跑与 SLSA provenance 挂账 |
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
| `internal/platform/httpserver/pipeline.go` | 变更 | ENT-02（幂等 / 分布式限流） |
| `internal/platform/config/config.go` | 变更 | 全部 |
| `internal/security/**` | 变更 | ENT-01（撤销检查、kid） |
| `internal/shared/auth/principal.go` | 变更 | ENT-12（scope） |
| `pkg/outbox/**` | 新增 | ENT-03 |
| `internal/platform/async_nats.go` | 新增 | ENT-03（T7 NATS JetStream 适配器） |
| `pkg/ratelimit/**` | 变更 | ENT-02 / ENT-14 |
| `pkg/testkit/**` | 新增 | ENT-16 |
| `cmd/worker/main.go` | 变更 | ENT-03 / ENT-08 / ENT-15 |
| `cmd/internal/iamruntime/runtime.go` | 变更 | ENT-02（NodeID / 发布器） |
| `migrations/000006_*` ～ `000012_*` | 新增 | 身份 / 审计 / 异步 / 租户 / 通知 / 开关 |
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
