# ADR-0004：RBAC 权限管理（IAM）落位与鉴权依赖失败关闭

- 状态：已接受
- 日期：2026-10-21
- 决策人：架构 Owner、安全 Owner
- 关联：[`docs/rbac-iam.md`](../rbac-iam.md)、ADR-0002（模块化单体多 cmd）、ADR-0003（三端隔离）、[`SECURITY_STANDARDS.md`](../SECURITY_STANDARDS.md) SEC-14、[`API_STANDARDS.md`](../API_STANDARDS.md) API-04

## 背景

角色与权限点目前在 `cmd/admin-api`、`cmd/biz-api` 装配期以 `rbac.GrantRole` 硬编码，无法持久化、运营与审计；`pkg/rbac` 只提供纯判定原语，缺少角色、绑定、数据范围与撤销时效的完整模型。历史上线路径需要一次可灰度、可回滚的迁移，而不是直接替换鉴权来源。

同时，「鉴权依赖不可用」缺少稳定错误码：现有映射会把 `CodeUnavailable` 归入 `50000`，无法与真正的内部错误区分，也不利于客户端按 `Retry-After` 重试。

## 决策

1. **落位**：RBAC 权限管理作为 `internal/iam` 业务域实现，由 `admin-api` / `biz-api` 按端挂载 transport，**不新增可执行文件**；`pkg/rbac` 保持纯判定，不引入 I/O、数据库或缓存。
2. **鉴权输入**：JWT 只承载身份与 `channel`，不承载权限点全集；每次鉴权以 IAM「有效权限快照」为准，快照经 L1（进程内）/ L2（Redis）两级缓存并主动失效。
3. **数据范围**：功能权限与数据范围分离建模（`all` / `merchant` / `self` / `merchant_set`）；B 端仅允许 `merchant` / `self`，由 app 与仓储条件双重落实（SEC-14）。
4. **失败关闭**：鉴权依赖（快照缓存与 DB）不可用时**拒绝**请求，返回新增稳定码 **`50300`（HTTP 503）**，附带 `Retry-After`；禁止回退到令牌内角色或默认放行。
5. **新增稳定错误码**：允许在 `internal/shared/apperr` 中新增 `CodeServiceUnavailable = 50300` 与 `Unavailable(msg)` 构造器；`xerr.CodeUnavailable` 双向映射到该码。按 API-04「只追加」执行，不改动任何已发布码语义。
6. **契约表达**：由于错误码表只增不减，`api/openapi/components.yaml` 的 `ErrorCode` 使用 `x-extensible-enum` 声明为开放集合，使新增码在 oasdiff 中不构成 breaking；客户端须对未知码兜底。
7. **灰度路线**：`iam.mode=off | shadow | enforce` 三态推进 —— `off`（沿用静态表，可作应急回滚）→ `shadow`（计算 IAM 快照但按静态表放行，记录差异）→ `enforce`（以 IAM 为唯一真相）；生产切 `off` 必须告警并记 `config.change` 审计。
8. **进入 M0/M1**：批准启动 M0（本文档评审、权限目录评审、威胁模型）与 M1（迁移 000002/000003、`internal/iam/domain`、memory + SQL 仓储、domain 单测）。
9. **M1 前置确认（2026-10-21）**：数据库基线为 **MySQL 8.0.19+**（单引擎，不并行维护 PostgreSQL；时间用 `DATETIME(3)`、JSON 用原生 `JSON`、字符集显式 `utf8mb4_0900_ai_ci`）；迁移序号 **`000002`（IAM 表）与 `000003`（权限种子）确认未被占用**；权限目录一致性由 `internal/iam/catalog` 的 **Go 测试**做门禁（`catalog.go` / `perm.go` / 种子迁移 / `docs/iam/PERMISSIONS.md` 四处一致），**不新增 shell 解析脚本**。

## 备选方案

| 方案 | 未采纳原因 |
|------|------------|
| 独立 IAM 微服务 | 当前阶段边界未稳定，违反「先模块化单体，后微服务」；引入跨服务鉴权依赖会放大可用性风险 |
| 权限点写入 JWT | 撤销延迟等于令牌有效期；令牌体积随权限数膨胀；泄露面更大 |
| 引入 Casbin / OPA / CEL | 新增策略引擎依赖与运维面，v1 无 ABAC 需求；RBAC + 数据范围已满足 |
| 复用 `50000` 表达依赖不可用 | 客户端无法区分「可重试的依赖故障」与「内部缺陷」，重试策略失真 |
| 直接提升三端 OpenAPI major | 仅为表达新增错误码而破坏客户端兼容，代价与收益不匹配 |

