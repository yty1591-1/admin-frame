# RBAC 权限管理服务（IAM）规划与设计

> **状态**：实施中（v0.16；ADR-0004 已接受；M1 已完成；M2 已完成；M3 已完成：A 端管理 API（15 个端点）、应用服务、审计接线、配置装配、OpenAPI、健康检查、指标与 runbook；M4.1 已完成：B 端自助 API（13 个端点，`/biz/v1/iam/*`）、`cmd/internal/iamruntime` 统一装配、商户角色配额与分页 total；M4.2 已完成：审批单聚合（SoD / 状态机 / 载荷校验）+ memory / MySQL 仓储 + 应用服务 + apply 接线 + A 端 6 个审批端点；M4.3 已完成：破玻璃（申请 / 审批 / 生效 / 撤销 + 快照 `*` 展开 + `iam_breakglass_active` + `rbac.breakglass`）；M4.4 已完成：事务性 Outbox（IAM Outbox 聚合 + memory / MySQL 仓储 + `OutboxRecorder` / `OutboxPublisher` + 退避重试 / 死信 + `iam_outbox_pending` / `iam_outbox_lag_seconds` / `iam_outbox_delivery_total` + prod enforce 强制启用；ENT-03 起存储已迁至平台 `async_outbox`）；M4.5 已完成：UnitOfWork（`pkg/db.WithinUnitOfWork` + `app.UnitOfWork`，业务写入与 outbox 同事务提交，嵌套复用外层事务）；M4.6 已完成：B 端审批单（审批单 `merchant_id` 商户维度与 B 端权限点已并入 000002 / 000003 模块基线、`ChangeScope` 租户隔离、B 端 6 个审批端点、跨商户 404）；M4.7 已完成：高风险直写强制转审批（`iam.approval.enabled`；开启后角色 / 绑定直写端点统一返回 `40900`，审批单 apply 走用例内部调用不受影响）；M5 进行中：可执行故障演练用例 + `BenchmarkReplaceRolePermissions_Diff` 登记 + 容量评估（staging shadow/enforce 与真实 SQL 压测待环境执行））
> **定位**：模块化单体中的 `internal/iam` 业务域，**不新增独立进程 / 微服务**
> **适用范围**：A 端平台角色、B 端商户内角色与权限管理；C 端不接入 IAM
> **决策记录**：[`docs/adr/0004-rbac-iam-domain.md`](./adr/0004-rbac-iam-domain.md)（落位、失败关闭、`50300`、灰度路线）
> **关联**：[`ARCHITECTURE.md`](./ARCHITECTURE.md) §五/§六/§七/§十/§十四、[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)、[`AUDIT_STANDARDS.md`](./AUDIT_STANDARDS.md)、[`DATA_STANDARDS.md`](./DATA_STANDARDS.md)、[`TESTING_STANDARDS.md`](./TESTING_STANDARDS.md)、[`API_STANDARDS.md`](./API_STANDARDS.md)、[`CONFIG_STANDARDS.md`](./CONFIG_STANDARDS.md)、[`RELIABILITY_STANDARDS.md`](./RELIABILITY_STANDARDS.md)、[`OBSERVABILITY_STANDARDS.md`](./OBSERVABILITY_STANDARDS.md)、[`CODE_STYLE.md`](./CODE_STYLE.md)、[`COMMENT_STANDARDS.md`](./COMMENT_STANDARDS.md)、[`CI_STANDARDS.md`](./CI_STANDARDS.md)、[`pkg-roadmap.md`](./pkg-roadmap.md) §3.3
> **最后更新**：2026-10-21
> **变更方式**：权限模型、存储模型、缓存一致性策略、超级管理员机制变更须提交 ADR（模板见 `ARCHITECTURE.md` 附录 C），经架构 / 安全 Owner 评审后实施；错误码新增按 API-04 只追加，并同步 `apperr` / `components.yaml` / 单测
> **目标读者**：后端工程师、架构 Reviewer、安全 Reviewer、SRE、A/B 端产品负责人

---

## 一、结论摘要（TL;DR）

本方案把「权限管理」建设为一个**有明确领域边界、可独立测试、可灰度替换现有演示权限表**的 IAM 域，而不是把角色表继续散落在 `cmd` 装配代码里。

| 编号 | 决策 | 说明 |
|------|------|------|
| D1 | **进程内领域模块** | 落在 `internal/iam`，由 `admin-api` / `biz-api` 按端挂载 transport；不新增可执行文件。满足架构「先模块化单体，后微服务」。 |
| D2 | **服务端权威** | JWT 只承载身份与渠道，**不承载权限点全集**；每次鉴权以 IAM 存储为准，避免「令牌未过期 = 权限未撤销」。 |
| D3 | **RBAC + 数据范围** | 功能权限（`resource:action`）+ 数据范围（`all` / `merchant` / `self` / `merchant_set`）分离建模；只做 RBAC 不足以满足 SEC-14。 |
| D4 | **默认拒绝、失败关闭** | 未认证 401、无权限 403、鉴权依赖不可用 503（稳定码 `50300`，已批准新增），**任何异常都不得放行**。 |
| D5 | **角色扁平、无继承（v1）** | 通过「一个主体绑定多个角色」表达组合，避免继承环、隐式提权与解释成本；继承 / 拒绝规则留待 ADR。 |
| D6 | **权限点目录代码所有、数据库只读同步** | 权限码是稳定契约，禁止在管理 API 中随意新建；目录由迁移种子入库，启动期校验，CI 校验唯一性与格式。 |
| D7 | **`pkg/rbac` 保持纯判定** | 不把数据库、缓存、租户逻辑塞进 `pkg/rbac`；动态权限通过 `security.Authorizer` 的 `SnapshotLoader` 端口注入。 |
| D8 | **两级缓存 + 有界陈旧** | L1 进程内 3s、L2 Redis 5m，写路径主动删除；权限撤销对外生效 ≤ 3s（L2 删除失败时告警，最坏 5m）。 |
| D9 | **所有变更强审计** | 角色、授权、绑定、破玻璃操作全部走 `audit.Emitter`；生产环境审计事件落 Outbox，写入失败即业务失败（M4.4 持久化 + 失败关闭；M4.5 起与业务写入同事务提交，不再存在「变更已提交、审计未入队」的崩溃窗口）。 |
| D10 | **先影子、后强制** | `iam.mode=off\|shadow\|enforce` 三态；先在 staging 影子比对，再切 `enforce`；回滚只需切回 `off`，不依赖数据库回滚。 |

**一句话**：以数据库为唯一真相，以「有效权限快照」为鉴权输入，以 `pkg/rbac` 做纯匹配，以审计、缓存、灰度与门禁保证生产可运营。

### 1.1 已评审决策（2026-10-21）

| 决策项 | 结论 | 落地状态 |
|--------|------|----------|
| 新增稳定错误码 `50300`（鉴权依赖不可用，HTTP 503） | **批准**，按 API-04 只追加 | 已落地：`apperr.CodeServiceUnavailable` + `Unavailable()`、`pkg/httpx` 503 映射、`components.yaml` 开放码表、单测；见 ADR-0004 |
| 按 `internal/iam` + `off/shadow/enforce` 路线进入 M0（ADR + 权限目录评审）与 M1（迁移 + 领域模型） | **批准** | M0 已完成设计输入（本文 + ADR-0004）；M1 已开工，权限目录与迁移已落地 |
| M1 权限目录一致性门禁 | **用 Go 测试**（`internal/iam/catalog`），不新增 shell 解析 | 已落地：`TestPermissionCatalog*` 6 个用例，覆盖格式 / 唯一性 / 常量 / 种子迁移 / 文档 / down |
| M2 快照与缓存 | 消费侧 `SnapshotLoader` + L1/L2 + shadow | 已落地：见 §6.3 / §8.2 / §14；覆盖率见 §13.1 |
| 迁移序号 `000002` / `000003` | **确认未被占用**（早期仅有 `000001_create_orders` 演示域） | 已落地：`000002_create_iam_tables` + `000003_seed_iam_permissions`；模块压平后编号保持，`000001` 为身份域、`000005` 为平台异步表 |
| 数据库引擎 | **MySQL 8.0.19+ 单基线** | 已落地：`DATETIME(3)` + 原生 `JSON` + 显式 `utf8mb4_0900_ai_ci` + `RESTRICT` 外键；不并行维护 PostgreSQL |

> 上述两项不再作为待确认项；其余仍开放的问题见 [§16.2](#162-开放问题owner-决策)。

---

## 二、现状与差距

### 2.1 已有能力

| 能力 | 当前位置 | 现状 |
|------|----------|------|
| 权限匹配原语 | `pkg/rbac` | 已完成：`Permission` / `Subject` / `Checker` / `Require` 中间件；默认拒绝、支持 `*` 与 `resource:*`、支持超级角色、并发安全 |
| HTTP 授权适配 | `internal/security.Authorizer` | 已完成：把 `rbac.Checker` 适配为 `auth.PermissionMiddleware`；401/403 映射 `apperr` |
| 认证主体 | `internal/shared/auth` | 已完成：`Principal`（channel / subject_id / merchant_id / roles / request_id） |
| 角色表装配 | `cmd/admin-api/register.go`、`cmd/biz-api/register.go` | **两端均已切 IAM**：`cmd/internal/iamruntime` 统一装配，A 端挂 `/admin/v1/iam/*`，B 端挂 `/biz/v1/iam/*`；静态表仅在 `iam.mode=off` 作为回滚兜底 |
| 审计动作 | `internal/shared/audit`、`docs/audit/EVENTS.md` | `rbac.role_change` / `rbac.binding_change` 已置 `active` 并接线（`app.SlogRecorder` + `observability.ChangeRecorder`） |
| 权限点常量 | `internal/iam/perm.go` | 统一维护，`resource:action` 或 `domain:resource:action` |

### 2.2 生产化差距

| 差距 | 风险 | 本方案对应章节 |
|------|------|----------------|
| 角色 / 权限无持久化，重启即回到代码常量 | 无法运营、无法审计、无法按商户定制 | §五 |
| 无权限管理 API | 所有变更靠发版，违反最小权限与职责分离 | §七 |
| 无数据范围模型 | B 端只能靠 Handler 手工判断，易漏判 | §四、§八 |
| 无权限快照缓存 | 每请求查库不可接受；直接缓存角色又易越权 | §六、§八 |
| 无撤销时效定义 | 离职 / 降权后旧令牌仍可能长期有效 | §八 |
| 无审计接线 | 不满足 AUD-05 / AUD-07 | §十 |
| 无权限目录治理 | 权限码拼写漂移、重复、通配符滥用 | §四、§十三 |
| 无灰度与回滚 | 一次切换失败会影响 A/B 全部鉴权 | §十五 |

---

## 三、目标与非目标

### 3.1 目标

| 编号 | 目标 | 可度量验收 |
|------|------|------------|
| G1 | 角色、权限点、绑定可持久化管理 | A/B 端 CRUD 全链路可用；重启后数据不丢 |
| G2 | 默认拒绝、最小权限 | 未授权请求 100% 拒绝；权限目录无隐式 `*` |
| G3 | 租户与端隔离 | B 端跨商户读写、A/B 跨端角色绑定全部被拒绝；有自动化用例 |
| G4 | 撤销可预期 | 降权 / 解绑后 ≤ 3s 生效（缓存正常）；有指标与告警 |
| G5 | 鉴权性能 | 缓存命中 P99 ≤ 5ms；回源 P99 ≤ 30ms；`/authorize` 不成为瓶颈 |
| G6 | 可审计 | 角色 / 授权 / 绑定 / 破玻璃 100% 产生审计事件；保留 ≥ 180 天 |
| G7 | 可灰度、可回滚 | `off → shadow → enforce` 三态；回滚 ≤ 1 次配置变更 |
| G8 | 符合工程门禁 | `make ci` 全绿；无新增高危依赖；覆盖率、注释、风格达标 |

### 3.2 非目标（明确不做）

- **不做完整 IdP**：登录、密码、MFA、SSO、会话管理仍由认证侧负责；IAM 只消费已认证的 `Principal`。
- **不做 ABAC 表达式引擎**：v1 不引入 Casbin / OPA / CEL 等策略引擎；时间、IP、资源属性等条件策略留待 ADR。
- **不做角色继承 / 拒绝规则**：v1 角色扁平、仅允许；避免「继承环 + 拒绝优先」导致的解释与缓存复杂性。
- **不做独立微服务 / 独立数据库**：与业务域共享库；`internal/iam` 只拥有自己的表。
- **不做组织架构 / 部门树**：v1 数据范围只到商户 / 本人 / 商户集合；部门树另立 ADR。
- **不做权限管理界面**：本文只定义后端契约；界面由独立工程负责。
- **不把权限点写进 JWT**：避免令牌膨胀与撤销延迟。

---

## 四、概念模型

### 4.1 术语

| 术语 | 含义 |
|------|------|
| Channel | 端别：`admin` / `biz` / `c`；IAM 只服务 `admin` / `biz` |
| Scope | 角色的作用域：`platform`（A 端全局）或 `merchant`（B 端单商户） |
| Subject | 权限主体：A 端员工（`staff`）或 B 端商户员工（`merchant_staff`） |
| Permission | 稳定权限点，形如 `iam:role:write`、`iam:role:read`；**代码即契约** |
| Role | 权限点的命名集合；同一角色可绑定多个主体 |
| Binding | 主体与角色的绑定关系；可带过期时间 |
| Data Scope | 角色对某类资源的数据可见范围；与功能权限正交 |
| Effective Permission | 某主体当前所有有效绑定展开后的权限点并集 |
| Snapshot | 某主体在某一时刻的角色、权限点、数据范围与版本快照 |
| Break-glass | 受控的临时超级权限，必须审批、限时、强审计 |
| SoD | Separation of Duties，职责分离；互斥权限不得由同一主体同时持有 |

### 4.2 模型决策

| 决策 | 选择 | 理由 / 约束 |
|------|------|-------------|
| 授权模型 | RBAC + 数据范围 | 功能权限与数据范围分离，避免「有读权限 = 能读全库」 |
| 角色继承 | v1 不支持 | 降低解释成本与提权风险；组合通过多绑定表达 |
| 拒绝规则 | v1 不支持 | `pkg/rbac` 为 allow-only；v1 用「不授予」表达拒绝 |
| 通配符 | 仅系统角色 / 破玻璃可用 | 管理 API 拒绝创建含 `*` 或 `resource:*` 的自定义角色 |
| 直接权限 | 不对外暴露 | 主体权限必须来自角色；`Subject.Permissions` 仅承载「已展开的有效权限」 |
| 超级角色 | 生产默认关闭 | `rbac.WithSuperRoles` 仅用于 local/demo；生产用限时破玻璃 |
| 角色删除 | 软删且 code 不可复用 | 避免历史审计无法解释、避免「删后重建」洗白 |
| 权限码变更 | 不可变，只弃用 | 已发布权限码改语义视为破坏性变更 |
| 数据范围冲突 | 取并集（最宽） | 多角色绑定视为显式授权；B 端禁止 `all` |
| 过期绑定 | 自动失效 | 查询条件过滤 `expires_at`；无需定时任务即可生效 |

### 4.3 权限点命名与目录

**语法**：

```text
permission := segment (":" segment){1,3}
segment    := [a-z][a-z0-9_]*
wildcard   := "*" | segment ":" "*"
```

- 推荐 `resource:action`（如 `iam:read`），子资源可用 `domain:resource:action`（如 `iam:role:write`）。
- 全小写、无空格、无连字符；段数 2～4。
- `*` 仅允许作为最后一段，且只允许系统角色 / 破玻璃持有。
- 权限码一经发布**不可重命名、不可改语义**；弃用通过 `status=deprecated`，至少保留一个 major。

**目录归属**：

| 位置 | 内容 |
|------|------|
| `internal/<domain>/perm.go` | 各业务域权限点常量（现有约定，保持） |
| `internal/iam/perm.go` | IAM 自身管理权限点常量 |
| `docs/iam/PERMISSIONS.md` | 全量目录（新增，供评审与运营查阅） |
| `migrations/000003_seed_iam_permissions.*.sql` | 目录种子（只读同步，追加式：后续 `*_seed_*.sql` 同约定） |
| `iam_permission` 表 | 运行时元数据（名称、类型、父节点、排序、状态） |

> 约束：新增权限点必须同 MR 更新常量、目录文档、种子迁移与 OpenAPI（若暴露）；CI 通过 Go 测试校验唯一性、格式、段数与目录一致性。

### 4.4 数据范围

| `scope_type` | 含义 | 适用端 | SQL 约束 |
|--------------|------|--------|----------|
| `all` | 全部数据 | A | 无租户条件（仍须权限点 + 审计） |
| `merchant` | 当前商户 | A/B | `merchant_id = :principal.merchant_id` |
| `self` | 本人资源 | A/B/C 语义 | `user_id = :principal.subject_id`（或等价归属列） |
| `merchant_set` | 指定商户集合 | A | `merchant_id IN (:ids)`，最多 100 个 |

规则：

1. 数据范围按 `(role_id, resource)` 存储；`resource` 为 `*` 或权限点的资源段（如 `order`）。
2. 有效范围 = 所有有效角色范围的**并集**；任一角色为 `all` 则结果为 `all`。
3. B 端角色只允许 `merchant` / `self`；授予 `all` 或 `merchant_set` 直接拒绝（40000）。
4. 标记为 `data_bearing` 的权限点，角色授权时必须至少配置一条数据范围，否则拒绝创建 / 更新。
5. 数据范围必须由 app + 仓储条件双重落实；**禁止**只查全量再在内存过滤（ARCHITECTURE §5.5、SEC-14）。
6. 数据范围不写入 JWT；每次鉴权从快照解析，并注入 `context` 供 app 读取。

### 4.5 职责分离（SoD）

- 配置化互斥组，例如：`iam:role:grant` 与 `audit:read` 不得由同一主体同时持有；`iam:breakglass:request` 与 `iam:breakglass:approve` 必须由不同主体持有。
- 授权 / 绑定时校验：若新增权限导致主体同时持有任一互斥组，返回 `40900`，并在消息中只指出冲突组名，不泄露其他主体信息。
- 破玻璃同样适用 SoD（申请人 ≠ 审批人），并记 `rbac.breakglass` 审计；不允许以「紧急」为由绕过双人审批。
- SoD 规则属于安全策略，放配置（`iam.security.sod_conflicts`），变更须安全 Owner Review。

---

## 五、数据模型

### 5.1 关系概览

```text
iam_role_template (id PK) ──► iam_role_template_permission
                     └─────► iam_role_template_data_scope
                                  │ 实例化（复制基线）
                                  ▼
iam_permission (code PK)          iam_role (id PK) ◄─ template_id / template_version
        ▲                         │        ▲
        │ permission_code         │        │ role_id
iam_role_permission ──────────────┘        │
                                           │
iam_role_data_scope ──(role_id, channel)───┘
iam_role_binding ──(role_id, channel, merchant_id)──► iam_role
                  │ expires_at / revoked_at / revoked_by（历史保留）
                  └── active_key 生成列：生效行=1，撤销行=id

iam_change_request（M4）  审批流，引用目标角色 / 绑定；pending_key 防重复提单
async_outbox（ENT-03）    审计与缓存失效事件（topic=iam.audit / iam.cache）
```

### 5.2 表设计

| 表 | 用途 | 关键约束 |
|----|------|----------|
| `iam_permission` | 权限点目录 | `code` 主键；`channel ∈ {admin,biz,both}`；`kind ∈ {menu,button,api}`；`status ∈ {active,deprecated}`；`parent_code` 可空 + 自引用外键（根节点 `NULL`，库层拒绝悬空节点） |
| `iam_role` | 角色 | 唯一 `(channel, merchant_id, code)`；`scope_type ∈ {platform,merchant}`；`role_type ∈ {system,custom,breakglass}`；`version` 乐观锁；`template_id` / `template_version` 记录模板来源与实例基线；`uk_iam_role_id_channel`、`uk_iam_role_id_scope` 供子表复合外键引用 |
| `iam_role_template` | 角色模板 | 唯一 `(channel, code)`；`status ∈ {active,disabled}`；`version` 递增以识别实例漂移；软删保留 `code` |
| `iam_role_template_permission` | 模板 → 权限点基线 | 主键 `(template_id, permission_code)`；外键到模板与权限点（`RESTRICT`） |
| `iam_role_template_data_scope` | 模板数据范围基线 | 主键 `(template_id, resource)`；`scope_type` / `scope_values` CHECK 与角色侧一致 |
| `iam_role_permission` | 角色 → 权限点 | 主键 `(role_id, permission_code)`；授权人、时间 |
| `iam_role_binding` | 主体 → 角色 | 唯一 `(channel, subject_type, subject_id, merchant_id, role_id, active_key)`：`active_key` 生成列保证「同一主体同一角色至多一条生效绑定」；`revoked_at/revoked_by` 软撤销保留历史；复合外键 `(role_id, channel, merchant_id)` 阻止跨端 / 跨商户绑定；`expires_at` 可空 |
| `iam_role_data_scope` | 角色数据范围 | 主键 `(role_id, resource)`；冗余 `channel` + 复合外键 `(role_id, channel)`；CHECK 强制 B 端仅 `merchant` / `self`；`scope_values` 为原生 JSON 类型 |
| `iam_change_request` | 审批单（M4） | 状态机 `pending → approved/rejected → applied`（另有 `cancelled` / `expired`）；申请人与审批人不同；`merchant_id` 区分平台级 / 商户级（M4.6）；`pending_key` 生成列唯一键阻止同一目标并发重复提单；`expires_at` 时效、`decision_reason` 决策说明、`applied_by` 执行人、`payload_version` 载荷版本 |
| `async_outbox`（ENT-03） | 审计 / 缓存失效事件 | 业务请求内与业务写入**同事务**写入（失败即失败关闭）；`pkg/outbox` Relay 投递；状态、重试次数与死信 |

> **唯一真相**：库级约束以 `migrations/000002_create_iam_tables.up.sql` 为准；本节只做语义摘要，两者冲突时先修迁移再同步文档。企业级加固（2026-10-26）的完整说明见 §5.3.9。

### 5.3 DDL（MySQL 基线）

> **迁移文件**：`migrations/000002_create_iam_tables.up.sql` / `.down.sql`（含审批单 `merchant_id` 商户维度与 `idx_iam_change_merchant_status`）；种子：`000003_seed_iam_permissions.up.sql` / `.down.sql`（A 端目录 + B 端审批权限点 + 演示域 `deprecated` 状态）、`000004_seed_breakglass_role.up.sql` / `.down.sql`。平台异步表见 `000005_create_async_outbox.up.sql` / `.down.sql`。命名遵循 DATA-01，up / down 必须配对；已应用的迁移文件不得重写（checksum 漂移会被迁移框架拒绝），新增权限点写入新的 `*_seed_*.up.sql`。
>
> **M1 前置决策（2026-10-21 已确认）**：
>
> 1. **数据库基线 = MySQL 8.0.19+**（InnoDB / `utf8mb4_0900_ai_ci`）。仓库现有迁移、驱动与运维脚本均为 MySQL，本设计只维护 MySQL 一套 DDL；不并行维护 PostgreSQL 版本（§5.3.8 仅作未来演进参考，不纳入门禁）。
> 2. **迁移序号已确认未被占用**：`000001` 为现有 `create_orders`，`000002`（IAM 表）与 `000003`（权限种子）可用；`check-migrations.sh` 已通过 up/down 配对校验。
> 3. **类型决策**：时间统一 `DATETIME(3)`，不使用 `TIMESTAMP`（避免 2038 上限与隐式时区转换）；JSON 使用 MySQL 原生 `JSON` 类型（数据库层校验合法性，空对象写 `{}` 而非空串）；字符集 / 排序规则显式声明，避免环境默认值差异。
> 4. **外键仅用于防悬空与防误删**：`iam_role_permission`、`iam_role_binding`、`iam_role_data_scope` 对 `iam_role` / `iam_permission` 建 `RESTRICT` 外键；业务删除仍以软删为主，种子 down 被引用时因此**显式失败**（DATA-02）。
>
> **权限目录一致性门禁**：`internal/iam/catalog` 的 Go 测试强制 `catalog.go` / `perm.go` / 全部 `migrations/*_seed_*.up.sql` / `docs/iam/PERMISSIONS.md` 一致（§13.5），不新增 shell 解析脚本。权限点只追加：新增权限点写入新的 `*_seed_*.up.sql` 与对应 down，禁止改写已应用的种子文件（否则迁移历史表报 checksum 漂移）。

**注释约束（与 DATA-01 / COMMENT_STANDARDS C-08 对齐）**：

1. **每张表必须有表注释，每个字段必须有字段注释**；禁止出现无注释的新增列（Review 阻断）。
2. 注释写**语义与边界**（枚举取值、单位、空值含义、安全约束），禁止复述列名。
3. 枚举类字段在注释中列全取值，并与 `domain` 常量保持一致（C-10）。
4. 注释禁止包含密钥、生产 DSN、真实 Token、完整 PII（SEC-05）。
5. 索引也写 `COMMENT`，说明它服务的查询；唯一索引须注明「幂等 / 防重」用途。
6. MySQL 用列内 `COMMENT '...'` + 表尾 `COMMENT='...'`；PostgreSQL 用独立的 `COMMENT ON TABLE/COLUMN`（见 §5.3.8）。同一迁移不得两种写法混用。
7. 注释变更视为 schema 变更，须走迁移，禁止只改文档。

### 5.3.1 iam_permission（权限点目录）

```sql
CREATE TABLE IF NOT EXISTS iam_permission (
    code             VARCHAR(128)  NOT NULL                  COMMENT '权限点稳定标识，resource:action 或 domain:resource:action，全小写；发布后不可改名或复用',
    name             VARCHAR(128)  NOT NULL                  COMMENT '权限点显示名，用于菜单 / 按钮 / 授权勾选',
    channel          VARCHAR(16)   NOT NULL                  COMMENT '可授予端别：admin=仅A端 / biz=仅B端 / both=两端均可',
    resource         VARCHAR(64)   NOT NULL                  COMMENT '资源段，取权限码首段，如 order；用于数据范围匹配',
    action           VARCHAR(64)   NOT NULL                  COMMENT '动作段，取权限码末段，如 read / write / grant',
    kind             VARCHAR(16)   NOT NULL                  COMMENT '权限点形态：menu=菜单 / button=按钮 / api=接口',
    data_bearing     TINYINT       NOT NULL DEFAULT 0        COMMENT '是否涉及数据范围：1=是（授权时必须配置 iam_role_data_scope）/ 0=否',
    parent_code      VARCHAR(128)  NULL     DEFAULT NULL     COMMENT '父节点权限码，根节点为 NULL；用于菜单 / 按钮树',
    path             VARCHAR(256)  NOT NULL DEFAULT ''       COMMENT '树路径，逗号分隔的祖先编码，用于子树查询与排序；根节点为空串',
    sort             INT           NOT NULL DEFAULT 0        COMMENT '同级排序值，升序，值越小越靠前',
    status           VARCHAR(16)   NOT NULL DEFAULT 'active' COMMENT '状态：active=可用 / deprecated=已弃用（保留授权但不可新授）',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '创建时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)   NOT NULL                  COMMENT '最后更新时间（UTC，毫秒精度），由应用写入',
    PRIMARY KEY (code),
    KEY idx_iam_permission_channel_status (channel, status, sort) COMMENT '菜单树按端别 + 状态过滤并按同级排序输出',
    CONSTRAINT fk_iam_permission_parent FOREIGN KEY (parent_code) REFERENCES iam_permission (code)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_permission_channel CHECK (channel IN ('admin', 'biz', 'both')),
    CONSTRAINT ck_iam_permission_kind CHECK (kind IN ('menu', 'button', 'api')),
    CONSTRAINT ck_iam_permission_status CHECK (status IN ('active', 'deprecated')),
    CONSTRAINT ck_iam_permission_data_bearing CHECK (data_bearing IN (0, 1)),
    CONSTRAINT ck_iam_permission_parent_self CHECK (parent_code IS NULL OR parent_code <> code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 权限点目录，代码所有、库内只读同步；不存放角色授权关系';
```

### 5.3.2 iam_role（角色）

```sql
CREATE TABLE IF NOT EXISTS iam_role (
    id               BIGINT        NOT NULL                  COMMENT '角色主键，雪花 ID（pkg/id 生成），禁止自增',
    channel          VARCHAR(16)   NOT NULL                  COMMENT '角色所属端别：admin=A端平台 / biz=B端商户',
    scope_type       VARCHAR(16)   NOT NULL                  COMMENT '角色作用域：platform=A端全局 / merchant=B端单商户',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT 'B端所属商户ID；scope_type=platform 时为空串，禁止用空串代表“全部商户”',
    code             VARCHAR(64)   NOT NULL                  COMMENT '角色编码，端内（同商户）唯一；软删后不可复用，防止重建洗白',
    name             VARCHAR(128)  NOT NULL                  COMMENT '角色显示名',
    description      VARCHAR(512)  NOT NULL DEFAULT ''       COMMENT '角色说明，建议写清职责边界',
    role_type        VARCHAR(16)   NOT NULL DEFAULT 'custom' COMMENT '角色类型：system=系统内置 / custom=自定义 / breakglass=破玻璃，仅此类型可在审批有效期内展开 *',
    status           VARCHAR(16)   NOT NULL DEFAULT 'active' COMMENT '状态：active=生效 / disabled=停用（停用后快照不再加载其权限）',
    template_id      BIGINT        NOT NULL DEFAULT 0        COMMENT '来源角色模板 ID；0=非模板实例（手工创建或系统种子）',
    template_version BIGINT        NOT NULL DEFAULT 0        COMMENT '实例化时模板版本；与模板当前 version 不一致表示存在漂移，供同步任务比对',
    version          BIGINT        NOT NULL DEFAULT 1        COMMENT '乐观锁版本；更新须 WHERE version=?，冲突返回 40900',
    created_by       VARCHAR(64)   NOT NULL                  COMMENT '创建人 subject_id',
    updated_by       VARCHAR(64)   NOT NULL                  COMMENT '最后更新人 subject_id',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '创建时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)   NOT NULL                  COMMENT '最后更新时间（UTC，毫秒精度）',
    deleted_at       DATETIME(3)   NULL                      COMMENT '软删时间（UTC）；NULL=未删除，非空后 code 不可复用',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_role_scope_code (channel, merchant_id, code) COMMENT '端内角色编码唯一，绑定与授权的幂等防线',
    UNIQUE KEY uk_iam_role_id_channel (id, channel) COMMENT '供数据范围 / 绑定复合外键引用，库层防止跨端引用',
    UNIQUE KEY uk_iam_role_id_scope (id, channel, merchant_id) COMMENT '供绑定复合外键引用，库层防止跨商户绑定',
    KEY idx_iam_role_merchant_status (merchant_id, status) COMMENT 'B端按商户 + 状态列角色',
    KEY idx_iam_role_channel_scope (channel, scope_type, status) COMMENT 'A端按端别 + 作用域列角色',
    KEY idx_iam_role_template (template_id, template_version) COMMENT '模板漂移扫描：按模板聚合实例并比对基线版本',
    CONSTRAINT ck_iam_role_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_role_scope_type CHECK (scope_type IN ('platform', 'merchant')),
    CONSTRAINT ck_iam_role_tenant CHECK (
        (scope_type = 'platform' AND channel = 'admin' AND merchant_id = '')
     OR (scope_type = 'merchant' AND channel = 'biz' AND merchant_id <> '')
    ),
    CONSTRAINT ck_iam_role_type CHECK (role_type IN ('system', 'custom', 'breakglass')),
    CONSTRAINT ck_iam_role_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT ck_iam_role_version CHECK (version >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 角色定义，按端别与商户作用域隔离；角色扁平、无继承';
```

> 角色模板三表（`iam_role_template` / `iam_role_template_permission` / `iam_role_template_data_scope`）的完整 DDL 见 `migrations/000002_create_iam_tables.up.sql`；语义与实例化规则见 §5.3.9。

### 5.3.3 iam_role_permission（角色 → 权限点）

```sql
CREATE TABLE IF NOT EXISTS iam_role_permission (
    role_id          BIGINT        NOT NULL COMMENT '角色ID，关联 iam_role.id',
    permission_code  VARCHAR(128)  NOT NULL COMMENT '权限点编码，关联 iam_permission.code；仅 active 状态可授予',
    granted_by       VARCHAR(64)   NOT NULL COMMENT '最近一次授予人 subject_id',
    created_at       DATETIME(3)   NOT NULL COMMENT '授予时间（UTC，毫秒精度）',
    PRIMARY KEY (role_id, permission_code),
    KEY idx_iam_rp_permission (permission_code) COMMENT '反查某权限点被哪些角色引用，用于影响面与弃用评估',
    CONSTRAINT fk_iam_rp_role FOREIGN KEY (role_id) REFERENCES iam_role (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_iam_rp_permission FOREIGN KEY (permission_code) REFERENCES iam_permission (code)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='角色与权限点的授予关系；一行代表一次有效授予，删除即撤权';
```

### 5.3.4 iam_role_binding（主体 → 角色）

```sql
CREATE TABLE IF NOT EXISTS iam_role_binding (
    id               BIGINT        NOT NULL            COMMENT '绑定主键，雪花 ID',
    channel          VARCHAR(16)   NOT NULL            COMMENT '端别：admin / biz；须与角色 channel 一致',
    subject_type     VARCHAR(32)   NOT NULL            COMMENT '主体类型：staff=A端员工 / merchant_staff=B端商户员工',
    subject_id       VARCHAR(64)   NOT NULL            COMMENT '主体ID；A端为员工ID，B端为商户员工ID',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT '' COMMENT 'B端所属商户ID（隔离维度）；A端为空串；禁止以空串表示不限商户',
    role_id          BIGINT        NOT NULL            COMMENT '角色ID，关联 iam_role.id；角色须与绑定端别、商户一致',
    expires_at       DATETIME(3)   NULL                COMMENT '绑定过期时间（UTC）；NULL=长期有效；查询须过滤已过期记录',
    granted_by       VARCHAR(64)   NOT NULL            COMMENT '授权人 subject_id，用于审计与职责分离校验',
    created_at       DATETIME(3)   NOT NULL            COMMENT '绑定时间（UTC，毫秒精度）',
    revoked_at       DATETIME(3)   NULL                COMMENT '解绑时间（UTC）；NULL=生效；非空后该行仅用于历史与访问复核',
    revoked_by       VARCHAR(64)   NOT NULL DEFAULT '' COMMENT '解绑操作人 subject_id；未解绑时为空串',
    revoke_reason    VARCHAR(512)  NOT NULL DEFAULT '' COMMENT '解绑原因；强制填写策略由应用配置决定',
    active_key       BIGINT        GENERATED ALWAYS AS (CASE WHEN revoked_at IS NULL THEN 1 ELSE id END) STORED
                                                   COMMENT '生效绑定的唯一哨兵：生效行恒为 1，撤销行取自身 id，使同一主体同一角色至多一条生效绑定且保留全部历史',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_binding (channel, subject_type, subject_id, merchant_id, role_id, active_key)
        COMMENT '同一主体对同一角色的生效绑定幂等；撤销后重新授权会新增一行而不是复活旧行',
    KEY idx_iam_binding_subject (channel, subject_type, subject_id, merchant_id, revoked_at)
        COMMENT '快照回源主查询路径：按主体取全部生效绑定',
    KEY idx_iam_binding_expiry (expires_at)
        COMMENT '过期绑定清理扫描：避免全表扫描',
    CONSTRAINT fk_iam_binding_role_scope FOREIGN KEY (role_id, channel, merchant_id)
        REFERENCES iam_role (id, channel, merchant_id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_binding_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_binding_subject_type CHECK (subject_type IN ('staff', 'merchant_staff')),
    CONSTRAINT ck_iam_binding_tenant CHECK (
        (channel = 'admin' AND subject_type = 'staff' AND merchant_id = '')
     OR (channel = 'biz' AND subject_type = 'merchant_staff' AND merchant_id <> '')
    ),
    CONSTRAINT ck_iam_binding_revoke CHECK (
        (revoked_at IS NULL AND revoked_by = '')
     OR (revoked_at IS NOT NULL AND revoked_by <> '')
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 主体-角色绑定，支持限时有效与软撤销；鉴权快照的唯一绑定来源';
```

> **撤销语义**：解绑不再物理删除，而是写 `revoked_at` / `revoked_by` / `revoke_reason`（`revoke()`）；仓储解绑幂等（已撤销再撤销返回成功）。`active_key` 生成列让唯一键只约束「生效绑定」，因此同一主体可以保留多条历史行、且撤销后重新授权必然新增一行并重新生效。快照 SQL 必须带 `b.revoked_at IS NULL`；`ListByRole` 保留历史用于审计，角色删除引用检查只看 `IsEffective`。

### 5.3.5 iam_role_data_scope（角色数据范围）

```sql
CREATE TABLE IF NOT EXISTS iam_role_data_scope (
    role_id          BIGINT        NOT NULL COMMENT '角色ID，关联 iam_role.id',
    channel          VARCHAR(16)   NOT NULL COMMENT '角色端别冗余列；由复合外键锁定与 iam_role 一致，用于库层拒绝 B 端越权范围',
    resource         VARCHAR(64)   NOT NULL COMMENT '资源段：* 表示全部资源，或权限点首段如 order / file',
    scope_type       VARCHAR(16)   NOT NULL COMMENT '范围类型：all=全部 / merchant=当前商户 / self=本人 / merchant_set=指定商户集合；B端仅允许 merchant、self',
    scope_values     JSON          NOT NULL COMMENT '范围取值：merchant_set 存 {"merchant_ids":[...]}（≤100）；其他类型存 {}',
    granted_by       VARCHAR(64)   NOT NULL COMMENT '最近一次配置人 subject_id',
    created_at       DATETIME(3)   NOT NULL COMMENT '首次配置时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)   NOT NULL COMMENT '最后更新时间（UTC，毫秒精度）',
    PRIMARY KEY (role_id, resource),
    KEY idx_iam_rds_scope_type (scope_type) COMMENT '按范围类型审计与越权排查',
    CONSTRAINT fk_iam_rds_role_channel FOREIGN KEY (role_id, channel) REFERENCES iam_role (id, channel)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_rds_scope_type CHECK (scope_type IN ('all', 'merchant', 'self', 'merchant_set')),
    CONSTRAINT ck_iam_rds_channel_scope CHECK (channel = 'admin' OR scope_type IN ('merchant', 'self')),
    CONSTRAINT ck_iam_rds_scope_values CHECK (
        (scope_type = 'merchant_set'
            AND JSON_LENGTH(scope_values, '$.merchant_ids') BETWEEN 1 AND 100)
     OR (scope_type <> 'merchant_set' AND JSON_LENGTH(scope_values) = 0)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='角色数据范围，与功能权限正交；授予 data_bearing 权限时必填';
```

### 5.3.6 iam_change_request（审批单，M4）

```sql
CREATE TABLE IF NOT EXISTS iam_change_request (
    id               BIGINT        NOT NULL                   COMMENT '审批单主键，雪花 ID',
    channel          VARCHAR(16)   NOT NULL                   COMMENT '发起端别：admin / biz',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '商户ID；空串表示平台级（A 端），非空表示商户级（B 端）',
    action           VARCHAR(64)   NOT NULL                   COMMENT '申请动作：role_create / role_permission_replace / binding_create / breakglass 等',
    target_type      VARCHAR(32)   NOT NULL                   COMMENT '目标类型：iam_role / iam_binding / iam_breakglass',
    target_id        VARCHAR(64)   NOT NULL                   COMMENT '目标ID；新建类动作在应用前存业务临时ID或空串',
    payload          JSON          NOT NULL                   COMMENT '申请载荷；应用前须重新校验，不得直接信任',
    payload_version  INT           NOT NULL DEFAULT 1         COMMENT '载荷结构版本；载荷语义变更时递增，供审批人识别与回溯',
    status           VARCHAR(16)   NOT NULL DEFAULT 'pending' COMMENT '状态机：pending=待审批 / approved=已通过 / rejected=已拒绝 / applied=已应用 / cancelled=已撤销 / expired=已超时',
    reason           VARCHAR(512)  NOT NULL DEFAULT ''        COMMENT '申请原因；iam.security.require_reason=true 时必填',
    requested_by     VARCHAR(64)   NOT NULL                   COMMENT '申请人 subject_id',
    requested_at     DATETIME(3)   NOT NULL                   COMMENT '申请时间（UTC，毫秒精度）',
    expires_at       DATETIME(3)   NULL                       COMMENT '审批时效截止（UTC）；NULL=按配置计算，超时后不得再通过',
    approved_by      VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '审批人 subject_id，必须与申请人不同（职责分离）',
    approved_at      DATETIME(3)   NULL                       COMMENT '审批时间（UTC）',
    decision_reason  VARCHAR(512)  NOT NULL DEFAULT ''        COMMENT '审批结论说明；拒绝时必填，通过时可选',
    applied_by       VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '执行应用的操作人 subject_id；系统执行时写 system',
    applied_at       DATETIME(3)   NULL                       COMMENT '实际应用时间（UTC）',
    version          BIGINT        NOT NULL DEFAULT 1         COMMENT '乐观锁版本，防并发重复审批',
    pending_key      VARCHAR(255)  GENERATED ALWAYS AS (
        CASE WHEN status = 'pending' AND target_id <> ''
             THEN CONCAT(channel, '|', merchant_id, '|', action, '|', target_id)
        END
    ) STORED COMMENT '待审批幂等键：同一端别 / 商户 / 目标至多一张待审批单，终态行取 NULL 不参与唯一约束',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_change_pending (pending_key)
        COMMENT '阻止并发重复提单；新建类动作 target_id 为空时不参与（应用层按载荷幂等）',
    KEY idx_iam_change_status (channel, status, requested_at) COMMENT '待办列表与超时审批扫描',
    KEY idx_iam_change_expiry (status, expires_at) COMMENT '超时审批扫描：仅扫描 pending 且已过期的单据',
    KEY idx_iam_change_merchant_status (merchant_id, channel, status, requested_at) COMMENT 'B 端待办列表：按商户 + 端别 + 状态 + 申请时间扫描',
    CONSTRAINT ck_iam_change_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_change_status CHECK (status IN ('pending', 'approved', 'rejected', 'applied', 'cancelled', 'expired')),
    CONSTRAINT ck_iam_change_tenant CHECK (
        (channel = 'admin' AND merchant_id = '')
     OR (channel = 'biz' AND merchant_id <> '')
    ),
    CONSTRAINT ck_iam_change_version CHECK (version >= 1),
    CONSTRAINT ck_iam_change_payload CHECK (JSON_TYPE(payload) = 'OBJECT')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 高风险变更审批单；申请人与审批人强制不同，审批通过后才落到目标表';
```

> **时效与幂等**：`expires_at` 在提单时按配置计算（默认 24h，破玻璃按自身时效），过期未决只能标记 `expired`、不得再 `approve`；`pending_key` 生成列只对 `status='pending' AND target_id<>''` 生效，同一端别 / 商户 / 目标至多一张待审批单，终态后自动释放键值，新建类动作由应用层按载荷幂等。`decision_reason` 在拒绝时必填；`applied_by` 记录实际执行人（审批人自查或系统任务写 `system`），与 `approved_by` 区分，避免「谁批的」与「谁执行的」混淆。

> **实现状态（M4.2 应用层 / 持久化 / 端点已落地；M4.6 补齐 B 端租户隔离）**：`domain.ChangeRequest`（状态机 `pending → approved → applied` / `rejected` / `cancelled`、SoD、载荷 JSON 对象校验、敏感字段与 B 端 `merchant_id` 覆盖拒绝、端别 / 商户不变量）、`MemoryChangeRequestRepository` / `SQLChangeRequestRepository`（乐观锁、过滤、分页、`merchant_id` 过滤与不可变）、`app.ChangeRequestService`（创建 / 列表 / 详情 / 通过 / 拒绝 / 撤销 / 应用，全部接收 `ChangeScope`）与 `app.RoleChangeApplicator`（应用时重新加载目标聚合并按当前版本写入，跨端 / 跨商户一律 404）已落地。
>
> A 端端点：`GET|POST /admin/v1/iam/change-requests`、`POST /admin/v1/iam/change-requests/{id}/{approve|reject|cancel|apply}`，作用域恒为 `{channel: admin, merchantID: ""}`，只看到平台级审批单。
>
> B 端端点：`GET|POST /biz/v1/iam/change-requests`、`POST /biz/v1/iam/change-requests/{id}/{approve|reject|cancel|apply}`；`merchant_id` 只来自 `Principal`，列表与决定类操作都强制按商户过滤，跨商户统一 `40400`（SEC-14）。权限点为 `iam:change:request`（申请 + 查看待办）与 `iam:change:review`（审批），见 `docs/iam/PERMISSIONS.md`。
>
> **应用语义**：`apply` 失败时审批单保持 `approved`（可重试），不会标记 `applied`；新建类动作（`role_create` / `role_clone` / `binding_create`）在应用成功后把真实主键回填到 `target_id`。
>
> **强制转审批（M4.7 已落地）**：`iam.approval.enabled=true` 时，角色创建 / 更新 / 删除 / 克隆 / 权限替换与绑定 / 解绑共 7 个直写端点在 transport 层统一返回 `40900`，消息给出对应的 `action` 与 `/change-requests` 路径；调用方须先提交审批单。审批单 `apply` 走应用服务内部调用，不经过 HTTP，因此不受该开关影响。开关默认关闭，存量调用方行为不变；开启时 A/B 端均可使用审批流，C 端与 worker 因无 IAM transport 会在配置校验阶段被拒绝。
>
> **租户隔离（迁移 000002 内置）**：`merchant_id NOT NULL DEFAULT ''` + `idx_iam_change_merchant_status`；A 端单据 `merchant_id` 恒为空、B 端恒非空，由 `domain.validateChangeTenant` 在创建时强制。B 端申请的单据执行时目标角色 / 绑定必须同商户（`loadRoleForChange` / `UnbindCommand.MerchantID`），否则返回 `40400`；B 端载荷出现 `merchant_id` 返回 `40300`、`scope_type=platform` 在申请阶段即返回 `40000`（不把越权意图带上审批台）。破玻璃仍为 A 端专属：B 端创建 `breakglass_request` 与 B 端路由均直接拒绝。
>
> **权限点分层**：B 端不复用 A 端的 `iam:change:read` / `iam:change:approve`，而是新增 `iam:change:request` / `iam:change:review`（随 `000003` 目录种子写入）。即使商户过滤逻辑出现缺陷，持有 B 端权限点的主体也没有平台级审批单的权限。

### 5.3.7 异步 Outbox（平台 async_outbox）

ENT-03 T8 起 IAM 不再维护自有 `iam_outbox`：审计与缓存失效事件统一写入平台表 `async_outbox`（表结构见 `migrations/000005_create_async_outbox.up.sql`）。模块基线不再创建 `iam_outbox`，因此无需历史数据迁移。

> **T8 实现状态**：
> - 写入：`app.OutboxRecorder` 通过 `pkg/outbox.Store.Append` 写入 `async_outbox`（topic=`iam.audit` / `iam.cache`，event_type 沿用 `audit.event` / `cache.invalidate`），复用 `pkg/db.TxFromContext` 与业务写入同事务；写入失败返回 `50000`（fail-closed，不静默丢审计）。
> - 投递：`cmd/worker` 启动平台 `AsyncRuntime`；`iamruntime.RegisterAsync` 为两个 topic 注册 `app.AuditEventHandler`（审计载荷不可解析 / 未知事件类型 → `retry.Permanent` 进 `async_dlq`）。
> - 重试与死信：由 `pkg/outbox.Relay` 统一负责（指数退避 + 租约回收 + 死信重放），语义至少一次；审计出口按 `event_id` 去重。
> - 指标：`iam_outbox_delivery_total` 由 `app.AuditTopicObserver` 映射；`iam_outbox_pending` / `iam_outbox_lag_seconds` 由 `TopicStats(topic=iam.audit)` 提供，名称保持不变。
> - 装配：`iamruntime.New` 在 `deps.Async != nil` 时切换审计出口；`async.enabled=false` 时回落 `SlogRecorder`（仅 local / demo）。
>
> **M4.5 实现状态（事务边界）**：
> - 底层：`pkg/db.WithinUnitOfWork(ctx, *sql.DB, opts, fn)` 开启事务后把 `*sql.Tx` 注入 context（`db.WithTx` / `db.TxFromContext`）；**已在事务中则直接复用**，不会开启嵌套事务（MySQL 无真正的嵌套事务，二次 `Begin` 会各自提交并破坏原子性）。
> - 端口：`app.UnitOfWork.WithinTx(ctx, fn)`；MySQL 实现 `persistence.SQLUnitOfWork`，内存实现 `app.NoopUnitOfWork`（显式声明「无原子性」，避免误以为已受保护）。
> - 仓储：`resolveExecutor(ctx, *sql.DB)` 统一解析执行器，IAM 各仓储的全部读写都经它；`pkg/outbox.MySQLStore.Append` 同样通过 `db.TxFromContext` 复用事务——事务对仓储透明，调用链无需层层传 `*sql.Tx`。
> - 用例：`CreateRole` / `UpdateRole` / `ReplacePermissions` / `CloneRole` / `DeleteRole`、`Bind` / `Unbind`、审批 `Apply`、破玻璃 `Request` / `Approve` / `Revoke` 均把「业务写入 + 审计」包进同一个 `WithinTx`；审批应用内部再调用角色 / 绑定用例时命中复用分支，全程只有一个事务（`TestBreakglassApprovalReusesOuterTransaction` 断言 `maxDepth=1`）。
> - 装配：`cmd/internal/iamruntime` 在 `db.driver≠memory` 时注入 `SQLUnitOfWork`，否则注入 `NoopUnitOfWork`；无需新增配置项（原子性是正确性要求，不提供关闭开关）。
> - 验证：`pkg/db` 单测覆盖提交 / 回滚 / 复用 / 入参 fail-closed；`tests/integration/iam/mysql_uow_test.go` 用真实 MySQL 断言「outbox 唯一键冲突 ⇒ 角色主表与权限明细一并回滚」与「嵌套写入随外层回滚」。
> - 语义边界（不可混淆）：事务保证的是**入队原子性**；投递仍是至少一次，审计出口必须按 `event_id` 幂等去重。业务回滚时审计事件一并回滚，因此不会出现「审计记录了未发生的变更」。


### 5.3.8 PostgreSQL 写法（非基线，仅演进参考）

> **当前不维护**：M1 基线为 MySQL 8.0.19+（§5.3），本节只用于记录「若未来切换引擎」的差异点，**不纳入 CI 门禁、不要求与 MySQL 迁移同步修改**。切换引擎必须先提交 ADR，并按 `migrations/mysql/`、`migrations/postgres/` 分目录，禁止在同一目录混放两种方言。

PostgreSQL 不支持列内 `COMMENT`，必须用独立语句；表与索引同理。示例（与 §5.3.2 同一张表）：

```sql
CREATE TABLE IF NOT EXISTS iam_role (
    id          BIGINT       PRIMARY KEY,
    channel     VARCHAR(16)  NOT NULL,
    merchant_id VARCHAR(64)  NOT NULL DEFAULT '',
    code        VARCHAR(64)  NOT NULL,
    role_type   VARCHAR(16)  NOT NULL DEFAULT 'custom',
    status      VARCHAR(16)  NOT NULL DEFAULT 'active',
    version     BIGINT       NOT NULL DEFAULT 1,
    -- 其余列（scope_type / name / description / created_by / updated_by）见 §5.3.2，此处仅示意 COMMENT 写法
    created_at  TIMESTAMPTZ  NOT NULL,
    updated_at  TIMESTAMPTZ  NOT NULL,
    deleted_at  TIMESTAMPTZ  NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_iam_role_scope_code
    ON iam_role (channel, merchant_id, code);

COMMENT ON TABLE  iam_role                         IS 'IAM 角色定义，按端别与商户作用域隔离；角色扁平、无继承';
COMMENT ON COLUMN iam_role.id                      IS '角色主键，雪花 ID（pkg/id 生成），禁止自增';
COMMENT ON COLUMN iam_role.channel                 IS '角色所属端别：admin=A端平台 / biz=B端商户';
COMMENT ON COLUMN iam_role.merchant_id             IS 'B端所属商户ID；scope_type=platform 时为空串，禁止用空串代表“全部商户”';
COMMENT ON COLUMN iam_role.code                    IS '角色编码，端内（同商户）唯一；软删后不可复用，防止重建洗白';
COMMENT ON COLUMN iam_role.role_type               IS '角色类型：system=系统内置 / custom=自定义 / breakglass=破玻璃，仅此类型可在审批有效期内展开 *';
COMMENT ON COLUMN iam_role.status                  IS '状态：active=生效 / disabled=停用（停用后快照不再加载其权限）';
COMMENT ON COLUMN iam_role.version                 IS '乐观锁版本；更新须 WHERE version=?，冲突返回 40900';
COMMENT ON COLUMN iam_role.created_at              IS '创建时间（UTC，毫秒精度）';
COMMENT ON COLUMN iam_role.updated_at              IS '最后更新时间（UTC，毫秒精度）';
COMMENT ON COLUMN iam_role.deleted_at              IS '软删时间（UTC）；NULL=未删除，非空后 code 不可复用';
COMMENT ON INDEX  uk_iam_role_scope_code           IS '端内角色编码唯一，绑定与授权的幂等防线';
```

**类型选型（MySQL 基线）**：

| 逻辑类型 | MySQL 基线 | 未来 PostgreSQL 对照 | 说明 |
|----------|------------|----------------------|------|
| 雪花 ID | `BIGINT` | `BIGINT` | 应用生成，不使用序列 / 自增 |
| 编码 / 名称 | `VARCHAR(n)` | `VARCHAR(n)` | 长度与 domain 常量、目录校验保持一致 |
| 布尔标记 | `TINYINT`（0/1） | `BOOLEAN` | 如 `data_bearing`；应用层负责 `bool` 映射 |
| JSON 载荷 | `JSON`（原生） | `JSONB` | 如 `scope_values` / `payload`；空对象写 `{}`，禁止空串 |
| 时间 | `DATETIME(3)`（UTC） | `TIMESTAMPTZ` | 毫秒精度；不用 `TIMESTAMP`，避免 2038 与隐式时区转换 |
| 长文本 | `VARCHAR(n)` / `TEXT` | `VARCHAR(n)` / `TEXT` | 有长度上限的优先 `VARCHAR`，避免无界列 |

约定：

- 当前单引擎单目录：只维护 `migrations/*.sql` 的 MySQL 写法；引入第二引擎前必须 ADR，并分目录维护，禁止同一文件内混用方言。
- MySQL 的索引必须写在 `CREATE TABLE` 内（`KEY` / `UNIQUE KEY`）；**禁止**依赖 `CREATE INDEX IF NOT EXISTS`（MySQL 不支持），以免部分重试时因索引已存在而失败。
- MySQL 没有 `DROP INDEX IF EXISTS`；down 中删索引必须用 `ALTER TABLE ... DROP INDEX ...`，或直接 `DROP TABLE`（索引随表删除）。
- 注释长度受引擎限制（MySQL 单列注释 ≤ 1024 字符、表注释 ≤ 2048 字符），保持一行可读，避免把整段设计文档塞进 `COMMENT`。
- `COMMENT` 与 `CREATE TABLE` 必须同一次迁移提交，禁止后续补注释的独立迁移（除非修复历史遗漏，且 down 中还原旧注释）。

### 5.3.9 企业级模型补齐（2026-10-26，基线内修订）

> 触发：评审指出原 DDL 只保证「表能存下 RBAC」，缺少库级完整性、授权历史、角色模板与审批治理。因迁移基线尚未发布（无 tag、最近一次提交压平迁移），本次直接修订 `000002` / `000003`，不另开增量迁移；发布后该文件即冻结，后续变更须新增迁移。
>
> **能力边界不变**：仍不做角色继承、ABAC、部门树（§3.2 非目标）；本次只补齐「生产级完整性」，不改变 RBAC + 数据范围的判定语义。

1. **库级完整性（不依赖单点应用校验）**
   - 枚举、端别 / 子类型 / 商户组合、JSON 载荷结构全部落到 `CHECK`：`ck_iam_role_tenant`、`ck_iam_binding_tenant`、`ck_iam_change_tenant`、`ck_iam_rds_channel_scope`、`ck_iam_change_payload` 等。
   - 子表引用改为**复合外键**：`iam_role_binding (role_id, channel, merchant_id)`、`iam_role_data_scope (role_id, channel)` 指向 `iam_role` 的对应唯一键。跨端 / 跨商户绑定从「只有应用层拦」变为「库层直接拒绝」。
   - 权限目录 `parent_code` 改为 `NULL` 根节点 + 自引用外键：`''` 不再是合法根值，悬空父节点与自引用父节点（`ck_iam_permission_parent_self`）被库层拒绝。

2. **绑定历史（软撤销 + 生成列唯一键）**
   - `iam_role_binding` 新增 `revoked_at` / `revoked_by` / `revoke_reason`；解绑写撤销标记，不物理删除。
   - `active_key` 生成列：生效行恒为 `1`，撤销行取自身 `id`；唯一键扩展为 `(..., active_key)`，从而「同一主体同一角色至多一条生效绑定」与「保留全部历史」同时成立。
   - 快照 / 主体查询必须过滤 `revoked_at IS NULL`；解绑幂等；撤销后重新授权新增一行（不复活旧行），访问复核可按主体回放完整授权轨迹。

3. **角色模板（平台定义、商户实例化）**
   - 三表：`iam_role_template`、`iam_role_template_permission`、`iam_role_template_data_scope`；模板 `version` 递增，`iam_role.template_id` / `template_version` 记录实例来源与基线。
   - 实例（`iam_role`）与模板的关系是**复制而非继承**：实例化时复制权限与数据范围；模板变更后由同步任务比对 `template_version < template.version` 识别漂移，显式同步而不是运行时联动，避免平台改动无通知地扩大存量商户权限。
   - 模板删除用软删（`deleted_at`），`code` 不复用；模板权限 / 数据范围外键 `RESTRICT`，防止删空基线。

4. **审批治理**
   - `expires_at`（时效）、`decision_reason`（决策说明）、`applied_by`（执行人）、`payload_version`（载荷版本）、`expired` 状态。
   - `pending_key` 生成列唯一键：同一端别 / 商户 / 目标至多一张待审批单；终态行取 `NULL` 释放键值。
   - `apply` 仍须按 `target_id` 重新加载并校验目标聚合，不得信任载荷（原不变量不变）。

5. **代码与接口影响（本次同步项）**
   - domain：`Binding` 增加撤销字段与 `Revoke`；`ChangeRequest` 增加时效 / 决策 / 执行人字段与 `expired`；`Role` 增加模板字段。
   - repository：`BindingRepository.Delete` 语义改为 `Revoke(ctx, id, actor, reason)`；`ListBySubject` 与快照 SQL 过滤撤销；`ListByRole` 保留历史，角色删除引用检查只看生效绑定。
   - 快照 SQL（§8.3）与集成测试须补 `revoked_at IS NULL`；`internal/iam/catalog` 门禁需允许种子 `parent_code` 为 `NULL`。

### 5.4 数据纪律

- 主键统一雪花 ID（`pkg/id`），**禁止**自增主键暴露给外部；种子权限点使用自然键 `code`。
- 时间列统一 `DATETIME(3)` 存 UTC（毫秒精度），**不使用** `TIMESTAMP`（2038 上限 + 隐式时区转换）；`created_at` / `updated_at` 非空；软删 `deleted_at` 仅用于角色与目录。
- JSON 列使用原生 `JSON` 类型，写入必须是合法 JSON；空对象写 `{}`，**禁止**写空串或 `NULL`（列定义为 `NOT NULL`）。
- 禁止 `SELECT *`；列表强制分页，`page_size ≤ 100`（API-05）。
- 所有租户相关 SQL 必须带 `merchant_id`；B 端仓储方法签名必须显式接收 `merchantID`，不允许「空字符串代表全部」。
- 唯一索引是幂等的最后防线；应用层冲突映射 `40900`，不得静默覆盖。
- 角色 `version` 用于乐观锁；更新语句必须 `WHERE id=? AND version=?`，影响行数为 0 返回 `40900`。
- 绑定不设版本；重复绑定由唯一索引兜底：**刷新既有生效记录的 `expires_at` / `granted_by` 并保留原主键**（过期后重绑必须重新生效，不能只做空更新）；解绑为软撤销（写 `revoked_at` / `revoked_by`），重复解绑幂等（不存在或已撤销都返回成功），不物理删除以保留访问复核轨迹。
- 不物理删除审计相关数据；角色软删保留 `code`，防止重建洗白。
- **注释完整性**：每张表必须有表注释，每个字段必须有字段注释；新增列不带注释视为未完成。枚举字段的注释必须列全取值，并与 domain 常量一致（C-10）。注释变更随迁移提交，不得只改文档。
- **注释可检查**：MySQL（基线）可用以下查询做人工 / CI 抽检；PostgreSQL 的 `pg_description` + `obj_description` 等价查询仅作未来演进参考，当前不维护。

```sql
-- MySQL：找出缺表注释或缺列注释的 IAM 表（期望返回 0 行）
SELECT t.TABLE_NAME, c.COLUMN_NAME, t.TABLE_COMMENT, c.COLUMN_COMMENT
FROM information_schema.TABLES AS t
LEFT JOIN information_schema.COLUMNS AS c
       ON c.TABLE_SCHEMA = t.TABLE_SCHEMA
      AND c.TABLE_NAME   = t.TABLE_NAME
WHERE t.TABLE_SCHEMA = DATABASE()
  AND t.TABLE_NAME LIKE 'iam\_%'
  AND (t.TABLE_COMMENT = '' OR c.COLUMN_COMMENT = '');
```

> **门禁决策（2026-10-21）**：权限目录三处一致性**已决策用 Go 测试**（`internal/iam/catalog`，不再新增 shell 解析脚本）；注释完整性查询仍待 Owner 决定是否纳入自动门禁。若纳入，须放入 `scripts/ci/check-migrations.sh`（或新增 `check-schema-comments.sh`）并同 MR 更新 GitHub Actions、Gitee `.workflow`、`gitee-job.sh` 与 `Makefile`（CI 专文 §3.1）；需要数据库连接的检查不得在无 DB 的 CI job 中静默跳过，应显式跳过并告警。

### 5.5 迁移策略

- 采用 expand / contract：
  1. **Expand**：建表、建索引、种子权限目录；旧代码不感知。
  2. **Deploy**：发布 `iam.mode=off` 的代码（只读目录校验，不接管鉴权）。
  3. **Shadow**：写路径双写 IAM（仅测试环境）或只读比对；确认无差异。
  4. **Enforce**：切换鉴权来源；观察一个发布周期。
  5. **Contract**：删除 `cmd` 中的演示角色表与临时开关（保留 `off` 作为应急回滚位，至少一个版本）。
- **up 不可改，down 可修正**：`up` 参与 checksum，已应用后禁止修改（drift 会阻断后续迁移）；`down` 不参与 checksum，可用于修正语法错误。早期演示域 down 曾误用 `DROP INDEX IF EXISTS`（MySQL 不支持该语法），已改为 `DROP TABLE`（索引随表删除）；演示域表与迁移已随模块压平移除。
- **MySQL 8.0.19+**：种子迁移使用 `INSERT ... VALUES ... AS new ON DUPLICATE KEY UPDATE`（8.0.19 引入的行别名语法），可重放且不依赖已弃用的 `VALUES()` 函数。
- **索引内联**：所有索引写在 `CREATE TABLE` 内；MySQL 不支持 `CREATE INDEX IF NOT EXISTS`，独立建索引在部分重试场景会因索引已存在而失败。
- **外键顺序**：父表先建、子表后建；down 反向先子后父。`RESTRICT` 外键使误删被引用数据时显式失败，而不是留下悬空授权。
- `.down.sql` 必须可执行；种子数据的 down 在存在角色引用时**显式失败**并注释原因（DATA-02）。
- 生产禁止 API / worker 启动时自动迁移（DATA-03）；只允许 `cmd/migrate` 或发布流水线。

---

## 六、架构与代码落位

### 6.1 包结构

```text
internal/
├── iam/
│   ├── doc.go
│   ├── perm.go                       # IAM 自身权限点常量
│   ├── catalog/
│   │   ├── catalog.go                # 权限目录唯一真相：格式 / 唯一性 / 排序校验
│   │   └── catalog_test.go           # 与 perm.go / 种子迁移 / PERMISSIONS.md 一致性门禁
│   ├── domain/                       # M1 已落地，覆盖率 90.8%
│   │   ├── doc.go                    # 分层约束与 catalog 分工
│   │   ├── role.go                   # 角色聚合：状态机、乐观锁、授权 / 数据范围替换
│   │   ├── change_request.go         # M4.2：审批单聚合（SoD / 状态机 / 载荷校验）
│   │   ├── binding.go                # 绑定聚合：端别 / 商户 / 破玻璃时效校验
│   │   ├── scope.go                  # 端别、作用域、主体类型、数据范围与并集合并
│   │   ├── permission.go             # 权限码校验、集合语义、授权差量
│   │   ├── repository.go             # 仓储端口（消费侧定义）+ 快照契约
│   │   ├── errors.go                 # 领域哨兵 + apperr 映射辅助函数
│   │   └── *_test.go
│   ├── app/
│   │   ├── role_service.go           # M3：创建/更新/启停/克隆/软删 + 权限全量替换（差量）
│   │   ├── binding_service.go        # M3：绑定/解绑 + 主体上限 + 端别/商户隔离
│   │   ├── query_service.go          # M3：目录树、角色详情、主体快照、模拟鉴权
│   │   ├── audit.go                  # M3：ChangeRecorder（fail-closed）+ 审计事件构造
│   │   ├── outbox.go                 # ENT-03 T8：OutboxRecorder（写入 async_outbox，fail-closed）+ AuditEventHandler（供 worker 注册）
│   │   ├── uow.go                    # M4.5：UnitOfWork 端口（业务写入 + 审计同事务）+ NoopUnitOfWork
│   │   ├── rules.go                  # M3：授权校验 / 数据范围归一化
│   │   ├── authorize_service.go      # M2 已落地：快照加载（cache → DB、singleflight、300ms 超时、清洗）
│   │   ├── change_request_service.go  # 审批单用例（M4.2：创建 / 通过 / 拒绝 / 撤销 / 应用）
│   │   ├── change_request_service_test.go
│   │   └── *_test.go
│   ├── infrastructure/
│   │   ├── persistence/              # M1 已落地 memory + MySQL 8.0 仓储；M3 补 memory_snapshot；M4.2 补 memory_change_request；M4.5 补 uow.go（SQLUnitOfWork + resolveExecutor）；ENT-03 T8 删除自有 outbox，统一走 pkg/outbox
│   │   └── cache/                    # M2 已落地：版本化 JSON + L1/L2 + negative_ttl + 失效；M3 补 Redis Store / Invalidator
│   ├── observability/                # M2 已落地：Prometheus 指标适配（缓存 / 加载 / 决策 / shadow）
│   ├── transport/
│   │   ├── rest/                     # M4.1：A/B 共用解析、DTO 转换与 handler（通道无关）
│   │   ├── admin/                    # M3 已落地：15 个端点 + 路由 / 权限点
│   │   └── biz/                      # M4.1 已落地：13 个端点 + 路由（强制商户隔离）
│   └── mocks/                        # mockery 生成，禁止手改（GEN-04）
├── security/                         # M2 已落地：SnapshotLoader 端口 + off/shadow/enforce 三态
└── shared/
    ├── auth/                         # Principal（现有）
    └── authz/                        # M2 已落地：Snapshot / Scope 契约 + context 读写
```

> 命名说明：不叫 `internal/rbac`，避免与 `pkg/rbac` 混淆；`internal/iam` 覆盖「身份之后、业务之前」的授权管理语义。

### 6.2 依赖方向

```text
cmd/*-api
  → internal/iam/{transport,app,infrastructure}
      → internal/iam/domain
          → internal/shared/{apperr,auth,authz}
              → pkg/{errors,rbac}

internal/security
  → internal/shared/{auth,authz,apperr,httpx}
      → pkg/rbac
```

硬约束：

- `internal/iam/domain` **不得** import `app` / `transport` / `infrastructure` / `platform` / `database/sql`。
- `transport` **不得** import `infrastructure`（必须经 app 接口）。
- `internal/security` **不得** import `internal/iam`；通过消费侧接口 `SnapshotLoader` 由 `cmd` 注入实现（S-07）。
- `internal/shared/authz` 只放契约类型，不放实现；不得 import 任何业务域。
- `internal/iam/domain` 不得 import `iam/{app,transport,infrastructure}` 或 `internal/platform`（LAYER-05）。
- `internal/iam/transport` 不得 import `iam/infrastructure`，必须经 app 接口（LAYER-06）。
- `internal/iam/app` 仅 `authorize_service.go` 允许依赖 `infrastructure/cache` 的组合适配器；领域规则仍只能来自 domain。
- `pkg/rbac` 保持零业务依赖、零 I/O；不新增数据库 / 缓存概念。

### 6.3 关键接口

**消费侧定义的快照加载端口（`internal/security`）**：

```go
// SnapshotLoader 加载主体有效权限快照。
//
// 返回错误表示鉴权依赖不可用；调用方必须失败关闭（503），不得降级放行。
type SnapshotLoader interface {
	LoadSnapshot(ctx context.Context, p auth.Principal) (authz.Snapshot, error)
}
```

**共享契约（`internal/shared/authz`）**：

```go
// Snapshot 是某一时刻主体的有效权限快照；缓存序列化格式须保持向后兼容。
type Snapshot struct {
	Channel     string
	SubjectType string
	SubjectID   string
	MerchantID  string
	Roles       []string
	Permissions []string
	Scopes      map[string]Scope // resource -> scope
	Version     int64
	GeneratedAt time.Time
}

// Scope 是数据范围；Type 为 all / merchant / self / merchant_set。
type Scope struct {
	Type        string
	MerchantIDs []string
}

// FromContext / WithSnapshot 供 app 读取当前请求的快照；仅由 AuthZ 中间件写入。
func FromContext(ctx context.Context) (Snapshot, bool)
func WithSnapshot(ctx context.Context, s Snapshot) context.Context
```

**仓储接口（`internal/iam/domain`，节选）**：

```go
// RoleRepository 角色聚合持久化端口。
type RoleRepository interface {
	FindByID(ctx context.Context, id string) (*Role, error)
	FindByCode(ctx context.Context, channel, merchantID, code string) (*Role, error)
	Save(ctx context.Context, role *Role) error
	List(ctx context.Context, filter ListFilter) ([]*Role, error)
}

// BindingRepository 主体-角色绑定端口。
type BindingRepository interface {
	Bind(ctx context.Context, b *Binding) error
	Unbind(ctx context.Context, id string) error
	ListBySubject(ctx context.Context, q SubjectQuery) ([]*Binding, error)
}

// SnapshotRepository 鉴权快照只读端口（一条 SQL 展开，禁止 N+1）。
type SnapshotRepository interface {
	Load(ctx context.Context, q SubjectQuery) (*Snapshot, error)
}
```

### 6.4 请求链路

```text
Recover → Trace → RequestID → Metrics → AccessLog → CORS
→ Timeout → BodyLimit → RateLimit
→ AuthN（JWT，写 Principal）
→ AuthZ（Require(permission)）
     ├─ 读请求上下文快照（同一请求只加载一次）
     ├─ SnapshotLoader：L1 → L2 → DB（超时 + singleflight）
     ├─ 失败：503 / 50300，禁止放行
     ├─ 构造 rbac.Subject{Roles, Permissions}
     ├─ checker.Allow(need)
     └─ 写入 authz.Snapshot 到 context
→ TenantGuard（B 端）
→ Handler / app（数据范围二次校验）
```

- 同一请求内多个 `Require` / `RequireAny` 复用 context 中的快照，避免重复回源。
- `AuthZ` 只判断功能权限；数据范围由 app / 仓储按 `authz.Scope` 落实。
- 中间件顺序固定由 `platform.NewPipeline` 装配（REL-01 / §3.1），IAM 不另起 pipeline。

### 6.5 兼容现有代码

| 现有 API | 兼容策略 |
|----------|----------|
| `pkg/rbac.Checker` | API 不变；IAM 动态模式用空 `Checker`（不注册静态角色），仅用 `Subject.Permissions` 判定 |
| `security.NewAuthorizer(checker)` | 保留，供 demo / `iam.mode=off` 使用 |
| `security.Authorizer.Require` / `RequireAny` | 保留；无 loader 时走原 `rbac.Require` 静态逻辑；有 loader 时在 `security` 内先加载快照、再调用 `Checker.Allow`，以便把 loader 错误映射为 503（`rbac.Require` 的 `SubjectFrom` 无法返回错误，不强行改造其签名） |
| `auth.Principal.Roles` | 保留；**不信任**令牌中的角色用于授权，仅作诊断字段 |
| `cmd/*/newAuthorizer()` | 已改为接收 `*platform.Deps`，并由 `cmd/internal/iamruntime.New` 统一组装（仓储 / 缓存 / 用例 / 鉴权器 / A 端与 B 端路由）；`iam.mode` 为 `off`/`shadow` 时 B 端静态权限表仍作为兜底 |
| `docs/audit/EVENTS.md` | M4.3 已将 `rbac.breakglass` 置为 `active` 并接线（申请 / 审批 / 撤销，AUD-01）；三类授权事件均为 active |

### 6.6 领域不变量（M1 实现基线）

`internal/iam/domain` 已实现，覆盖率 90.8%，下列不变量由聚合自身强制，**不依赖 handler 或 SQL**：

| 不变量 | 实现位置 | 失败码 |
|--------|----------|--------|
| `platform` 角色必须 `channel=admin` 且 `merchant_id` 为空；`merchant` 角色必须 `channel=biz` 且 `merchant_id` 非空 | `NewRole` → `validateScopeMerchant` | `40000` |
| 破玻璃角色不得由接口直接创建，必须走审批单 | `NewRole` | `40300` |
| 权限码 2～4 段、全小写、`*` 一律拒绝（即使绕过 `NewPermissionSet` 传入集合） | `ValidatePermissionCode` + `ReplacePermissions` | `40000` |
| 停用角色不得授权（避免「先授权、后启用」绕过审批） | `ReplacePermissions` | `40900` |
| 替换授权返回差量，供审计只记录变更而非全量 | `PermissionSet.Diff` | — |
| 数据范围：B 端只允许 `merchant` / `self`；`merchant_set` 1～100 个非空商户；非集合类型不得携带商户列表 | `DataScope.Validate` | `40000` |
| 多角色数据范围合并取并集（`all` > `merchant_set` > `merchant` > `self`），超过 100 个商户显式失败而非截断 | `MergeDataScopes` | `40000` |
| 绑定端别 / 主体类型 / 商户三者自洽（A 端 `staff` 无商户，B 端 `merchant_staff` 必有商户） | `NewBinding` | `40000` |
| 绑定目标角色须同端、同商户、`active` 且未删除（跨端绑定即提权） | `Binding.ValidateAgainst` | `40000` / `40900` |
| 破玻璃绑定必须有有效期且 ≤ 2h | `Binding.ValidateAgainst` | `40000` |
| 写操作必须带操作人，每次变更 `version++`，`expectedVersion` 不匹配即冲突 | `Role.ensureMutable` | `40900` |
| 软删角色的 `code` 不可复用：`FindByCode` 仍能查到已删除角色 | 仓储语义 | `40900` |
| 审批单只能 `pending → approved → applied` / `rejected` / `cancelled`，终态不可回退 | `ChangeRequest.Approve` / `Reject` / `Cancel` / `MarkApplied` | `40900` |
| 申请人不得审批或拒绝自己的单子（SoD） | `ChangeRequest.Approve` / `Reject` | `40300` |
| 审批载荷必须是 ≤ 16 KiB 的 JSON 对象，禁止 Token / 密钥字段；B 端禁止携带 `merchant_id` | `normalizeChangePayload` | `40000` / `40300` |
| B 端载荷 `scope_type` 只允许 `merchant`（或省略），不得申请 `platform` | `normalizeChangePayload` | `40000` |

**仓储端口语义（memory 实现 94.7%）**：

| 语义 | memory 实现 |
|------|-------------|
| 未找到 → `apperr.NotFound` | `FindByID` / `FindByCode` / `FindByID(binding)` |
| 创建冲突（主键或唯一键）→ `apperr.Conflict` | `Save(expectedVersion=0)` |
| 乐观锁：`WHERE version=?` 等价校验 → `apperr.Conflict` | `Save(expectedVersion>0)` |
| `code / channel / merchant` 创建后不可变 | `Save` 拒绝变更 |
| 列表默认排除软删角色，分页上限 100 | `List` |
| 重复绑定刷新有效期 / 授权人并保留原主键（可重新生效），解绑幂等 | `Save` / `Delete` |
| 快照查询过滤过期绑定；B 端空 `merchant_id` 直接报错 | `ListBySubject` |
| 读方法返回深拷贝，调用方修改不影响内部状态 | `cloneRole` / `cloneBinding` |

> SQL 实现须逐条对齐上述语义；集成测试放在 `tests/integration/`，使用 testcontainers（MySQL 8.0，TEST-07）。引入 testcontainers 依赖需走 DEP 评审并更新 `docs/deps/` 记录。

---

## 七、API 设计

### 7.1 通用约定

- 路径前缀：A 端 `/admin/v1/iam/...`，B 端 `/biz/v1/iam/...`；C 端不提供（API-06）。
- 统一信封、分页、`trace_id`：`internal/shared/httpx`（API-03）。
- 列表 `page` / `page_size`，上限 100；排序字段白名单（API-05）。
- 写操作：
  - `POST` 创建 / 绑定 / 审批；`PUT` 全量替换角色权限；`PATCH` 局部更新；`DELETE` 解绑 / 软删角色。
  - 非天然幂等写建议带 `Idempotency-Key`；批量替换与破玻璃**强制**（复用 `pkg/idempotency`）。
  - 更新角色 / 替换权限必须带 `version`（body 或 `If-Match`）；不匹配返回 `40900`。
- 错误码只使用 `apperr` 已发布码；新增码只追加（API-04）。`50300`（鉴权依赖不可用）**已批准新增**：`internal/shared/apperr.CodeServiceUnavailable` + `Unavailable(msg)`、`pkg/httpx` 状态映射、`api/openapi/components.yaml`（`x-extensible-enum`）与单测已同步落地（见 ADR-0004）。
- 所有响应禁止回显完整 Token、密钥、他人 PII；错误消息不泄露内部 SQL / 堆栈（SEC-05）。

### 7.2 A 端接口

| 方法 | 路径 | 权限点 | 说明 | 审计 |
|------|------|--------|------|------|
| GET | `/admin/v1/iam/permissions` | `iam:permission:read` | 权限点目录（可分页 / 按 channel 过滤） | 否 |
| GET | `/admin/v1/iam/permissions/tree` | `iam:permission:read` | 菜单 / 按钮树 | 否 |
| GET | `/admin/v1/iam/roles` | `iam:role:read` | 角色列表（channel / scope / 状态过滤） | 否 |
| POST | `/admin/v1/iam/roles` | `iam:role:write` | 创建角色（code 不可重复） | `rbac.role_change` |
| GET | `/admin/v1/iam/roles/{id}` | `iam:role:read` | 角色详情（含权限点、数据范围、绑定数） | 否 |
| PATCH | `/admin/v1/iam/roles/{id}` | `iam:role:write` | 改名称 / 描述 / 状态；code 不可改 | `rbac.role_change` |
| PUT | `/admin/v1/iam/roles/{id}/permissions` | `iam:role:grant` | **全量替换**权限点（差量计算） | `rbac.role_change` |
| POST | `/admin/v1/iam/roles/{id}/clone` | `iam:role:write` | 复制角色（新 code，默认停用） | `rbac.role_change` |
| DELETE | `/admin/v1/iam/roles/{id}` | `iam:role:write` | 软删；有绑定时 `40900` | `rbac.role_change` |
| GET | `/admin/v1/iam/bindings` | `iam:binding:read` | 按主体 / 角色查询绑定 | 否 |
| POST | `/admin/v1/iam/bindings` | `iam:binding:write` | 绑定角色（可带 `expires_at` / `reason`） | `rbac.binding_change` |
| DELETE | `/admin/v1/iam/bindings/{id}` | `iam:binding:write` | 解绑（幂等） | `rbac.binding_change` |
| GET | `/admin/v1/iam/subjects/{subject_type}/{subject_id}/permissions` | `iam:binding:read` | 主体有效权限 + 角色 + 数据范围 | 否 |
| POST | `/admin/v1/iam/authorize` | `iam:check` | 模拟鉴权（指定 subject + permission + resource） | 否（拒绝记指标） |
| GET | `/admin/v1/iam/me/permissions` | 仅认证 | 当前主体有效权限 / 菜单 | 否 |
| GET | `/admin/v1/iam/change-requests` | `iam:change:read` | 审批单列表（`status` / `action` / `requested_by` 过滤，默认按申请时间倒序） | 否 |
| POST | `/admin/v1/iam/change-requests` | `iam:role:write` | 发起审批单（载荷 ≤ 16 KiB，禁敏感字段与 B 端 `merchant_id`） | 否（应用时才产生变更） |
| POST | `/admin/v1/iam/change-requests/{id}/approve` | `iam:change:approve` | 通过审批（申请人 ≠ 审批人；自审 `40300`） | 否（应用时记 `rbac.*`） |
| POST | `/admin/v1/iam/change-requests/{id}/reject` | `iam:change:approve` | 拒绝审批（同样适用 SoD） | 否 |
| POST | `/admin/v1/iam/change-requests/{id}/cancel` | `iam:change:read` | 撤销（仅申请人本人） | 否 |
| POST | `/admin/v1/iam/change-requests/{id}/apply` | `iam:change:approve` | 应用已通过审批；重新加载目标并按当前版本写入 | `rbac.role_change` / `rbac.binding_change` |
| POST | `/admin/v1/iam/breakglass` | `iam:breakglass:request` | 申请限时超级权限（≤ 2h，需开启开关） | `rbac.breakglass` |
| POST | `/admin/v1/iam/breakglass/{id}/approve` | `iam:breakglass:approve` | 审批并立即生效（申请人 ≠ 审批人） | `rbac.breakglass` |
| POST | `/admin/v1/iam/breakglass/{id}/revoke` | `iam:breakglass:approve` | 提前撤销临时绑定（需 version） | `rbac.breakglass` |

审批单载荷（`payload`）按 `action` 区分，字段名与角色 DTO 保持一致（数据范围用 `scope_type`），未知字段返回 `40000`：

| `action` | 载荷字段 | `target_id` |
|----------|----------|-------------|
| `role_create` | `scope_type` / `code` / `name` / `description` / `permissions[]` / `data_scopes[]` | 空，应用后回填新角色 ID |
| `role_update` | `name` / `description` / `status`（任意子集） | 角色 ID |
| `role_permission_replace` | `permissions[]` / 可选 `data_scopes[]` | 角色 ID |
| `role_clone` | `code` / `name` | 源角色 ID，应用后返回新角色 ID |
| `role_delete` | 无 | 角色 ID |
| `binding_create` | `role_id` / `subject_type` / `subject_id` / 可选 `expires_at` | 空，应用后回填绑定 ID |
| `binding_delete` | 无 | 绑定 ID |

### 7.3 B 端接口（M4.1 已落地；M4.6 补齐审批单）

| 方法 | 路径 | 权限点 | 说明 |
|------|------|--------|------|
| GET | `/biz/v1/iam/permissions` | `iam:role:read` | 仅 `biz` / `both` 权限点（B 端无 `iam:permission:read`） |
| GET | `/biz/v1/iam/permissions/tree` | `iam:role:read` | 菜单 / 按钮树，同样强制 `channel=biz` |
| GET | `/biz/v1/iam/roles` | `iam:role:read` | 仅本商户角色，`total` 为真实总数 |
| POST | `/biz/v1/iam/roles` | `iam:role:write` | 仅本商户；`merchant_id` 取自 Token，`channel=admin` 直接 40000 |
| GET | `/biz/v1/iam/roles/{id}` | `iam:role:read` | 他商户角色对外 `40400` |
| PATCH | `/biz/v1/iam/roles/{id}` | `iam:role:write` | 名称 / 描述 / 启停；需 `version` |
| PUT | `/biz/v1/iam/roles/{id}/permissions` | `iam:role:grant` | 仅可授予 `channel=biz/both` 的权限点 |
| POST | `/biz/v1/iam/roles/{id}/clone` | `iam:role:write` | 克隆默认停用，仍受商户配额约束 |
| DELETE | `/biz/v1/iam/roles/{id}` | `iam:role:write` | 有生效绑定则 `40900` |
| GET | `/biz/v1/iam/bindings` | `iam:binding:read` | 仅本商户绑定 |
| POST | `/biz/v1/iam/bindings` | `iam:binding:write` | 主体必须是 `merchant_staff` 且属于本商户 |
| DELETE | `/biz/v1/iam/bindings/{id}` | `iam:binding:write` | 仅本商户；跨商户 `40400` |
| GET | `/biz/v1/iam/subjects/{subject_type}/{subject_id}/permissions` | `iam:binding:read` | 本商户主体有效权限 |
| GET | `/biz/v1/iam/me/permissions` | 仅认证 | 当前商户员工有效权限 / 菜单 |
| GET | `/biz/v1/iam/change-requests` | `iam:change:request` | 仅本商户审批单；`channel` / `merchant_id` query 被忽略（M4.6） |
| POST | `/biz/v1/iam/change-requests` | `iam:change:request` | 发起申请；`merchant_id` 取自 Token，载荷带 `merchant_id` 返回 `40300`，载荷 `scope_type=platform` 返回 `40000` |
| POST | `/biz/v1/iam/change-requests/{id}/approve` | `iam:change:review` | 通过；申请人 ≠ 审批人，跨商户 `40400` |
| POST | `/biz/v1/iam/change-requests/{id}/reject` | `iam:change:review` | 拒绝；同样适用 SoD |
| POST | `/biz/v1/iam/change-requests/{id}/cancel` | `iam:change:request` | 撤销；仅申请人本人 |
| POST | `/biz/v1/iam/change-requests/{id}/apply` | `iam:change:review` | 应用；目标角色 / 绑定必须同商户，否则 `40400` |

B 端**不注册** `POST /biz/v1/iam/authorize`：模拟鉴权依赖 `iam:check`，属 A 端排障工具。
B 端**不注册**破玻璃端点：超级权限申请是平台级应急能力，B 端路由与用例双重拒绝。

B 端硬约束：

- `merchant_id` 只从 `Principal` 取，**禁止**从 query / body 接收后直接信任（SEC-14）；缺失商户的 Principal 返回 `40300`。
- 角色 `scope_type` 只能 `merchant`；请求体 `channel=admin` 一律 `40000`，不做静默改写。
- 不可授予 A 端专属权限点（`iam:permission:read`、`iam:check`、`iam:change:read`、`iam:change:approve`、`iam:breakglass:*`、`audit:read`、`config:*`）；目录 `channel` 已限制，越界返回 `40000`。B 端审批使用独立的 `iam:change:request` / `iam:change:review`。
- 自定义角色数量受 `iam.limits.max_custom_roles_per_merchant` 限制，超限返回 `40000`（软上限：Count 与写入不在同一事务，并发下可能短暂超出）。
- 跨商户访问统一表现为 `40400`，不泄露资源是否存在（SEC-14）。
- 引导顺序：B 端自助授权依赖真实绑定，因此首个商户管理员角色必须由 A 端创建并绑定；`iam.mode=off` 的静态回滚表**不授予** IAM 管理权限点，避免回滚时放大权限。

### 7.4 请求 / 响应示例

**创建角色**

```http
POST /admin/v1/iam/roles
Authorization: Bearer <token>
Idempotency-Key: 0c1f...
Content-Type: application/json

{
  "channel": "admin",
  "scope_type": "platform",
  "code": "ops_lead",
  "name": "运营主管",
  "description": "IAM 运营",
  "permissions": ["iam:permission:read", "iam:role:read", "iam:binding:read"],
  "data_scopes": [
    {"resource": "iam", "scope_type": "merchant_set", "merchant_ids": ["m_001", "m_002"]}
  ]
}
```

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "184000000000000001",
    "channel": "admin",
    "scope_type": "platform",
    "merchant_id": "",
    "code": "ops_lead",
    "name": "运营主管",
    "role_type": "custom",
    "status": "active",
    "version": 1,
    "permissions": ["iam:permission:read", "iam:role:read", "iam:binding:read"],
    "data_scopes": [
      {"resource": "iam", "scope_type": "merchant_set", "merchant_ids": ["m_001", "m_002"]}
    ],
    "created_at": "2026-10-21T06:00:00Z",
    "updated_at": "2026-10-21T06:00:00Z"
  },
  "trace_id": "4f2c..."
}
```

**全量替换角色权限（差量 + 乐观锁）**

```http
PUT /admin/v1/iam/roles/184000000000000001/permissions
If-Match: "3"
Idempotency-Key: 9a7e...