## 后果

**正面**

- 角色 / 权限 / 绑定可运营、可审计、可按商户定制，满足 AUD-05 与 SEC-14。
- 撤销时效有界（L1 3s），依赖故障时行为确定（503 + `50300`），不出现「静默放行」。
- 迁移路径可回滚，回滚只需切配置，不依赖数据库回滚。
- 契约层用开放枚举表达只增码表，避免每次加码都触发 breaking 误报。

**负面 / 风险**

- 鉴权多了一次缓存 / DB 依赖：缓存命中率与 DB 容量成为可用性关键路径，需 SLO 与告警。
- `enforce` 后静态表与 IAM 双写期存在一致性维护成本，必须靠 `shadow` 差异指标收敛。
- `ErrorCode` 改为开放集合后，生成式客户端不再做枚举校验，需在客户端侧约定未知码兜底。
- 破玻璃机制若配置不当会变成长期超级权限，需默认关闭 + 双人审批 + 实时告警。

## 修订记录

### 2026-10-26：企业级模型补齐（基线内修订）

原决策保持不变（RBAC + 数据范围、角色扁平、不做 ABAC）。本次只加固模型完整性与库级约束：

1. **库级完整性**：枚举、端别 / 商户组合、审批单租户组合改由 `CHECK` 约束拒绝；绑定与角色之间改复合外键
   `(role_id, channel, merchant_id)`，跨端 / 跨商户绑定在库层不可能写入；权限目录父节点改为 `NULL` 并加自引用
   外键，菜单 / 按钮树不再允许悬空节点。
2. **绑定历史**：解绑由物理删除改为软撤销（`revoked_at/revoked_by/revoke_reason`），并用生成列 `active_key` +
   唯一索引保证「同一主体同一角色至多一条生效绑定」。已过期但未解绑的记录仍由查询条件过滤，不再需要物理删除。
3. **角色模板**：新增 `iam_role_template` / `iam_role_template_permission` / `iam_role_template_data_scope`；
   `iam_role.template_id + template_version` 记录实例基线，用于识别与同步模板漂移，避免商户规模增长后逐商户重复维护。
4. **审批单治理**：补充 `expires_at`、`decision_reason`、`applied_by`、`payload_version`，状态机增加 `expired`；
   `pending_key` 生成列 + 唯一索引阻止同一目标并发重复提单。
5. **数据范围**：冗余 `channel` 列并由复合外键锁定与角色一致，使「B 端只允许 merchant / self」成为库级约束；
   JSON 取值结构（`merchant_set` 的商户数量）由 `CHECK` 校验。

影响与边界：

- 迁移 `000002` / `000003` 尚未发布，按基线内修改处理；已发布环境必须改为追加式迁移，不得回改历史文件。
- 权限目录仍为 API 优先，`menu` / `button` 节点按产品节奏追加，本次只提供树结构与接口能力，不编造菜单码。
- 非目标不变：仍不做角色继承、拒绝规则、ABAC 表达式引擎与部门树；若客户要求「部门-岗位-角色」三层，需另立 ADR。

## 后续行动

1. M0：提交权限目录、OpenAPI 草案、威胁模型评审；确认 `docs/iam/PERMISSIONS.md` 归属。
2. M1（已完成）：权限目录（`catalog` / `perm.go` / 种子 / `PERMISSIONS.md`）、迁移 000002/000003、`internal/iam/domain`（97.9%）、memory 仓储（94.7%）、MySQL 仓储与 `tests/integration/iam` 集成测试（MySQL 8.0 service，`IAM_TEST_MYSQL_DSN`）均已落地并通过 Go 门禁。
3. M2：接入 `SnapshotLoader` 与两级缓存，跑通 `shadow` 差异比对。
4. 同步更新 `docs/audit/EVENTS.md`（`rbac.role_change` 等置 `active`）与 `docs/runbooks/iam.md`。