{
  "permissions": ["iam:role:read", "iam:binding:read"],
  "reason": "职责调整：移除角色写权限"
}
```

- `If-Match` 与 `version` 二选一，推荐 `If-Match`；两者不一致返回 `40000`。
- 服务端计算 `added` / `removed`，在一个事务内更新、`version+1`、写 Outbox、写审计；返回最新角色。
- 请求重复但载荷一致：按幂等键返回首次结果；载荷不一致返回 `40900`。

**模拟鉴权**

```http
POST /admin/v1/iam/authorize

{
  "subject_type": "staff",
  "subject_id": "1001",
  "permission": "iam:role:read",
  "resource": {"type": "iam", "id": "role_123", "merchant_id": "m_001"}
}
```

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "allowed": true,
    "reason": "role_permission",
    "matched_roles": ["ops_lead"],
    "data_scope": {"type": "merchant_set", "merchant_ids": ["m_001", "m_002"]},
    "snapshot_version": 12
  },
  "trace_id": "4f2c..."
}
```

> `/authorize` 是高权限只读接口，仅 A 端可用；返回最小必要信息，不返回完整角色权限表。

### 7.5 错误映射

| 场景 | 业务码 | HTTP | 说明 |
|------|--------|------|------|
| 参数 / 权限码格式非法 | 40000 | 400 | 不泄露内部字段 |
| 未认证 / Token 无效 | 40100 | 401 | 不区分具体失败原因 |
| 无权限 / 跨端调用 | 40300 | 403 | B 端跨商户优先 40400 |
| 角色 / 绑定 / 审批单不存在 | 40400 | 404 | 含越权对外表现 |
| code 重复 / 版本冲突 / 角色有绑定 | 40900 | 409 | 消息指出冲突类型 |
| 角色数 / 绑定数超限、限流 | 42900 | 429 | 带 `Retry-After` |
| 审计 / Outbox / DB 写失败 | 50000 | 500 | 记录 `trace_id` |
| 快照依赖（DB + 缓存）不可用 | **50300** | 503 | 失败关闭，带 `Retry-After` |

> `50300` 已按 ADR-0004 批准新增：`apperr.CodeServiceUnavailable` 与 `apperr.Unavailable(msg)` 已实现，`xerr.CodeUnavailable ↔ 50300` 双向映射；`pkg/httpx` 的 `BizCodeServiceUnavailable` 与 `HTTPStatusFromBizCode` 返回 503。契约侧因码表只增不减，`ErrorCode` 使用 `x-extensible-enum` 声明为开放集合，使新增码不被 oasdiff 判为 breaking；客户端须对未知码兜底。**禁止**改用 `40300` 表达依赖不可用。

---

## 八、鉴权决策

### 8.1 Principal 与信任边界

| 字段 | 来源 | IAM 是否信任 |
|------|------|--------------|
| `channel` | JWT claim / 进程配置 | 信任，且必须与进程端别一致 |
| `subject_id` | JWT `sub` | 信任 |
| `merchant_id` | JWT claim | B 端信任；用于绑定与数据范围 |
| `roles` / `permissions` | JWT（若存在） | **不信任**，仅诊断；不得据此放行 |
| `request_id` / `trace_id` | 中间件 | 信任，用于日志 / 审计 |

- JWT 只放身份与渠道；**禁止**把权限点全集写入 claim（体积、撤销、泄露风险）。
- B 端 Token 的 `merchant_id` 与路径 / Body 中的商户不一致时，`TenantGuard` 直接拒绝（401/403），不进入 app。
- A/B Token 不得互换（SEC-03 / SEC-04）；IAM 再次校验 `Principal.Channel` 与角色 `channel` 一致。

### 8.2 快照加载与判定

```text
LoadSnapshot(principal):
  1. 校验 principal.channel ∈ {admin,biz}，subject_id 非空；B 端 merchant_id 非空
  2. 读请求 context 中的 Snapshot；命中则返回
  3. 读 L1（进程内，TTL 3s）；命中则注入 context 并返回
  4. 读 L2（Redis，TTL 5m）；命中则回填 L1、注入 context 并返回
  5. 回源 DB（单条 SQL，超时 300ms，singleflight 合并同 key 并发）
  6. 清洗快照：过滤 disabled/过期绑定；非破玻璃移除 * / resource:*
  7. 回填 L2 / L1；注入 context
  8. 任一步失败：返回 error，由中间件映射 503 / 50300
```

**缓存 Key 与空值**：

```text
{env}:{channel}:{merchant_id}:iam:subject:{subject_type}:{subject_id}
```

- 由 `pkg/redis.Key` 拼接，`env` 由 platform 前缀统一注入；Key 必须包含 `channel` 与 `merchant_id`，禁止跨租户复用。
- 无角色 / 无权限的主体缓存**空快照**（`negative_ttl=30s`），防止恶意或异常主体打穿到 DB；授权变更后主动删除。
- L1 使用 `pkg/cache.Memory`（或 `Multi` 的 L1 层），L2 使用 `pkg/redis.Cache`；回源使用其 `GetOrLoad` 的进程内 singleflight，跨实例击穿由短 TTL + 可选 `pkg/redis.Lock` 兜底。
- 缓存值带 `schema_version`；反序列化失败视为 miss，不得 panic，也不得放行。

**M2 实现约定**（`internal/iam/app.AuthorizeService` + `internal/iam/infrastructure/cache`）：

- `security.Authorizer` 通过 `WithSnapshotLoader` 注入 loader；未显式指定模式时默认 `enforce`，`WithMode(ModeOff)` 完全跳过快照 I/O。
- `shadow` 仅加载并比对，**仍按静态表放行**；loader 失败也只记录 `OnLoadError`，且不把快照写入 context，避免下游数据范围被意外收缩。
- 空快照编码为 `roles=[]`、`permissions=[]` 的正常快照，L1/L2 均使用 `negative_ttl`（30s）；不使用负数占位标记，保证 Memory 与 Redis 行为一致。
- L2 读错误降级到 DB（`result=error` 指标）；L2 写 / 删失败只告警，不阻断鉴权；解码失败会删除坏 key 并视为 miss。
- 回源 singleflight 以完整缓存 key 合并；`LoadTimeout` 只包住 DB 调用，缓存 I/O 受请求 context 约束。
- 清洗阶段 `allow_wildcard=false`（prod 固定）会移除所有含 `*` 的权限；破玻璃的显式通配符展开在 M4 单独开启。
- 快照规模超过 `max_roles_per_subject` / 内部权限上限时**失败关闭**（50300），不做截断。

**判定**：

```go
sub := rbac.Subject{Roles: snap.Roles, Permissions: snap.Permissions}
if !checker.Allow(sub, need) {
    return 403
}
// 功能权限通过后，app 读取 authz.Snapshot.Scopes 落实数据范围
```

- 动态模式下 `checker` 不注册静态角色、不配置 super roles；`Allow` 仅依赖快照中的有效权限。
- `pkg/rbac` 的通配符语义保持不变；清洗阶段保证只有破玻璃快照可含 `*`。
- 空快照 = 无权限，默认拒绝。
- `AllowAny` / `AllowAll` 语义不变；IAM 路由优先使用单个权限点，避免歧义。

### 8.3 有效权限 SQL（单条，禁止 N+1）

```sql
SELECT DISTINCT
       r.id            AS role_id,
       r.code          AS role_code,
       p.code          AS permission_code
FROM iam_role_binding b
JOIN iam_role r
  ON r.id = b.role_id
 AND r.channel = b.channel
 AND r.status = 'active'
 AND r.deleted_at IS NULL
JOIN iam_role_permission rp
  ON rp.role_id = r.id
JOIN iam_permission p
  ON p.code = rp.permission_code
 AND p.status = 'active'
 AND p.channel IN (?, 'both')
WHERE b.channel = ?
  AND b.subject_type = ?
  AND b.subject_id = ?
  AND b.merchant_id = ?
  AND b.revoked_at IS NULL
  AND (b.expires_at IS NULL OR b.expires_at > ?);
```

数据范围单独一条：

```sql
SELECT s.resource, s.scope_type, s.scope_values
FROM iam_role_data_scope s
JOIN iam_role_binding b ON b.role_id = s.role_id
JOIN iam_role r ON r.id = b.role_id
WHERE b.channel = ?
  AND b.subject_type = ?
  AND b.subject_id = ?
  AND b.merchant_id = ?
  AND b.revoked_at IS NULL
  AND r.status = 'active'
  AND r.deleted_at IS NULL
  AND (b.expires_at IS NULL OR b.expires_at > ?);
```

- 两条查询可在同一事务 / 同一连接内执行；结果合并为 `authz.Snapshot`。
- 角色数、权限数受 `iam.limits` 限制，防止单主体快照过大。
- 缓存序列化使用版本化结构（`schema_version`），旧缓存反序列化失败视为 miss，不得 panic。

### 8.4 失败策略

| 故障 | 行为 | 可观测 | 业务影响 |
|------|------|--------|----------|
| L1 miss | 读 L2 | `iam_authz_cache_total{layer="l1",result="miss"}` | 无 |
| L2 miss | 回源 DB | `...{layer="l2",result="miss"}` | 延迟上升 |
| L2 错误 | 记录并回源 DB（不阻断） | `...{result="error"}` + Warn | 延迟上升 |
| DB 超时 / 不可用 | **拒绝**，返回 503 / 50300 | `iam_authz_decisions_total{result="unavailable"}` + Error | A/B 鉴权失败；触发告警 |
| 快照反序列化失败 | 视为 miss，回源；连续失败告警 | Warn + 指标 | 无 |
| 审计 / Outbox 写失败 | **业务写回滚**，返回 50000 | Error + 审计告警 | 变更失败，可重试 |
| 缓存删除失败 | 记录并告警；依赖 L1 3s + L2 5m 过期 | Error + `iam_cache_invalidate_failed_total` | 撤销延迟至最坏 5m |

> **禁止**在鉴权失败时回退到「令牌里的 roles」或「默认放行」。这是本方案的安全底线。

### 8.5 超级管理员与破玻璃

| 机制 | 规则 |
|------|------|
| 生产超级角色 | 默认不存在；`rbac.WithSuperRoles` 仅 local/demo 使用 |
| 破玻璃角色 | 仅 `role_type=breakglass` 的系统角色可产生 `*`；**权限目录本身永不含 `*`**，避免被普通授权引用 |
| 破玻璃申请 | 必须由 `iam:breakglass:request` 持有人发起，填写原因、时长（≤ 2h）、目标权限 |
| 审批 | 必须由另一名 `iam:breakglass:approve` 持有人批准；申请人 ≠ 审批人 |
| 生效 | 创建一条 `expires_at` 的临时绑定到 `breakglass` 角色；快照含 `*`；到期自动失效 |
| 审计 | 申请、批准、生效、每次使用、到期全部记 `rbac.breakglass`；触发实时告警 |
| 撤销 | 支持提前撤销；撤销后删除缓存并等待 L1 过期（≤ 3s） |
| 防绕过 | 普通绑定接口拒绝 `breakglass` 角色（40300）；破玻璃角色禁止写入权限点，超级权限只能由快照临时展开 |
| 配置 | `iam.breakglass.enabled=false` 为生产默认；启用须安全 Owner 审批并配置允许主体 |

> 破玻璃不是「日常运维账号」。任何长期 `*` 权限都视为安全缺陷。

---

## 九、安全设计

### 9.1 威胁模型

| 威胁 | 场景 | 控制 |
|------|------|------|
| 垂直提权 | 普通运营给自己授予 `iam:role:grant` | 权限点校验 + SoD + 审批 + 审计；禁止修改自身绑定（可配置） |
| 水平越权 | B 端商户 A 操作商户 B 的角色 | `merchant_id` 从 Token 取；仓储条件；越权 404 |
| 跨端提权 | A 端 Token 调 B 端 IAM | `channel` 校验；角色 `channel` 校验；Token 域隔离 |
| 通配符滥用 | 自定义角色写入 `*` | API 校验 + 目录约束 + 启动期检查；仅破玻璃可 `*` |
| 过期权限 | 解绑 / 降权后仍可访问 | 快照 TTL + 主动删除；撤销 SLA ≤ 3s；指标告警 |
| 缓存投毒 | 伪造缓存值提升权限 | 仅服务端写缓存；值含 `schema_version` + 合法性校验；Redis 网络隔离 |
| 权限码漂移 | 代码常量与 DB 目录不一致 | 启动期校验 + CI 目录测试 + 种子迁移评审 |
| 审计绕过 | 直接改 DB 或跳过 Emitter | 所有写路径经 app；审计与业务同事务；生产 DB 权限最小化；审计追加写 |
| 枚举 | 通过 403/404 差异探测他人资源 | B 端统一 404；错误消息统一；限流 |
| 内部人滥用 | A 端管理员批量授权 | 审批 + SoD + 审计 + 定期访问复核（quarterly access review） |
| 依赖故障 | DB / Redis 故障导致越权或全站不可用 | 失败关闭；缓存分层；超时 / 熔断；容量与降级预案 |
| 重放 | 重复提交授权请求 | 唯一约束 + `Idempotency-Key` + 版本号 |

### 9.2 安全 Checklist

- [ ] 所有 IAM 路由挂 `AuthN` + `Require(permission)`，无匿名管理接口（SEC-07）。
- [ ] 权限点目录无生产 `*`；自定义角色创建 / 更新拒绝通配符。
- [ ] B 端所有查询 / 写入带 `merchant_id`；跨商户返回 404（SEC-14）。
- [ ] A/B 角色、绑定、Token 不互通（SEC-03 / SEC-04）。
- [ ] 写操作有幂等键或唯一约束；版本冲突返回 409（REL-04）。
- [ ] 审计无 Token / 密钥 / 完整 PII（SEC-05 / AUD-08）。
- [ ] 破玻璃默认关闭；启用有 allowlist、双人审批、告警。
- [ ] 缓存 Key 含 `env` / `channel` / `merchant_id` / `subject_id`，禁止跨租户复用。
- [ ] 管理接口限流；异常枚举有监控。
- [ ] 生产 `iam.mode=enforce`，禁止 `off` / `shadow`（除非迁移 ADR）。

---

## 十、审计与合规

### 10.1 事件目录

在 [`docs/audit/EVENTS.md`](./audit/EVENTS.md) 追加 / 激活：

| action | 端 | 说明 | 保留类别 | 最短保留 | 初始 status |
|--------|----|------|----------|----------|-------------|
| `rbac.role_change` | A/B | 角色创建 / 更新 / 停用 / 删除 / 权限替换 / 克隆 | 权限 | 180d | M3 起 `active` |
| `rbac.binding_change` | A/B | 角色绑定 / 解绑 / 过期变更 | 权限 | 180d | M3 起 `active` |
| `rbac.breakglass` | A | 破玻璃申请 / 审批 / 生效 / 使用 / 撤销 | 权限 | 180d | M4 起 `active` |

同步更新 `internal/shared/audit/actions.go` 常量与 `KnownActions()`；`check-audit.sh` 必须通过（AUD-01/02/04）。

> **M3/M4.4/M4.5 实现状态**：`RoleService` / `BindingService` / `BreakglassService` / 审批应用统一通过 `app.ChangeRecorder` 记录审计，写失败即失败关闭（`50000`）。默认注入 `app.SlogRecorder`（结构化日志，M3）；`iam.outbox.enabled=true` 时切换为 `app.OutboxRecorder`，事件先持久化到 `iam_outbox`，再由进程内发布器投递到审计出口（M4.4）。M4.5 起审计写入与业务写入处于同一个数据库事务：审计不可写时业务变更一并回滚，不会留下「已生效但无审计」的记录。§14 的「staging enforce 稳定」现由配置门禁强制：prod/staging + enforce 必须启用 outbox，且 prod 必须使用持久化 `db.driver`。

### 10.2 事件字段

| 字段 | 值 |
|------|-----|
| `event_id` | 雪花 / UUID，全局唯一 |
| `action` | 上表稳定 action |
| `actor_id` | 操作人 `subject_id` |
| `actor_type` | `staff` / `merchant_staff` |
| `channel` | `admin` / `biz` |
| `merchant_id` | B 端必填；A 端按目标资源 |
| `resource_type` | `iam_role` / `iam_binding` / `iam_breakglass` |
| `resource_id` | 角色 / 绑定 / 审批单 ID |
| `outcome` | `success` / `failure` / `denied` |
| `occurred_at` | UTC |
| `trace_id` / `service` | 与请求一致 |
| `reason` | 变更原因（`require_reason=true` 时必填） |
| `attrs` | `change_type`、`before_hash`、`after_hash`、`added_count`、`removed_count`、`version` |

约束：

- **禁止**记录完整权限列表作为审计载荷；用 `before_hash` / `after_hash` + 数量，需要时按 `resource_id` 回溯版本。
- **禁止**记录 Token、密码、密钥、完整 PII（AUD-08）。
- 审计写入与业务写入**同一事务**：`async_outbox` 插入失败则业务回滚（fail-closed）。
- 生产由 worker 将 Outbox 投递到审计存储 / SIEM；`SlogEmitter` 仅用于 local/demo（AUD 例外）。
- 保留期 ≥ 180 天；权限变更历史不可被业务进程 UPDATE / DELETE（AUD-06）。

### 10.3 访问复核

- 每季度导出 A 端高权限角色（含 `iam:*`、`data:export`、`config:*`）及绑定，由安全 Owner 复核。
- 离职 / 转岗：解绑 + 过期时间兜底；复核结果归档到审计存储。
- 破玻璃使用后 24h 内必须提交复盘说明。

---

## 十一、可观测性

### 11.1 日志

必须包含：`service`、`channel`、`env`、`trace_id`、`request_id`、`subject_id`、`merchant_id`（B）、`permission`、`role_id`、`result`、`source`（l1/l2/db）、`duration_ms`。

禁止：完整 Token、密钥、密码、完整权限列表、他人 PII。

分级：

- 预期内 401/403：`Warn` / `Info`，不打 `Error`（OBS-06）。
- 依赖不可用 / 审计失败 / 缓存删除失败：`Error` + 告警。
- 破玻璃使用：`Warn` + 专用审计事件 + 实时告警。

### 11.2 指标

| 指标 | 类型 | 标签 | 说明 |
|------|------|------|------|
| `iam_authz_decisions_total` | Counter | `channel`、`result=allow\|deny\|unavailable` | 决策总量；`deny` 突增告警 |
| `iam_authz_duration_seconds` | Histogram | `channel`、`source=l1\|l2\|db` | P50/P95/P99 |
| `iam_authz_cache_total` | Counter | `layer`、`result=hit\|miss\|error` | 命中率 |
| `iam_authz_snapshot_permissions` | Histogram | `channel` | 快照权限数，防角色膨胀 |
| `iam_authz_load_errors_total` | Counter | `stage=cache\|db\|cache_write\|limit\|shadow` | 快照加载 / 缓存写失败；`db` 连续增长告警 |
| `iam_shadow_mismatch_total` | Counter | `channel`、`static_allow`、`iam_allow` | shadow 与静态表判定差异；切 enforce 前必须为 0 或逐条解释 |
| `iam_role_changes_total` | Counter | `channel`、`operation`、`outcome` | M3 已落地：角色变更量与失败率（由审计装饰器计数） |
| `iam_binding_changes_total` | Counter | `channel`、`operation`、`outcome` | M3 已落地：绑定变更量与失败率 |
| `iam_bindings_total` | Gauge | `channel`、`scope_type` | 绑定规模，低基数（M4：需定期聚合，避免每请求查询） |
| `iam_outbox_pending` | Gauge | — | M4.4 已落地：未投递事件数（含重试等待与死信） |
| `iam_outbox_lag_seconds` | Gauge | — | M4.4 已落地：最老未投递事件的积压秒数 |
| `iam_outbox_delivery_total` | Counter | `result=success\|retry\|dead\|poison` | M4.4 已落地：投递结果；`dead` / `poison` >0 必须告警 |
| `iam_cache_invalidate_failed_total` | Counter | `layer` | 缓存删除失败 |
| `iam_breakglass_active` | Gauge | — | 活跃破玻璃数（>0 告警，M4） |

**标签纪律**：禁止把 `subject_id`、`role_id`、`permission`、`merchant_id` 作为 Prometheus 标签（高基数）。这些放日志 / Trace / 审计。

> **实现注意（M4.4 修复）**：平台 `metrics.New` 会把 `service` / `channel` / `env` 注入为常量标签，而 IAM 指标自带 `channel` 变量标签；两者同名会被 Prometheus 拒绝（启动即失败）。因此 IAM 通过 `Metrics.RegistererWithoutChannel()` 注册（保留 `service` / `env` 常量标签），`cmd/internal/iamruntime` 有回归测试覆盖。

### 11.3 追踪

- **现状（M3）**：`platform` 统一挂载 `tracing.HTTPMiddleware`，每个请求生成 `METHOD <route 模板>` span（含 `http.route`），
  因此 `POST /admin/v1/iam/roles` 等变更接口天然有 span；span 内不记录 Token 与权限全量。
- 后续可选增强（M4）：在用例内追加 `iam.authorize` / `iam.snapshot.load` / `iam.role.change` 子 span，
  属性 `channel`、`permission`、`source`、`operation`、`resource_id`、`outcome`；禁止把 `subject_id` 作为指标标签。

### 11.4 健康检查与告警

- `admin-api` / `biz-api` 的 `/readyz` 注册 `iam_db` 与 `iam_cache`（若启用 Redis）检查器；失败返回 503（OBS-03）。M3 已在 `cmd/admin-api/iam.go` 的 `registerIAMHealth` 落地：仅当 `db.driver≠memory` / 已配置 Redis 时才注册，避免出现永远成功的假检查。
- `c-api` 不注册 IAM 检查器（不依赖 IAM）。
- 告警基线：

| 告警 | 条件 | 级别 |
|------|------|------|
| 鉴权拒绝率突增 | `deny / total > 5%` 持续 5m | P2 |
| 鉴权不可用 | `unavailable > 0.1%` 持续 1m | P1 |
| 鉴权延迟 | P99 > 50ms 持续 5m | P2 |
| 缓存命中率下降 | < 95% 持续 10m | P2 |
| 缓存删除失败 | `> 0` 持续 5m | P1 |
| Outbox 积压 | `iam_outbox_pending > 1000` 或 `iam_outbox_lag_seconds > 60`（M4.4） | P1 |
| 破玻璃活跃 | `iam_breakglass_active > 0` | P1（即时通知） |
| 角色变更失败率 | > 1% 持续 10m | P2 |

Runbook：`docs/runbooks/iam.md`（RB-01～RB-07），覆盖缓存故障、DB 故障、误授权回滚、破玻璃撤销、Outbox 积压（含 pending / retry / dead 三种分诊路径）、迁移回滚；`docs/runbooks/ALERTS.md` 已登记 5 条 IAM 告警（含 `iam.outbox.backlog`）。

---

## 十二、配置与部署

### 12.1 配置草案

```yaml
# configs/admin.yaml.example / biz.yaml.example 追加；c.yaml 不添加
iam:
  mode: enforce                 # off | shadow | enforce

  load_timeout: 300ms
  cache:
    driver: redis               # memory | redis；prod 必须 redis
    l1_ttl: 3s
    l2_ttl: 5m
    negative_ttl: 30s
    key_prefix: iam

  limits:
    max_roles_per_subject: 20
    max_permissions_per_role: 200
    max_bindings_per_subject: 50
    max_custom_roles_per_merchant: 50

  security:
    allow_wildcard: false
    require_reason: true
    sod_enabled: true
    sod_conflicts:
      - ["iam:role:grant", "audit:read"]
      - ["iam:breakglass:request", "iam:breakglass:approve"]

  approval:
    enabled: false       # true 时角色 / 绑定直写端点返回 40900，必须走 /change-requests

  breakglass:
    enabled: false        # 生产默认关闭；启用要求 mode=enforce，prod 必填 allowed_subjects
    allowed_subjects: []  # 允许申请的主体白名单；非空时名单外主体返回 40300
    # 时长上限硬编码 2h（domain.MaxBreakglassDuration），不提供配置项，避免误改放大权限

  outbox:
    enabled: false        # 审计事件先持久化再投递；prod/staging + enforce 必须 true
    poll_interval: 1s     # 拉取间隔，[100ms, 1m]
    batch_size: 50        # 单轮拉取上限，[1, 500]
    max_attempts: 8       # 投递次数上限，耗尽进死信，[1, 20]
```

环境变量由 `pkg/config` 按 yaml tag 自动推导，例如：

| 变量 | 字段 |
|------|------|
| `ADMIN_FRAME_IAM_MODE` | `iam.mode` |
| `ADMIN_FRAME_IAM_LOAD_TIMEOUT` | `iam.load_timeout` |
| `ADMIN_FRAME_IAM_CACHE_DRIVER` | `iam.cache.driver` |
| `ADMIN_FRAME_IAM_CACHE_L1_TTL` | `iam.cache.l1_ttl` |
| `ADMIN_FRAME_IAM_CACHE_L2_TTL` | `iam.cache.l2_ttl` |
| `ADMIN_FRAME_IAM_SECURITY_ALLOW_WILDCARD` | `iam.security.allow_wildcard` |
| `ADMIN_FRAME_IAM_BREAKGLASS_ENABLED` | `iam.breakglass.enabled` |
| `ADMIN_FRAME_IAM_APPROVAL_ENABLED` | `iam.approval.enabled` |
| `ADMIN_FRAME_ASYNC_ENABLED` | `async.enabled` |
| `ADMIN_FRAME_ASYNC_DRIVER` | `async.driver` |
| `ADMIN_FRAME_ASYNC_OUTBOX_PUBLISH_IN_API` | `async.outbox.publish_in_api` |
| `ADMIN_FRAME_ASYNC_OUTBOX_POLL_INTERVAL` | `async.outbox.poll_interval` |
| `ADMIN_FRAME_ASYNC_OUTBOX_BATCH_SIZE` | `async.outbox.batch_size` |
| `ADMIN_FRAME_ASYNC_OUTBOX_LEASE` | `async.outbox.lease` |
| `ADMIN_FRAME_ASYNC_OUTBOX_MAX_ATTEMPTS` | `async.outbox.max_attempts` |
| `ADMIN_FRAME_ASYNC_OUTBOX_CONCURRENCY` | `async.outbox.concurrency` |
| `ADMIN_FRAME_ASYNC_CONSUMER_CONCURRENCY` | `async.consumer.concurrency` |
| `ADMIN_FRAME_ASYNC_CONSUMER_VISIBILITY_TIMEOUT` | `async.consumer.visibility_timeout` |
| `ADMIN_FRAME_ASYNC_CONSUMER_RETRY_BACKOFF` | `async.consumer.retry_backoff` |

> 数组 / 映射类字段（`sod_conflicts`、`allowed_subjects`、`required_permissions`）建议只走 yaml，不强行支持环境变量批量覆盖；如需支持，须在 CONFIG 专文登记并加 `Validate` 单测。

> **实施状态**：M2 已提供 `security.Mode` / `ParseMode`、`cache.Config`、`app.AuthorizeConfig` 与 `observability.NewMetrics`；M3 已把 `iam` / `redis` 段接入 `internal/platform.Config`（`applyIAMDefaults` + `validateIAM`）；M4.1 已把仓储 / 缓存 / 用例 / 传输层装配收敛到 `cmd/internal/iamruntime`，A / B 端共用同一实例。M4.3 已把 `breakglass.enabled` / `allowed_subjects` 接入 `Validate`（含 admin-only、enforce 与 prod 白名单要求）；M4.7 已把 `approval.enabled` 接入 `applyIAMDefaults` / `validateIAM`。ENT-03 T8 起 Outbox 参数统一走 `async.*`（`iam.outbox.*` 已删除）：`async.outbox.poll_interval / batch_size / lease / max_attempts / concurrency` 由 `validateAsync` 校验，prod/staging + `iam.mode=enforce` 强制 `async.enabled=true`；`approval.required_permissions` / `sod_*` / `max_duration` 仍未实现，写了会被 `pkg/config` 严格模式拒绝。

### 12.2 Validate 基线

`internal/platform.Config.Validate()` 已落地（`validateIAM` + `config_iam_test.go`）：

- `iam.mode ∈ {off,shadow,enforce}`；`env=prod` 允许 `enforce` 与 `off`（`off` 仅用于应急回滚，必须触发配置变更审计）；`shadow` 仅迁移期允许。
- `iam.load_timeout > 0` 且 ≤ 1s；`l1_ttl ≤ 5s`；`l2_ttl ≤ 15m`；`l2_ttl > l1_ttl`；`negative_ttl ≤ l2_ttl`。
- `env ∈ {prod,staging}` 且 `iam.mode=enforce` ⇒ `cache.driver=redis` 且 `redis.addr` 非空。
- `iam.security.allow_wildcard=false`（prod 强制）。
- `channel ∈ {c, worker}` ⇒ `iam.mode=off`（SEC-14：非 A/B 端不启用 IAM）。
- 未知 `iam.mode` / `cache.driver` / 负值时长一律拒绝。
- 配置错误时 `errors.Join` 聚合，启动失败并打印字段名，不打印值（CFG-03 / CFG-05）。

M4.3 已落地：`breakglass.enabled`（仅 admin + enforce，prod 必须配置 `allowed_subjects`）；ENT-03 T8 已落地：`async.outbox.poll_interval ∈ [100ms,1m]`、`batch_size ∈ [1,500]`、`lease ∈ [5s,10m]`、`max_attempts ∈ [1,20]`、`concurrency ∈ [1,16]`（零值视为默认，不误报），`env ∈ {prod,staging}` 且 `iam.mode=enforce` ⇒ `async.enabled=true`，`async.enabled=true` ⇒ `db.driver≠memory`；M4.7 已落地 `approval.enabled`（仅 A/B 端；prod 要求 `db.driver≠memory`），`approval.required_permissions` / `sod_*` 仍未实现。B 端 `iam.limits.max_custom_roles_per_merchant` 已落地（仅 B 端生效，A 端平台角色不受此限）。

### 12.3 部署

| 项 | 说明 |
|----|------|
| 进程 | 不新增 cmd；`admin-api` 挂 A 端 IAM，`biz-api` 挂 B 端 IAM；`c-api` 不挂 |
| 迁移 | 仅 `cmd/migrate` / 发布流水线执行；expand → deploy → enforce → contract |
| 依赖 | 复用现有 DB / Redis；不新增外部服务 |
| 就绪 | `admin-api` / `biz-api` 注册 `iam_db`、`iam_cache` 检查器 |
| Worker | ENT-03 起由 `cmd/worker` 独占平台 `AsyncRuntime` 投递 `async_outbox`（`iam.audit` / `iam.cache`，退避 + 死信 + 积压指标）；API 侧 `async.outbox.publish_in_api` 生产强制 false |
| 回滚 | 切 `iam.mode=off`（恢复演示 / 旧静态表）+ 清缓存；无需回滚 schema |
| 容量 | 初始 1 万角色、10 万绑定；索引与分页保证管理接口不拖垮 DB |

---

## 十三、测试与质量门禁

### 13.1 测试矩阵

| 层级 | 内容 | 门禁 |
|------|------|------|
| domain | 角色状态机、code 校验、通配符拒绝、数据范围、过期、SoD、目录格式 | 覆盖率 ≥ 70%（实测 90.8%，TEST-02） |
| catalog | 权限目录格式 / 唯一性 / 排序；`perm.go` 常量、种子迁移、`PERMISSIONS.md` 三处一致 | `go test ./internal/iam/catalog/... -count=1` |
| app | 事务、幂等、版本冲突、审计 Outbox、缓存删除、审批流 | 关键用例覆盖 |
| `internal/security` | loader 错误 → 503、无 loader 兼容、跨端、超级角色、401/403 | 覆盖率 ≥ 80%（TEST-02c；实测 94.1%） |
| `internal/iam/app`（快照） | singleflight、300ms 超时、失败关闭、negative cache、清洗与上限 | 覆盖率 ≥ 70%（实测 76.6%） |
| `internal/iam/infrastructure/cache` | schema 版本、L2 降级、脏值删除、TTL、失效可观测 | 覆盖率 ≥ 70%（实测 77.8%） |
| infrastructure | SQL 条件、唯一约束、租户隔离、并发写入、分页 | `tests/integration/iam`（`IAM_TEST_MYSQL_DSN` + MySQL 8.0 service，§13.6） |
| e2e | A 建角色 → 授权 → 绑定 → 鉴权；B 越权 404；撤销 ≤ 3s | `tests/e2e/{admin,biz}/` |
| 契约 | OpenAPI validate + breaking | `make openapi`（API-01/07/08） |
| 性能 | 缓存命中、回源、快照大小 | Benchmark 登记（PERF-01/02） |
| Fuzz | 权限码 / 角色码解析、非法 JSON | 不 panic、不越权 |

### 13.2 必测函数名

除仓库基线的 `TestBizRoleTenantIsolation` / `TestBizChangeRequestTenantIsolation` 必须继续存在外，IAM 的设计基线如下：

```text
TestDenyByDefault
TestPrivilegeEscalationPrevented
TestCrossChannelIsolation
TestExpiredBindingIgnored
TestWildcardRejected
TestDataScopeMerchant
TestDataScopeSelf
TestVersionConflict
TestConcurrentGrantIsIdempotent
TestAuditOnRoleChange
TestAuditOnBindingChange
TestCacheInvalidation
TestSnapshotLoadFailureClosed
TestBreakglassRequiresApproval
TestPermissionCatalog
TestPermissionCatalogMatchesConstants
TestPermissionCatalogMatchesSeedMigration
TestPermissionCatalogMatchesDoc
TestPermissionCatalogDownMigration
TestPermissionCatalogSeedIsIdempotent
```

> **落地映射（M1～M3）**：上表是评审用的能力清单，实际命名以代码为准，语义等价即视为覆盖：
>
> | 设计名 | 实际测试 |
> |--------|----------|
> | `TestDenyByDefault` / `TestPrivilegeEscalationPrevented` | `TestSimulateAuthorizeUsesSnapshot`、`TestBindRejectsCrossChannelAndDisabledRole`、`security` 的 `TestAuthorizer*` |
> | `TestConcurrentGrantIsIdempotent` | `TestBindLoadsSnapshotAndIsIdempotent`、`TestMemoryBindingRepositorySaveIsIdempotent` |
> | `TestAuditOnRoleChange` / `TestAuditOnBindingChange` | `TestCreateRolePersistsPermissionsAndAudits`、`TestReplacePermissionsComputesDiffAndInvalidatesSubjects`、`TestBindLoadsSnapshotAndIsIdempotent` |
> | `TestCacheInvalidation` | `TestReplacePermissionsComputesDiffAndInvalidatesSubjects`、`TestUnbindIsIdempotentAndAudited`、`TestSnapshotCache*` |
> | `TestBreakglassRequiresApproval` | `TestBreakglassBindingRequiresBoundedExpiry`、`TestNewBreakglassGrantRejectsInvalidInput`、`TestBreakglassRequestApproveExpandsWildcardSnapshot` |

**M1 已落地**（`internal/iam/domain`、`internal/iam/infrastructure/persistence`）：

```text
TestWildcardRejected                 # 通配符在集合构造与聚合替换两处都被拒绝
TestCrossChannelIsolation            # A 端绑定不能指向 B 端角色（反之亦然）
TestExpiredBindingIgnored            # 过期绑定在域内与仓储查询两处都被过滤
TestDataScopeMerchant                # B 端 merchant 允许
TestDataScopeSelf                    # self 在两个端别都允许
TestVersionConflict                  # 乐观锁不匹配返回 40900 且状态不变
TestBreakglassBindingRequiresBoundedExpiry  # 破玻璃必须限时且 ≤ 2h
TestMemoryRoleRepositoryOptimisticLock
TestMemoryBindingRepositoryTenantIsolation
```

**M2 已落地**（`internal/shared/authz`、`internal/security`、`internal/iam/{app,infrastructure/cache,observability}`）：

```text
TestAuthorizeServiceCachesAndCleans            # 回源一次 + L1 命中 + 清洗 / 去重 / 排序
TestAuthorizeServiceNegativeCache              # 空快照 negative cache，不打穿 DB
TestAuthorizeServiceRepositoryFailureIsClosed  # DB 失败 → 50300，且不写缓存
TestAuthorizeServiceTimeout                    # 300ms 回源超时失败关闭
TestAuthorizeServiceSingleflight               # 并发未命中只回源一次
TestAuthorizeServiceRejectsSubjectMismatch     # 仓储串主体 → 50300
TestAuthorizeServiceRejectsOversizedSnapshot   # 超限失败关闭，不截断
TestAuthorizeServiceReusesContextSnapshot      # 同请求复用 context 快照
TestSnapshotCacheL2Backfill                    # L2 命中回填 L1
TestSnapshotCacheCorruptValue                  # 脏值删除并视为 miss
TestSnapshotCacheNegativeTTL                   # 空快照 30s 过期
TestSnapshotCacheSoftFailAndDelete             # L2 读降级、写 / 删失败可观测
TestEncodeDecodeRoundTrip                      # schema_version 与字段往返
TestDecodeRejectsCorruptPayload                # 旧 schema / 非法 JSON 一律 ErrCorrupt
TestSnapshotLoadFailureClosed                  # loader 失败 → 503 / 50300
TestSnapshotLoad4xxPassthrough                 # 身份错误不被误报 503
TestAuthorizerWritesSnapshotToContext          # 嵌套 Require 只加载一次
TestAuthorizerShadowKeepsStaticDecision        # shadow 不改变行为，差异 / 错误可观测
TestAuthorizerShadowMatchesStaticWhenEquivalent # 等价配置下差异为 0
TestAuthorizerDecisionObserver                 # allow / deny / unavailable 决策事件
TestAuthorizerOffIgnoresLoader                 # off 不产生快照 I/O
TestAuthorizerRequireAnyEnforce                # 动态 RequireAny
TestParseMode                                  # off|shadow|enforce 白名单
TestCacheAndDecisionMetrics                    # 缓存 / 决策 / 加载指标
TestShadowMetrics                              # shadow 差异与失败指标
```

- 测试命名、表驱动、`t.Run`、`t.Helper()` 遵循 CODE_STYLE §8。
- 禁止无 issue 的 `t.Skip` 掩盖必测场景（TEST-06）。
- 禁止测试连生产 / 共享库（TEST-05）。

**M3 已落地**（`internal/iam/app`、`internal/iam/transport/admin`、`internal/platform`）：

```text
TestCreateRolePersistsPermissionsAndAudits        # 创建成功即写审计，返回聚合与 version
TestCreateRoleRejectsUnknownAndIncompatiblePermissions # 目录外 / 端别不匹配 → 40000
TestCreateRoleValidatesActorAndLimits             # 缺 actor、超限 → 拒绝
TestReplacePermissionsComputesDiffAndInvalidatesSubjects # added/removed + 主动失效主体快照
TestReplacePermissionsRequiresReasonAndScopes     # 数据权限必须声明范围；reason 必填
TestUpdateRoleStatusAndDetails                    # PATCH 局部更新 + 启停
TestCloneRoleCreatesDisabledCopy                  # 克隆默认停用
TestDeleteRoleRequiresNoBindingsAndReason         # 有绑定 / 缺 reason → 40900 / 40000
TestAuditFailureFailsClosed                       # 审计写失败 → 50000 且不落库
TestBindLoadsSnapshotAndIsIdempotent              # 绑定幂等 + 快照可见
TestBindReplacesExpiredBindingAndSkipsQuota       # 过期后重绑刷新原记录且不占额度
TestUpdateRoleValidatesReasonAndInvalidatesSubjects # 空 PATCH / 停用缺 reason → 40000；停用主动失效
TestDeleteRoleIgnoresExpiredBindings              # 仅过期绑定不阻断软删
TestBindRejectsCrossChannelAndDisabledRole        # 跨端别 / 停用角色拒绝
TestBindEnforcesSubjectLimit                      # max_bindings_per_subject 生效
TestBindValidatesExpiry                           # expires_at 必须晚于当前
TestUnbindIsIdempotentAndAudited                  # 解绑幂等 + 审计 + 主体失效
TestUnbindHidesCrossMerchantBinding               # 跨商户对外 40400
TestSimulateAuthorizeUsesSnapshot                 # 模拟鉴权只读、不修改状态
TestPermissionCatalogQueries / TestQueryRoleAndList # 目录树 / 角色详情与绑定数
TestCreateGetAndUpdateRole                        # HTTP：创建 / 详情 / PATCH
TestReplacePermissionsReturnsDiffAndRejectsMismatchedVersion # HTTP：差量 + 40900
TestBindingSubjectPermissionsAndAuthorize         # HTTP：绑定 / 主体快照 / 模拟鉴权
TestDeleteRoleRequiresVersionAndReportsNotFound   # HTTP：version 校验 / 404
TestMissingPrincipalIsUnauthorized                # HTTP：缺 Principal → 40100
TestValidateIAMDefaultsAndRules                   # 默认值、边界、prod/staging、端别互斥
TestValidateIAMProductionRules                    # prod+enforce 必须 redis；prod 禁通配符
TestIAMConfigLogValueRedacts                      # 日志不输出 redis 密码等敏感值
```

**M4.1 已落地**（`internal/iam/transport/{rest,biz}`、`cmd/internal/iamruntime`、配额与计数能力）：

```text
TestBizRouterMountsSelfServiceRoutes            # 13 个 B 端路由全部注册，且不挂 /authorize
TestBizRolesScopedToPrincipalMerchant           # 列表 / 详情只返回本商户，跨商户 40400
TestBizCreateRoleRejectsAdminChannelAndForeignMerchant # body 里伪造 merchant_id / channel=admin → 40000
TestBizRoleQuotaRejectsOverLimit                # max_custom_roles_per_merchant 超限 → 40000
TestBizPermissionCatalogFiltersAdminOnly        # 目录只含 biz/both，A 端专属权限点不可见
TestBizBindingsRequireMerchantStaff             # 主体类型 / 归属校验；跨商户绑定 40400
TestBizHasNoAuthorizeEndpoint                   # 未挂载平台排障端点
TestBizMePermissionsUsesPrincipal               # /me 以 Principal 为准，忽略 query 覆盖
TestListRolesReportsTotalAcrossPages            # Count 与分页解耦（memory + MySQL）
TestRuntimeSharedBetweenAdminAndBiz             # iamruntime.New 复用一个 Authorizer / 仓储 / 缓存实例
```

**M4.2 已落地**（`internal/iam/domain/change_request.go`、memory / MySQL 审批单仓储、`app.ChangeRequestService` + `app.RoleChangeApplicator`、A 端 6 个端点）：

```text
TestNewChangeRequestDefaults                    # pending / version=1 / target_type 由 action 派生
TestNewChangeRequestNormalizesPayloadAndReason  # 空载荷归一为 {}，reason 去空白
TestNewChangeRequestAllowsEmptyTargetForCreateActions # role_create 允许无目标 ID
TestNewChangeRequestValidation                  # 动作、目标、载荷、长度逐项 40000
TestNewChangeRequestRejectsBizTenantOverride    # B 端载荷带 merchant_id → 40300（SEC-14）
TestApproveEnforcesSeparationOfDuties           # 申请人自审 → 40300，状态不变
TestApproveHappyPathAndIdempotency              # 通过后二次审批 → 40900
TestApproveRejectsVersionConflict               # 乐观锁
TestRejectFlow / TestRejectEnforcesSeparationOfDuties
TestCancelOnlyByRequester                       # 仅申请人可撤销
TestMarkAppliedRequiresApproval                 # pending 不可应用；applied 不可重复
TestChangeStatusValidate / TestChangeActionTargetTypes
TestMemoryChangeRequestRepositorySaveAndFind
TestMemoryChangeRequestRepositoryOptimisticLock
TestMemoryChangeRequestRepositoryListFiltersAndOrder
TestMemoryChangeRequestRepositoryReturnsDeepCopies
TestChangeRequestFullFlowAppliesPermissions      # 创建 → 审批 → 应用，角色权限落库且只写一条变更审计
TestChangeRequestApplyRequiresApproval           # 未审批不动目标聚合
TestChangeRequestApplyIsVersionGuarded           # 过期版本 40900
TestChangeRequestApplyFailureKeepsApprovedState  # 应用失败保持 approved，可重试
TestChangeRequestRoleCreateBindsTargetOnApply    # 新建类动作回填 target_id
TestChangeRequestBindingCreateApplies            # 绑定类审批落库
TestChangeRequestRejectsUnknownPayloadFields     # 载荷字段拼错 → 40000（不静默清空）
TestChangeRequestCreateGuardsChannelAndBreakglass # B 端 / breakglass / 缺 reason 的创建拒绝
TestChangeRequestHTTPFlow                        # A 端全链路 HTTP：自审 40300 → 换人审批 → 应用
TestChangeRequestHTTPRejectsMistypedPayload
TestChangeRequestHTTPRequiresVersion
TestBizRouterHasNoChangeRequestEndpoints         # 审批端点不对 B 端暴露
TestSQLChangeRequestRepository                   # MySQL：落库 / 状态流转 / 乐观锁 / 过滤（需 IAM_TEST_MYSQL_DSN）
TestNewBreakglassGrantAdmin / TestNewBreakglassGrantRejectsInvalidInput
TestBreakglassRequestApproveExpandsWildcardSnapshot  # 申请 → 审批 → 绑定 → 快照展开 "*" → 撤销后收回
TestBreakglassWritesAuditTrail                    # request / approve / revoke 三条 rbac.breakglass
TestBreakglassGuards                              # 开关 / 白名单 / 超时长 / 主体类型
TestBreakglassHTTPFlow                            # A 端全链路：自审 40300 → 换人审批 → 撤销
TestBreakglassHTTPRejectsOverlongDuration
TestSQLSnapshotBreakglassWildcard                 # MySQL：绑定期内 "*"，过期后不再展开（需 IAM_TEST_MYSQL_DSN）
TestNewEventValidatesEnvelope / TestRetryPolicy # 载荷校验、指数退避与耗尽判定（pkg/outbox）
TestMemoryStoreLifecycle / TestMemoryStoreReclaimStale # 认领原子性、租约回收
TestRelayDelivers / TestRelayRetriesThenDeadLetters / TestRelayPoisonGoesToDLQ # 投递、退避、死信
TestRelayObserverReportsResults / TestRelayGracefulCancel # 指标出口与优雅退出
TestRouterDispatch / TestRouterUnknownTopicIsPermanent # 路由与未知 topic 进 DLQ
TestIdempotentSkipsCompletedEvent / TestIdempotentReleasesOnFailure / TestMemoryDeduperTakeOverAfterVisibility # 消费幂等
TestOutboxRecorderWritesPendingAuditEvent         # 审计事件写入 async_outbox（topic=iam.audit），重复写入 fail-closed
TestAuditEventHandlerDeliversAuditEvent           # 审计载荷投递到 Emitter
TestAuditEventHandlerRetriesEmitterFailure        # 出口失败可重试（非 poison）
TestAuditEventHandlerPoisonsCorruptPayload / TestAuditEventHandlerPoisonsUnknownEventType # 毒消息进 DLQ
TestAuditEventHandlerIgnoresLegacyCacheInvalidation / TestAuditTopicObserverFiltersOtherTopics # 缓存 topic 空操作、指标只统计 iam.audit
TestOutboxResumeAfterRestart / TestOutboxConcurrentReplicasDeliverOnce # MySQL：重启续投、并发副本只投一次（tests/integration/async）
TestOutboxPermanentFailureWritesDLQ / TestOutboxRetryExhaustedWritesDLQ # MySQL：毒消息 / 重试耗尽进死信
TestValidateAsyncRules / TestValidateAsyncRequiresPersistentDB / TestValidateAsyncProductionPublishInAPI # async 配置边界
TestValidateIAMEnforceRequiresAsync              # prod/staging + enforce 强制 async.enabled
TestWithinUnitOfWorkCommitsAndExposesTx           # 提交一次、ctx 可见 *sql.Tx
TestWithinUnitOfWorkRollsBackAndKeepsCause        # 回滚且保留业务错误链（ErrTxFailed）
TestWithinUnitOfWorkJoinsExistingTx               # 嵌套复用外层事务，不重复 Begin/Commit
TestWithinUnitOfWorkRejectsInvalidInput / TestWithTxNilClearsTx # fail-closed 与显式清空
TestCreateRoleRunsInsideSingleTransaction         # 「角色写入 + 审计」同事务装配
TestCreateRolePropagatesUnitOfWorkFailure         # 事务不可用时不落库、不写审计（50300）
TestBreakglassApprovalReusesOuterTransaction      # 审批 → 应用 → 审计全程单层事务（maxDepth=1）
TestApplyChangeRequestPropagatesCommitFailure     # 应用失败保持 approved，不标记 applied
TestNoopUnitOfWorkPropagatesErrors                # 内存实现语义：无事务、不吞错
TestSQLUnitOfWorkCommitsRoleAndOutboxTogether     # MySQL：角色与 outbox 一次提交（需 IAM_TEST_MYSQL_DSN）
TestSQLUnitOfWorkRollsBackRoleWhenOutboxFails     # MySQL：outbox 冲突 ⇒ 角色与权限明细一并回滚（需 IAM_TEST_MYSQL_DSN）
TestSQLUnitOfWorkNestedApplyReusesTransaction     # MySQL：嵌套写入随外层回滚（需 IAM_TEST_MYSQL_DSN）
```

### 13.3 性能基准

在 `docs/perf/BASELINES.md` 登记：

```text
BenchmarkAuthorize_CacheHit
BenchmarkAuthorize_CacheMiss_DB
BenchmarkSnapshotLoad_100Permissions
BenchmarkReplaceRolePermissions_Diff
```

目标（单实例，参考值，以实测为准）：

| 场景 | P99 |
|------|-----|
| L1 命中鉴权 | ≤ 1ms |
| L2 命中鉴权 | ≤ 3ms |
| DB 回源鉴权 | ≤ 30ms |
| 角色权限替换（200 权限） | ≤ 50ms（不含审计投递） |

### 13.4 工程规范符合性矩阵

| 约束 | 落实方式 | 门禁 |
|------|----------|------|
| SEC-01 / SEC-05 | 配置不含密钥；日志 / 审计脱敏；Token 不入日志 | `make secrets` + Review |
| SEC-03 / SEC-04 | A/B 路由、角色、绑定隔离；`channel` 校验；JWT 域隔离 | `make check-transport` + 单测 |
| SEC-07 | 所有 IAM 管理接口挂 AuthN + AuthZ；仅探针匿名 | Review + 路由测试 |
| SEC-14 | B 端 `merchant_id` 从 Token 取；仓储条件；越权 404 | `TestDataScopeMerchant` + `TestBizRoleTenantIsolation` |
| DATA-01 / DATA-02 | 迁移六位序号 + 配对 down；种子 down 显式失败 | `make check-migrations` |
| DATA-01 / D-06 | 权限目录代码所有；格式 / 唯一性 / 排序；`catalog.go`、`perm.go`、种子迁移、`PERMISSIONS.md` 四处一致 | `go test ./internal/iam/catalog/... -count=1` |
| DATA-01 / C-08 | 每张表有表注释、每列有字段注释；枚举取值列全；注释与 domain 常量一致 | Review + §5.4 注释查询（可选门禁） |
| DATA-03 | 禁止 API/worker 启动自动迁移 | `make check-migrations` |
| DATA-06 | 租户表带 `merchant_id` 与索引；SQL 带租户条件 | Review + 集成测试 |
| AUD-01 / AUD-02 / AUD-04 | 更新 EVENTS / FIELDS；事件字段完整；Emitter 接线 | `make check-audit` |
| AUD-05 / AUD-07 / AUD-08 | A/B 必审；保留 180d；载荷无密钥 | Review + 单测 |
| TEST-01 / TEST-02 | domain 单测 + 覆盖率 ≥ 70% | `make test-ci` |
| TEST-02c | security 覆盖率 ≥ 80% | `make test-ci` |
| TEST-03 | B 隔离 / C 越权基线用例保持 | `check-test-gates.sh` |
| TEST-04 | `-race` | `make test-ci` |
| API-01 / API-03 / API-05 | 分端 OpenAPI；统一信封；分页上限 100 | `make openapi` |
| API-04 | 新码 `50300` 只追加，不改旧码语义；`ErrorCode` 以 `x-extensible-enum` 表达开放码表 | Review + 单测 + `make openapi` |
| API-06 / API-07 / API-09 | 路径前缀；breaking 检测；弃用标记 | `make openapi` |
| API-11 | 批量替换 / 破玻璃强制 `Idempotency-Key` | Handler 测试 |
| REL-01 / REL-03 | 回源 DB 带 300ms 超时；缓存 / DB 调用可取消 | `make check-reliability` |
| REL-04 | 唯一约束 + 幂等键；重复请求结果一致 | `TestBindLoadsSnapshotAndIsIdempotent` + `TestMemoryBindingRepositorySaveIsIdempotent` |
| REL-05 | 超限 `42900` + `Retry-After` | OpenAPI + 单测 |
| REL-06 | 缓存删除失败降级可观测，不静默 | 指标 + 日志 |
| OBS-01 / OBS-05 | 结构化日志 + `trace_id` 贯通 | `make check-observability` |
| OBS-02 / OBS-03 / OBS-04 | 探针 + IAM 依赖检查器 + `/version` | `make check-observability` |
| OBS-06 / OBS-07 | 4xx 不打 Error；关键操作审计 | Review + `check-audit` |
| CFG-01 / CFG-02 / CFG-05 | example 更新；不提交真实 yaml；Validate 覆盖 prod 互斥 | `make check-config` |
| CFG-03 / CFG-07 | 启动即校验；无魔法数 | 单测 + Review |
| CI-03～CI-09 | fmt / lint / test / vuln / tidy / build / transport | `make ci` |
| C-01～C-10 | 包注释、导出 godoc、`nolint` 原因、忽略 error 原因 | `make lint` + Review |
| S-01～S-12 | gofmt/goimports、签名、context key、错误早返回、接口消费侧 | `make lint` + Review |
| DEP-01～DEP-08 | 不新增策略引擎依赖；新增依赖须说明用途 / 许可证 | `make vuln` + `make check-deps` |
| LIC-01～LIC-07 | 不引入禁止许可证 | `make check-license` |
| GEN-01～GEN-07 | mockery 生成物在 `internal/iam/mocks/`，禁止手改 | `make check-codegen` |
| PERF-01 / PERF-02 | 基准登记与抽检 | `make check-perf` |
| RELS-01～RELS-08 | 无新镜像；沿用现有发布流程 | `make check-release` |
| RB-01～RB-07 | 新增 `docs/runbooks/iam.md` | `make check-runbooks` |
| GIT-01～GIT-08 | 分支 / commit / PR 模板 | `make check-git` |

### 13.5 权限目录一致性门禁（已决策：Go 测试）

**唯一真相**：`internal/iam/catalog/catalog.go` 的 `entries`。其余三处是它的投影：

| 位置 | 作用 | 漂移后果 |
|------|------|----------|
| `internal/iam/catalog/catalog.go` | 唯一真相（码、名称、端别、形态、数据范围标记、排序） | — |
| `internal/iam/perm.go` | 业务代码引用的 `Perm*` 常量 | 编译失败或引用错误权限码 |
| `migrations/*_seed_*.up.sql` | 入库目录（启动期校验、菜单元数据；追加式，禁止改写已应用文件） | 运行时缺权限点 / 元数据不一致 |
| `docs/iam/PERMISSIONS.md` | 人读目录与变更流程 | Review 依据失效 |

**为什么不用 shell**：Go 测试可直接复用 `catalog` / `iam` 的类型，用 `go/ast` 精确解析常量声明，输出结构化差异；避免 shell 在 Windows 下的引号、换行与中文编码问题，也避免为同一规则维护两套解析逻辑。`check-migrations.sh` 只保留命名 / 配对 / 禁止自动迁移，不解析内容。

**用例与职责**：

| 用例 | 断言 |
|------|------|
| `TestPermissionCatalog` | 逐条格式（2～4 段、全小写、无 `*`、长度 ≤ 128）、枚举合法、`sort` 严格递增、父节点存在 |
| `TestPermissionCatalogMatchesConstants` | `go/ast` 解析 `perm.go`；目录 ↔ `Perm*` 常量**双向**一致，常量值必须是字符串字面量 |
| `TestPermissionCatalogMatchesSeedMigration` | 逐行解析种子；`code / name / channel / resource / action / kind / data_bearing / sort` 与目录一致，无重复、无遗漏 |
| `TestPermissionCatalogMatchesDoc` | 解析 `PERMISSIONS.md` 目录表；`code / channel / kind / data_bearing` 与目录一致 |
| `TestPermissionCatalogDownMigration` | down 覆盖全部种子 `code`（防止新增权限点后无法回滚） |
| `TestPermissionCatalogSeedIsIdempotent` | 种子含 `ON DUPLICATE KEY UPDATE`，可重放 |

**解析约定（改格式必须同步测试）**：

1. 种子迁移中每条权限点**独占一行**，行首为 `('`，列顺序与 `INSERT` 列表一致；
2. `PERMISSIONS.md` 的目录表列顺序固定为 `code | channel | kind | data_bearing | 说明`，`code` 用反引号包裹；
3. 新增权限点只允许追加，`sort` 取当前最大值 + 10。

**缓存陷阱**：Go 测试缓存不跟踪测试运行时读取的外部文件（种子 SQL、Markdown）。CI 的 `make test-ci` 已固定 `-count=1`；本地单独验证目录时也必须加 `-count=1`，否则可能命中旧缓存。

**失败输出要求**：错误信息必须包含「文件:行号、权限码、期望值 / 实际值、修复指引」，禁止只报 `assertion failed`。

> **已决策**：不新增 `scripts/ci/check-iam-permissions.sh`。权限目录一致性由 `internal/iam/catalog` 的 Go 测试承担，已纳入 `make test-ci` → `make ci`；若未来目录规模超过 ~200 条再评估「生成种子 / 文档」，届时仍以 Go 测试校验生成物最新（GEN-01/02）。

### 13.6 SQL 仓储集成测试（已决策：环境变量 DSN + CI service）

**位置**：`tests/integration/iam/mysql_repository_test.go`（TEST-07 要求集成测试集中放在 `tests/integration/`）。

**开关**：`IAM_TEST_MYSQL_DSN`。为空时用例显式 `t.Skipf` 并打印原因，**不静默通过**；DSN 必须包含 `parseTime=true&loc=UTC`，否则测试直接失败并提示（DATETIME(3) 依赖该参数扫描为 `time.Time`）。

| 环境 | 提供方式 |
|------|----------|
| GitHub Actions `test` job | `services.mysql`（`mysql:8.0`，health check 就绪后运行）+ job 级 `IAM_TEST_MYSQL_DSN` |
| Gitee Go | 平台不支持等同的 service 声明；在控制台配置 `IAM_TEST_MYSQL_DSN` 即启用，未配置时显式跳过 |
| 本地 | `docker run --rm -d -p 3306:3306 -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=iam_test mysql:8.0`，再导出 DSN |

**启动流程**：`openAndMigrate` 用 `pkg/migrate` 对 `migrations/` 全量执行 up（含 000001～000005 模块基线），确保测试 schema 与生产迁移完全一致；每个用例前清空 `async_consumed → async_dlq → async_outbox → iam_change_request → iam_role_binding → iam_role_data_scope → iam_role_permission → iam_role`（按外键 RESTRICT 逆序），并保留种子权限点。

**覆盖点**（与 §6.6 端口语义逐条对应）：

| 用例 | 断言 |
|------|------|
| `TestSQLRoleRepositoryRoundTrip` | 角色 + 权限 + `merchant_set` JSON 往返；`FindByCode` 按端别 / 商户命中 |
| `TestSQLRoleRepositoryUniqueCode` | `uk_iam_role_scope_code` → 40900；B 端同名 code 允许 |
| `TestSQLRoleRepositoryOptimisticLock` | `WHERE version=?` 冲突；不存在返回 40400 |
| `TestSQLRoleRepositoryImmutableFields` | `code / channel / merchant` 不可变 |
| `TestSQLRoleRepositoryListAndSoftDelete` | 软删角色不入列表、仍可按 code 查到、code 不可复用 |
| `TestSQLBindingRepositoryLifecycle` | 重复绑定刷新有效期 / 授权人且不新增行；过期过滤；解绑幂等 |
| `TestSQLBindingRepositoryTenantIsolation` | B 端空商户拒绝；跨商户查询为空 |
| `TestSQLSnapshotRepository` | 权限并集、数据范围合并、停用 / 过期绑定不进入快照、版本取最大角色版本 |
| `TestSQLRepositoriesRejectUnconfigured` | 未注入 `*sql.DB` 返回 50300，不 panic |
| `TestSQLUnitOfWorkCommitsRoleAndOutboxTogether` | 同一事务内角色主表 / 权限明细与 `async_outbox` 一次提交 |
| `TestSQLUnitOfWorkRollsBackRoleWhenOutboxFails` | outbox 唯一键冲突 → 角色与权限明细 0 残留、种子事件未被改动 |
| `TestSQLUnitOfWorkNestedApplyReusesTransaction` | 嵌套 `WithinTx` 复用外层事务，外层回滚后嵌套写入消失 |

**为什么不用 testcontainers-go**：现有依赖已含 `go-sql-driver/mysql`，CI service 容器与 testcontainer 同样是 ephemeral（TEST-05 / CI-12），且避免新增依赖与 DEP 评审、避免 CI 依赖 docker daemon。

### 13.7 生产加固演练（M5）

**目标**：把 `docs/runbooks/iam.md` 里承诺的降级行为固化为可执行测试与登记在案的实测数据，避免「文档说降级、代码其实失败关闭」这类漂移。

**故障演练矩阵**（`internal/iam/app/fault_injection_test.go`，随 `make test-ci` 运行）：

| 演练场景 | 用例 | 期望行为 | 观测手段 |
|----------|------|----------|----------|
| L2（Redis）整体不可用（读 / 写 / 删全部报错） | `TestAuthorizeServiceDegradesWhenL2Unavailable` | 读错误视为 miss 并回源 DB，鉴权成功；写失败只记 `cache_write`，请求不失败；后续请求由 L1 命中，不放大 DB 压力 | `iam_authz_cache_total{layer="l2",result="error"}`、`iam_authz_load_errors_total{stage="cache_write"}` |
| 管理写入后缓存删除失败 | `TestRoleServiceWriteSurvivesInvalidationFailure` | 权限变更照常提交并返回差量；失效失败只告警，由 L1 3s / L2 5m TTL 兜底 | `iam_cache_invalidate_failed_total{layer}` → 告警 `iam.cache.invalidate_failed` |
| 审计出口（Outbox sink）抖动 | `TestOutboxRelayDrainsBacklogAfterTransientFailure` | 失败一轮后 `iam_outbox_pending` 仍为 1（可告警）；退避到期后自动追平并归零 | `iam_outbox_pending` / `iam_outbox_lag_seconds` / `iam_outbox_delivery_total{result="retry\|success"}` |
| 回源 DB 超时 / 故障 | `TestAuthorizeServiceTimeout`、`TestAuthorizeServiceRepositoryFailureIsClosed` | 300ms 超时后失败关闭（`50300`），绝不回退到令牌内静态角色 | `iam_authz_load_errors_total{stage="db"}` → 告警 `iam.authz.unavailable` |
| 缓存脏值 / 反序列化失败 | `TestSnapshotCacheCorruptValue` | 按 miss 处理并删除坏 key，不放行、不 panic | `result="error"` |
| 死信与毒消息 | `TestRelayRetriesThenDeadLetters`、`TestRelayPoisonGoesToDLQ` | 重试耗尽进 `dead`；毒消息直接 `failed` 不重试 | `result="dead\|poison"` + `iam_outbox_pending` |
| shadow 与静态表判定不一致 | `TestAuthorizerShadowKeepsStaticDecision` | shadow 只记录差异、不改变放行结果 | `iam_shadow_mismatch_total{static_allow,iam_allow}` |

**实测基准**（`make check-perf`，本机 i5-12450H，`-benchtime=50ms`，仅作数量级参考）：

| 基准 | 实测 | 目标 |
|------|------|------|
| `BenchmarkAuthorize_CacheHit`（L1 命中） | ~5.8µs/op | ≤ 1ms |
| `BenchmarkAuthorize_CacheMiss_DB`（回源 + 回填） | ~6.6µs/op（fake repo，不含真实 SQL） | ≤ 30ms（真实 SQL 见下） |
| `BenchmarkSnapshotLoad_100Permissions` | ~19.7µs/op | ≤ 1ms |
| `BenchmarkReplaceRolePermissions_Diff`（200 权限全量替换） | ~39µs/op | ≤ 50ms |

> 真实 SQL 回源的 P99 依赖索引与网络，必须在 staging 用压测确认（`iam_role_binding` 命中 `idx_iam_binding_subject`，`iam_role` 命中 `uk_iam_role_scope_code`）；本地微基准只证明「应用层开销可忽略」。
>
> **2026-10-22 本地替代验证**（Docker MySQL 8.0、回环网络，1000 角色 / 1 万主体 / 10 万绑定，热点主体 10 角色 × 10 权限）：
> 串行回源 P50 2.7ms / P95 4.7ms / P99 5.6ms；4 / 8 / 16 / 32 并发时 P99 分别为 8.4 / 9.4 / 20.6 / 39.2ms，
> 16 并发约 1.7k rps 且无连接池等待，32 并发开始超过 30ms 目标。`EXPLAIN` 显示绑定表按 `uk_iam_binding`
> 前缀命中（rows=10）、`iam_role` 走主键，未出现 N+1 或全表扫描。Redis 停机演练：错误在 69～80ms 内返回，
> 300ms 读超时内可降级回源 DB（配合 `TestAuthorizeServiceDegradesWhenL2Unavailable`）；容器已恢复。
> 上述结果仅代表本机回环环境，staging 仍需按真实网络、数据分布与并发模型复测。

**容量评估**（初始规模：1 万角色 / 10 万绑定 / 1 万主体）：

| 项 | 估算 | 说明 |
|----|------|------|
| 快照回源 SQL 次数 | 每主体 1 次（L1/L2 未命中时） | 稳态命中率 ≥ 99%；Redis 故障时回落到 DB，需按 DB 连接池上限评估 |
| 单实例内存（L1） | ≤ `l1_max_entries × 平均快照大小`；默认 4096 条 × ~2KB ≈ 8MB | 快照含权限码与数据范围，超限由 `MaxEntries` 淘汰 |
| 撤销生效窗口 | 最坏 `max(L1TTL 3s, invalidate 成功时≈0)` | 失效失败时退化为 3s / 5m（L2） |
| 主从延迟风险 | 写入后立即读快照可能读到旧版本 | 失效在写事务提交后执行；跨实例依赖 TTL 兜底 |
| 连接池 | `max_open ≤ 3 × 实例数`，回源超时 300ms | 回源超时保证故障时不长期占用连接 |

**清理演示表**：本仓库未引入 IAM 演示 / 影子对照表（shadow 使用 `cmd` 内现有静态 `rbac.Checker`，不落库），因此 M5 无表需要清理。`off` 静态表在 `enforce` 稳定观察 1 个发布周期后随版本删除（§15.1）。

**M5 待环境验证项**（代码与本地门禁已就绪，需 staging 执行）：

1. `iam.mode=shadow` 连续观察 ≥ 24h：`iam_shadow_mismatch_total` 必须为 0 或逐条解释后归零；
2. `iam.mode=enforce` 灰度：鉴权 P99、`50300` 比率、`/readyz` 在 Redis 重启窗口的表现；
3. 真实 SQL 回源压测：命中率、P99、连接池水位。（2026-10-22 本地 Docker MySQL 已完成替代压测：串行 P99 5.6ms、16 并发 P99 20.6ms、32 并发 P99 39.2ms；staging 仍需按真实网络与数据分布复测）
4. Outbox 积压演练：停 worker / Relay → `iam_outbox_pending` 告警触发 → 恢复后自动追平；
5. 发布 Checklist（§16）逐项签字。

---

---

## 十四、里程碑与交付

> 工作量为人日粗估，按 1～2 名后端 + Review 计算；不含界面工作量。

| 阶段 | 交付 | 验收 | 回滚 |
|------|------|------|------|
| **M0 设计**（1～2d） | ADR、本文档、权限目录草案、OpenAPI 草案、威胁模型 | 架构 / 安全 Review 通过；目录无通配符（**ADR-0004 已接受，文档与 ADR 已完成；目录 / OpenAPI / 威胁模型评审待收口**） | 不涉及运行时代码 |
| **M1 领域与存储**（3～5d） | `internal/iam/domain`、memory + MySQL 仓储、迁移 000002/000003、`internal/iam/catalog` 目录门禁、domain 单测、SQL 集成测试 | domain 覆盖率 ≥ 70%（实测 90.8%）；memory 仓储 94.7%；迁移 up/down 可执行（MySQL 8.0 实测）；目录四处一致测试通过；租户条件 / 唯一约束 / 乐观锁 / 快照 SQL 集成测试通过（**已完成：domain + memory + MySQL 仓储 + CI service**） | `migrate down`（仅非生产） |
| **M2 快照与缓存**（3～5d） | `SnapshotLoader`、L1/L2 缓存、`security` 适配、`iam.mode=shadow`、指标 | shadow 与演示表判定一致率 100%；缓存命中 / 失效测试通过；P99 达标（**已完成：契约 + 缓存 + 服务 + 三态适配 + 指标 + 单测；覆盖率 app 76.6% / cache 77.8% / security 94.1%（按当前全量代码实测）；配置与 cmd 注入随 M3**） | 切回 `off` |
| **M3 A 端管理 API**（5～7d） | A 端角色 / 权限 / 绑定 API、审计接线、OpenAPI、配置装配 | A 端 15 个端点全链路可用；审计事件 `rbac.role_change` / `rbac.binding_change` 已 `active` 且 fail-closed（当前目标为 SlogEmitter，持久化 Outbox 待 M4）；`admin.yaml` 已含 IAM 契约；`go test ./...`、layering / test-gates / transport / audit / migrations / observability / config / openapi / perf / deps / licenses、`golangci-lint` 全绿（**已完成**）；staging `enforce` 稳定性待环境验证 | 切回 `off` + 清缓存 |
| **M4 B 端与治理**（5～7d） | B 端自助、审批单、SoD、破玻璃、访问复核导出 | B 跨商户 404、配额生效、目录按端过滤、`/authorize` 不挂载（**M4.1 已完成：13 个端点 + `iamruntime` 统一装配 + OpenAPI + 单测**）；审批单聚合 + SoD + memory / MySQL 仓储 + 应用服务 + apply 接线 + A 端 6 个端点（**M4.2 已完成：含 HTTP 全链路与 SoD 测试**）；破玻璃限时 + 告警（**M4.3 已完成**）、Outbox 持久化（**M4.4 已完成：`iam_outbox` + memory/MySQL 仓储 + 发布器 + 退避/死信 + 三项指标 + prod enforce 强制启用**）、Outbox 单事务写入（**M4.5 已完成：`UnitOfWork` + context 传递 `*sql.Tx`，业务写入与审计同提交，嵌套复用**）、B 端审批（**M4.6 已完成：审批单 `merchant_id` 与 B 端权限点并入 000002 / 000003 基线、`ChangeScope` 租户隔离、B 端 6 个端点、跨商户 404**）、高风险直写强制转审批（**M4.7 已完成：`iam.approval.enabled` + 7 个直写端点返回 40900、审批单 apply 不受影响、仅 A/B、prod 要求持久化 db**）；e2e 通过 | 关闭审批 / 破玻璃开关 |
| **M5 生产加固**（3～5d） | 压测、故障演练、runbook、告警、容量评估、清理演示表 | SLO 达标；故障演练通过；`make ci` + 发布 Checklist 全绿（**M5 已完成代码侧：§13.7 可执行故障演练用例、`BenchmarkReplaceRolePermissions_Diff` 登记、容量评估、演示表清理确认；staging shadow 24h / enforce 灰度 / 真实 SQL 压测 / Outbox 积压演练待环境执行**） | 保留 `off` 开关一个版本 |

**依赖顺序**：M1 完成前不合并 M2 的运行时切换；M2 shadow 数据必须来自真实（脱敏）角色配置；M3 enforce 前必须完成审计接线。

---

## 十五、发布与回滚

### 15.1 三态切换

| 模式 | AuthZ 来源 | 适用环境 | 行为 |
|------|------------|----------|------|
| `off` | 现有 `cmd` 静态角色表 | local / 生产应急回滚 | 行为与当前一致；IAM 只做目录校验；生产切 `off` 必须告警 + `config.change` 审计 |
| `shadow` | 计算 IAM 快照，**仍按静态表放行** | staging / 迁移验证 | 记录 `iam_shadow_mismatch_total`；不影响请求 |
| `enforce` | IAM 快照 | staging / prod | 以 IAM 为唯一真相；静态表仅保留回滚 |

- `shadow` 差异指标：`iam_shadow_mismatch_total{channel,static_allow,iam_allow}`；差异必须为 0 或逐条解释后才能 `enforce`。
- `enforce` 后至少观察 1 个发布周期再删除静态表；`off` 开关保留一个版本作为应急回滚位。

### 15.2 上线步骤

1. 合入 M1：迁移 + 权限目录（`catalog` / `perm` / 种子 / `PERMISSIONS.md`）+ 领域代码（不改变鉴权行为）。
2. 执行 `make migrate-up`（staging），校验目录种子与索引。
3. 发布 `iam.mode=off`；确认启动校验、探针、指标正常。
4. 切 `shadow`；导入真实角色 / 绑定（脱敏）；比对差异。
5. 修复差异；切 `enforce`（staging）并启用 `async.enabled=true`（配置校验要求 `db.driver` 持久化 + Redis 缓存，`iam.mode=enforce` 强制异步基础设施开启）；跑 e2e + 压测。
6. 生产按同样顺序；先 A 端后 B 端，或按租户灰度（`iam.mode=enforce` + 商户白名单）。
7. 观察 7 天；清理静态表与临时开关（保留 `off` 一个版本）。

### 15.3 回滚

| 场景 | 动作 | 数据影响 |
|------|------|----------|
| 鉴权误判 / 大面积 403 | 切 `iam.mode=off`；清 L1/L2 缓存 | 无 schema 变更 |
| 缓存一致性异常 | 清 Redis `iam:*`；临时缩短 `l1_ttl` / `l2_ttl` | 无 |
| 迁移失败 | 停止发布；按 `.down.sql` 回滚（非生产）或前向修复（生产） | 按迁移 |
| 审计 Outbox 积压 | 先修审计出口，事件按退避自动补投；必要时临时 `async.enabled=false` 回落同步 Emitter 并告警，恢复后重新开启 | 审计可能延迟，不可丢失；禁止手工改事件状态 |
| 破玻璃滥用 | 立即撤销临时绑定；清缓存；安全事件流程 | 审计留痕 |

> 生产**禁止**直接 `UPDATE` / `DELETE` IAM 表作为常规回滚手段；应急操作须双人 + 工单 + 审计（DATA-04）。

---

## 十六、风险与开放问题

### 16.1 风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| 鉴权依赖故障导致全站 503 | A/B 不可用 | 两级缓存、DB 超时、容量冗余、SLO 告警、演练 |
| 权限模型过宽（`all` / `*`） | 数据泄露 | 默认最小权限；API 拒绝通配符；季度复核；破玻璃审批 |
| 缓存陈旧导致撤销延迟 | 安全事故窗口 | L1 3s + 主动删 L2；删除失败告警；破玻璃撤销强制清缓存 |
| 角色 / 绑定规模失控 | 查询慢、快照大 | 限额 + 分页 + 索引 + 快照大小指标 |
| 目录与代码漂移 | 授权失败 / 越权 | 启动校验 + 目录测试 + 种子评审 |
| 审批流拖慢运营 | 业务抱怨 | 仅高风险操作强制审批；普通角色变更直接生效 + 审计 |
| B 端商户自定义角色滥用 | 越权 / 数据泄露 | 目录 channel 限制；禁止 IAM 管理权限；租户隔离用例 |
| 审计存储未就绪 | 合规缺口 | M3 前完成 Outbox + 审计存储方案；否则不得上生产 |
| 迁移与代码不兼容 | 发布失败 | expand/contract；shadow；回滚开关 |

### 16.2 开放问题（Owner 决策）

| 问题 | 建议默认 | 决策方式 |
|------|----------|----------|
| 是否需要 deny 规则？ | v1 不需要；用「不授予」表达 | ADR |
| 是否需要角色继承？ | v1 不需要；多绑定组合 | ADR |
| 是否引入 ABAC（时间 / IP / 资源属性）？ | v1 不引入；避免策略引擎依赖 | ADR |
| 审批流自建还是接 BPM？ | v1 自建最小审批单 | 产品 + 架构 |
| 审计存储用 DB 还是 SIEM？ | 按组织现状；本方案只保证 Outbox 可靠投递 | 安全 Owner |
| B 端自定义角色上限？ | 50 / 商户，可配置 | 产品 |
| 是否引入读副本承载快照回源？ | 先主库 + 缓存；QPS 超阈值再评估 | SRE |
| 是否把会话撤销纳入 IAM？ | 不纳入；由认证侧负责；IAM 只保证权限撤销 | 架构 |
| 权限目录由谁维护？ | 各域 Owner 提 PR，安全 / 架构 Review | 治理 |
| `ErrorCode` 使用开放枚举后，客户端未知码如何处理？ | 统一按「未知错误」兜底并上报，不改变已实现分支 | 前后端约定 |

---

## 十七、验收清单（Definition of Done）

### 17.1 设计阶段

- [x] ADR 已提交并接受（ADR-0004：落位、失败关闭、`50300`、灰度路线）。
- [x] 权限目录基线已落地（21 条，无通配符）；广泛一致性由 Go 测试强制，扩展条目仍需评审。
- [x] 数据模型 / 索引 / 迁移顺序已确认（MySQL 8.0.19 单基线，`000002` / `000003` 未被占用）。
- [ ] OpenAPI 草案评审通过，A/B 分端无混挂。（契约已补齐，`make openapi` 通过；评审随 PR 完成）
- [ ] 威胁模型与审计字段评审通过。

### 17.2 实现阶段

- [x] `internal/iam` 分层符合 ARCHITECTURE §五 / §六；domain 不 import app / transport / infrastructure / platform / database/sql（`make check-layering` 通过）。
- [x] 领域不变量由聚合强制（端别 / 作用域 / 通配符 / 乐观锁 / 破玻璃时效 / 数据范围并集），domain 覆盖率 90.8%。
- [x] memory 仓储对齐端口语义（NotFound / Conflict / 不可变字段 / 幂等绑定 / 过期过滤 / 租户隔离 / 深拷贝），覆盖率 94.7%。
- [x] MySQL 仓储对齐同一语义，`tests/integration/iam` 在 MySQL 8.0 上通过；CI 由 `mysql:8.0` service 注入 DSN（§13.6）。
- [x] `pkg/rbac` 未被业务 / DB 逻辑污染（IAM 改动未触碰该包）。
- [x] 所有导出符号有 godoc，包有注释（C-01～C-10）。（revive `exported` / `package-comments` 已设为 error 级，`golangci-lint run ./...` 0 issue）
- [ ] 错误处理、命名、context、并发符合 S-01～S-12。（`golangci-lint` 0 issue，人工 Review 仍待进行）
- [x] 权限目录四处一致（`catalog.go` / `perm.go` / 种子 / `PERMISSIONS.md`）由 `internal/iam/catalog` 的 Go 测试强制，`-count=1` 通过。
- [x] 迁移 `000001`～`000005` up/down 配对，`make check-migrations` 通过；基线按模块压平（identity / IAM / async），`up` 不再引用已删除的历史增量文件。
- [x] 所有 IAM 表有表注释、所有字段有字段注释，枚举取值列全且与 domain 常量一致；写法统一为 MySQL 列内 `COMMENT`（`000002_create_iam_tables.up.sql`）。
- [x] B 端租户条件在 SQL 与 app 双重落实；越权 404（`TestSQLBindingRepositoryTenantIsolation` / `TestUnbindHidesCrossMerchantBinding`）；B 端 HTTP 自助接口（19 个端点，含 6 个审批端点）已落地，跨商户 404、配额 40000、目录按端过滤均有传输层测试（`TestBiz*` / `TestBizChangeRequestTenantIsolation`）。
- [x] 审批单租户隔离（M4.6）：`000002` 内置 `merchant_id` + 复合索引、`domain.validateChangeTenant` 强制端别 / 商户不变量、`ChangeScope` 由 transport 从 Principal 派生、B 端路由与破玻璃路由分离；SQL / memory 仓储与 HTTP 用例均覆盖跨商户不可见。
- [x] 高风险直写强制转审批（M4.7）：`iam.approval.enabled` 开启后 7 个角色 / 绑定直写端点统一返回 `40900` 并提示 `/change-requests`，拦截发生在用例之前；审批单 `apply` 走用例内部调用不受影响；C 端 / worker 配置被拒绝；prod 要求持久化 `db.driver`（`TestApprovalRequiredRejectsDirectWrites` / `TestValidateIAMApprovalRules`）。
- [x] 审计事件 `rbac.role_change` / `rbac.binding_change` 已登记、置 `active` 并接线（`ChangeRecorder`，审计失败 fail-closed）；`rbac.breakglass` 已在 M4.3 接线；持久化 Outbox 已在 ENT-03 T8 完整迁移到平台 `async_outbox`（`pkg/outbox` Relay + 死信重放 + 积压指标）。
- [x] A 端管理 API 15 个端点、`RoleService` / `BindingService` / `QueryService` 单测与 HTTP 传输测试通过；OpenAPI（`api/openapi/admin.yaml`）已补齐 IAM 契约。
- [x] 日志 / 指标 / Trace 符合 OBS：结构化日志含 `trace_id`；指标低基数（无 `subject_id` / `role_id`）；变更计数由审计装饰器产生；`/readyz` 注册 `iam_db` / `iam_cache`；请求级 span 由 `tracing.HTTPMiddleware` 统一产生。
- [x] 配置 example（`iam` / `redis` 段）、`Validate`（`config_iam_test.go`）与环境变量文档同步。
- [x] 告警与 runbook：`docs/runbooks/iam.md` + `ALERTS.md` 5 条 IAM 告警（含 `iam.outbox.backlog`），`make check-runbooks` 通过。
- [x] `make ci` 全绿：fmt / tidy / lint / test-ci / vuln / deps / transport / layering / debug / secrets / config / migrations / reliability / observability / git / release / runbooks / audit / license / codegen / perf / openapi / build。（2026-10-22 本地逐项执行等价命令全部通过：fmt-check / tidy-check / lint / test-ci / vuln / secrets / 其余 16 项门禁 / build；正式流水线结果随 PR 校验）

### 17.3 生产就绪

- [ ] `iam.mode=enforce` 在 staging 稳定 ≥ 1 周。
- [ ] 压测达标：缓存命中 P99 ≤ 5ms，回源 P99 ≤ 30ms；无连接池耗尽。（2026-10-22 本地 MySQL 8.0 实测：回源 P99 5.6ms@串行、20.6ms@16 并发、39.2ms@32 并发；前两档达标，32 并发超目标，staging 未执行）
- [ ] 故障演练：Redis 故障、DB 超时、缓存删除失败、Outbox 积压均有预期行为。（代码侧用例已全部通过，见 §13.7；staging 演练待执行）
- [ ] 告警与 runbook 上线；破玻璃演练完成。
- [ ] 备份 / 恢复覆盖 IAM 表；恢复演练通过。（备份范围与恢复步骤见 `docs/runbooks/iam.md`「备份与恢复」；恢复演练尚未执行）
- [ ] 访问复核流程与季度计划已建立。（流程与季度节拍已写入 `docs/runbooks/iam.md`「季度访问复核」；首次执行与 Owner 排期待确认）
- [ ] 回滚演练：`enforce → off` 可在 1 次配置变更内完成。
- [ ] 安全 Review、架构 Review、SRE Review 全部通过。

---

## 附录 A：权限点目录基线

> 可读主表见 [`docs/iam/PERMISSIONS.md`](./iam/PERMISSIONS.md)；机器可读的唯一真相为 `internal/iam/catalog/catalog.go`，并由 Go 测试强制与 `internal/iam/perm.go`、全部 `migrations/*_seed_*.up.sql` 及该文档一致（§13.5）。本附录仅作设计摘要，**冲突时以 `PERMISSIONS.md` 为准**。`channel` 为 `admin` 表示仅 A 端可授予，`biz` 表示 B 端仅商户可授予，`both` 表示两端均可。

| code | channel | kind | data_bearing | 说明 |
|------|---------|------|--------------|------|
| `iam:permission:read` | admin | api | 否 | 查看权限目录 |
| `iam:role:read` | both | api | 否 | 查看角色 |
| `iam:role:write` | both | api | 否 | 创建 / 更新 / 停用 / 删除角色 |
| `iam:role:grant` | both | api | 否 | 替换角色权限点 |
| `iam:binding:read` | both | api | 否 | 查看绑定 |
| `iam:binding:write` | both | api | 否 | 绑定 / 解绑 |
| `iam:check` | admin | api | 否 | 模拟鉴权 |
| `iam:change:read` | admin | api | 否 | 查看审批单 |
| `iam:change:approve` | admin | api | 否 | 审批变更 |
| `iam:change:request` | biz | api | 否 | 发起本商户变更申请 / 查看本商户待办 |
| `iam:change:review` | biz | api | 否 | 审批本商户变更（申请人不得自审） |
| `iam:breakglass:request` | admin | api | 否 | 申请破玻璃 |
| `iam:breakglass:approve` | admin | api | 否 | 审批破玻璃 |
| `order:read` | both | api | 是 | 已弃用；保留既有授权，禁止新授 |
| `order:write` | both | api | 是 | 已弃用；保留既有授权，禁止新授 |
| `file:read` | both | api | 是 | 已弃用；保留既有授权，禁止新授 |
| `file:write` | both | api | 是 | 已弃用；保留既有授权，禁止新授 |
| `audit:read` | admin | api | 是 | 查询审计 |
| `data:export` | both | api | 是 | 导出数据 |
| `config:read` | admin | api | 否 | 查看配置 |
| `config:write` | admin | api | 否 | 修改配置 |

**系统角色种子（示例，非最终）**：

| channel | code | 权限 | 数据范围 |
|---------|------|------|----------|
| admin | `system_admin` | 逐项授予 IAM 管理权限 + `audit:read` + `config:read/write` | `all` |
| admin | `ops` | `iam:permission:read`、`iam:role:read`、`iam:binding:read`、`iam:change:read`、`audit:read`、`config:read` | `all`（或 `merchant_set`） |
| admin | `breakglass_admin`（`role_type=breakglass`） | 目录中无权限点；仅在有效审批绑定时展开 `*` | `all` |
| biz | `merchant_owner` | `iam:role:read` / `write` / `grant`、`iam:binding:read` / `write`、`iam:change:request` / `review` | `merchant` |
| biz | `merchant_staff` | `iam:change:request` | `merchant` |

> B 端审批 SoD：`merchant_staff` 只能提申请；`merchant_owner` 虽同时持有申请与审批权限点，仍受「申请人 ≠ 审批人」约束，同一主体无法自审。

> `system_admin` 不含 `iam:breakglass:approve`，以满足「申请 ≠ 审批」；破玻璃审批人单独配置。系统角色在种子中逐项授予权限，**不写 `*`**；`*` 只由 `breakglass` 角色在有效审批绑定时动态展开。

---

## 附录 B：配置示例（生产最小段）

```yaml
env: prod
channel: admin

iam:
  mode: enforce
  load_timeout: 300ms
  cache:
    driver: redis
    l1_ttl: 3s
    l2_ttl: 5m
    negative_ttl: 30s
    key_prefix: iam
  limits:
    max_roles_per_subject: 20
    max_permissions_per_role: 200
    max_bindings_per_subject: 50
    max_custom_roles_per_merchant: 50
  security:
    allow_wildcard: false
    require_reason: true
    sod_enabled: true
    sod_conflicts:
      - ["iam:role:grant", "audit:read"]
      - ["iam:breakglass:request", "iam:breakglass:approve"]
  approval:
    enabled: false       # true 时角色 / 绑定直写端点返回 40900，必须走 /change-requests
  breakglass:
    enabled: false        # 生产默认关闭；启用要求 mode=enforce，prod 必填 allowed_subjects
    allowed_subjects: []  # 允许申请的主体白名单；非空时名单外主体返回 40300
    # 时长上限硬编码 2h（domain.MaxBreakglassDuration），不提供配置项，避免误改放大权限

  outbox:
    enabled: false        # 审计事件先持久化再投递；prod/staging + enforce 必须 true
    poll_interval: 1s     # 拉取间隔，[100ms, 1m]
    batch_size: 50        # 单轮拉取上限，[1, 500]
    max_attempts: 8       # 投递次数上限，耗尽进死信，[1, 20]
```

---

## 附录 C：错误码与 HTTP 映射

| code | HTTP | 语义 | IAM 使用场景 |
|------|------|------|--------------|
| 0 | 200 | 成功 | 所有成功响应 |
| 40000 | 400 | 参数非法 | 权限码格式、通配符、范围非法、版本字段缺失 |
| 40100 | 401 | 未认证 | Token 缺失 / 无效 / 渠道不符 |
| 40300 | 403 | 无权限 | 缺少权限点、跨端调用 |
| 40400 | 404 | 不存在 | 角色 / 绑定不存在；B 端越权统一表现 |
| 40900 | 409 | 冲突 | code 重复、版本冲突、角色有绑定、SoD 冲突、幂等载荷不一致 |
| 42900 | 429 | 限流 / 配额 | 角色 / 绑定超限、管理接口限流 |
| 50000 | 500 | 内部错误 | DB / 审计 / Outbox 写失败 |
| **50300** | 503 | 依赖不可用 | 快照回源 DB + 缓存均不可用；失败关闭 |

---

## 附录 D：参考

- [`adr/0004-rbac-iam-domain.md`](./adr/0004-rbac-iam-domain.md)：落位、失败关闭、`50300`、灰度路线的 ADR
- [`ARCHITECTURE.md`](./ARCHITECTURE.md)：分层、依赖、三端隔离、数据范围、缓存与消息
- [`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)：SEC-01～SEC-14
- [`AUDIT_STANDARDS.md`](./AUDIT_STANDARDS.md)：AUD-01～AUD-09
- [`DATA_STANDARDS.md`](./DATA_STANDARDS.md)：DATA-01～DATA-08
- [`TESTING_STANDARDS.md`](./TESTING_STANDARDS.md)：TEST-01～TEST-08
- [`API_STANDARDS.md`](./API_STANDARDS.md)：API-01～API-12
- [`CONFIG_STANDARDS.md`](./CONFIG_STANDARDS.md)：CFG-01～CFG-08
- [`RELIABILITY_STANDARDS.md`](./RELIABILITY_STANDARDS.md)：REL-01～REL-08
- [`OBSERVABILITY_STANDARDS.md`](./OBSERVABILITY_STANDARDS.md)：OBS-01～OBS-08
- [`CODE_STYLE.md`](./CODE_STYLE.md) / [`COMMENT_STANDARDS.md`](./COMMENT_STANDARDS.md)：S-01～S-12 / C-01～C-10
- [`CI_STANDARDS.md`](./CI_STANDARDS.md)：CI-01～CI-14
- [`docs/audit/EVENTS.md`](./audit/EVENTS.md) / [`docs/audit/FIELDS.md`](./audit/FIELDS.md)
- 代码：`pkg/rbac`、`internal/security`、`internal/shared/auth`、`internal/shared/apperr`
- NIST RBAC（INCITS 359）概念参考；OWASP Authorization Cheat Sheet
