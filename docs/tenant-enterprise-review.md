# 租户域企业级评审与分批开发计划（ENT-09）

> **文档性质**：L3 活跃计划（评审结论 + 分批任务）。未关闭条目的范围与验收对合入有约束力。  
> **评审对象**：`internal/tenant`（4243 行）、`migrations/000008`～`000011`、`api/openapi/{paths,schemas}/admin/tenant.yaml`、`cmd/internal/tenantruntime`、`internal/platform/tenantguard.go`、`docs/adr/0006`。  
> **评审日期**：2026-11-03  
> **当前结论**：**不满足企业级租户模块要求**；作为「主数据 + 状态机」垂直切片质量合格，但缺入驻审核、套餐、邀请、租户自助与 IAM 联动，且有 3 个已复现的 P0 缺陷。  
> **状态**：**B1 已完成；B2 进行中（T-05/T-06 已完成）**，B3/B4 未开始。执行记录见 [§6.2](#62-b1-实施记录2026-11-03) / [§6.3](#63-b2-实施记录2026-11-032)。

---

## 一、结论摘要

| 维度       | 评分     | 依据                                   |
| -------- | ------ | ------------------------------------ |
| 分层与代码质量  | 8 / 10 | domain 零依赖、四层边界清晰；无绕过状态机的写路径；命名与注释规范 |
| 生命周期正确性  | 5 / 10 | 状态机设计合理，但缺开通前置、缺并发保护、读语义不一致          |
| 企业级功能完整度 | 5 / 10 | 缺入驻审核、套餐/计量、邀请、租户自助、SSO、租户级配置        |
| 生产就绪度    | 4 / 10 | 审计非原子、缓存失效器实际不生效、无租户指标、无域门禁          |
| 验收口径达成度  | 4 / 10 | ENT-09 原始验收三条中两条未真正达成（详见 §四）         |

**一句话**：能跑通「建租户 → 开通 → 停用 → 恢复 → 终止 + 成员/配额」的完整链路，工程质量在平均线以上；但把它当企业级租户模块交付会漏掉客户生命周期里最关键的几段（审核、套餐、邀请、自助、注销），并且现有三处缺陷会造成「接口报错但数据已变」「用户输错参数报 500」这类难以解释的现象。

### 1.1 明确不属于缺陷的设计取舍

以下三项是已记录的决策，评审不再重复挑战，但列为后续演进的约束条件：

| 取舍                                             | 出处                                                          | 影响                                                       |
| ---------------------------------------------- | ----------------------------------------------------------- | -------------------------------------------------------- |
| `tenant.id` 直接等于 `merchant_id`，不建映射表、不设 `code` | [ADR-0006](./adr/0006-tenant-as-isolation-boundary.md)      | 上线顺序被固定为「先回填租户 → 再开守卫」；`merchant_id` 从自由字符串收紧为必须存在的租户 ID |
| C 端保持全局单租户，`merchant_id` 恒为空串                  | [ADR-0005](./adr/0005-c-channel-multi-merchant-boundary.md) | C 端不接租户守卫是正确的，不是遗漏                                       |
| 成员只有 owner / admin / member 三级，不复制 IAM 权限模型    | 计划 §3.1、ADR-0006                                            | 细粒度权限必须通过 IAM 绑定表达，因此「成员表 ↔ IAM」的联动不可省略（见 P1-8）          |

---

## 二、评估方法与可复现证据

- 逐层通读 domain / app / infrastructure / transport 与四个迁移、OpenAPI 契约、runbook。
- 对三个可疑点写一次性断言实跑（`go test -run TestScratch`），确认后删除脚本，工作区无残留。复现代码见 [附录 A](#附录-a缺陷复现脚本)。
- 关键代码位置均给到 `文件:行号`，便于直接定位。

---

## 三、企业级能力基线 vs 现状

状态说明：**有** = 可直接支撑生产；**部分** = 有数据结构但缺关键环节；**无** = 完全缺失。

### 3.1 租户主数据与标识

| 能力   | 企业级要求                                  | 现状                 | 状态 |
| ---- | -------------------------------------- | ------------------ | -- |
| 唯一标识 | 稳定 ID + 人类可读 `code`/slug（URL、对账、工单都用它） | 只有服务端生成的 ID；无 code | 部分 |
| 名称   | 展示名，可改名                                | 只有创建时写入，**无重命名接口** | 部分 |
| 联系方式 | 主联系人、邮箱、电话、地址（合同与通知依赖）                 | 无                  | 无  |
| 组织属性 | 行业、规模、地区、时区、语言、币种                      | 无                  | 无  |
| 品牌   | logo、主题色、自定义域名                         | 无                  | 无  |
| 外部映射 | 与 CRM / 计费 / 工单系统的外部 ID                | 无                  | 无  |
| 租户类型 | 试用 / 正式 / 内部 / 沙箱                      | 无（只有状态，没有类型）       | 无  |

### 3.2 入驻与开通

| 能力    | 企业级要求                                             | 现状                                                 | 状态 |
| ----- | ------------------------------------------------- | -------------------------------------------------- | -- |
| 申请与审核 | 申请单 → 资质审核 → 通过才可开通，审核留痕                          | 无；`POST /tenants` 直接建 `pending`，`activate` 无任何前置校验 | 无  |
| 开通前置  | 至少一名 owner、默认配额、默认配置就绪才允许 `active`                | 可开通 0 成员、0 配额的租户                                   | 无  |
| 开通编排  | 异步 saga：建租户 → 建默认角色绑定 → 建默认配额 → 初始化配置 → 发通知；失败可补偿 | 单条 INSERT，无编排、无补偿                                  | 无  |
| 幂等    | 重试不产生重复租户（幂等键或外部唯一键）                              | 依赖全局 Idempotency 中间件；业务层无外部唯一键                     | 部分 |
| 试用与到期 | `trial_ends_at`、到期自动转 `suspended`                 | 无字段、无定时任务                                          | 无  |

### 3.3 生命周期

| 能力      | 企业级要求              | 现状                                                      | 状态 |
| ------- | ------------------ | ------------------------------------------------------- | -- |
| 状态机     | 合法迁移白名单，终态不可复活     | 四态白名单，`terminated` 终态                                   | 有  |
| 迁移留痕    | 原因、操作人、时间、前后状态     | `status_reason` + `suspended_at` / `terminated_at` + 审计 | 有  |
| 并发保护    | 乐观锁或行锁 + 冲突语义      | `version` CAS，0 行区分 404/409                             | 有  |
| 定时动作    | 到期停用、宽限期结束终止       | 无                                                       | 无  |
| 注销与数据处置 | 终止后按策略导出/删除/保留，可验证 | 仅状态位；处置属 ENT-15                                         | 无  |

### 3.4 成员与组织

| 能力         | 企业级要求                            | 现状                                  | 状态 |
| ---------- | -------------------------------- | ----------------------------------- | -- |
| 成员管理       | 增删改查、停用代替删除                      | 有（停用保留记录）                           | 有  |
| owner 保护   | 至少一名在册 owner                     | 有校验，但存在并发绕过（P1-4）                   | 部分 |
| 邀请流        | 邀请 → 邮件/链接 → 接受 → 入组；可过期、可重发、可撤销 | 无；只能按 `subject_id` 直接插入，运营必须知道内部 ID | 无  |
| 席位上限       | 成员数受套餐配额约束                       | 无；加成员不读 `tenant_quota`              | 无  |
| 批量导入       | CSV / SCIM                       | 无                                   | 无  |
| SSO / 域名验证 | 企业客户刚需                           | 无                                   | 无  |
| 多租户切换      | 一个用户属于多租户时的上下文切换                 | 表结构支持（PK 含 tenant），但无接口、令牌无当前租户概念   | 无  |
| 离职处置       | 停用成员并回收其会话/绑定                    | 只改成员状态，不动会话与绑定                      | 部分 |

### 3.5 授权与隔离

| 能力      | 企业级要求           | 现状                                    | 状态 |
| ------- | --------------- | ------------------------------------- | -- |
| 租户级授权   | 「租户管理员」只能管自己租户  | 无；只有全局 `tenant:read` / `tenant:write` | 无  |
| 租户自助    | B 端管理自己的成员/配额   | 无；11 个端点全在 A 端                        | 无  |
| 数据范围强制  | 仓储层强制过滤，跨租户读写被拒 | 待 ENT-12                              | 无  |
| 高风险双人复核 | 终止租户等动作需审批或四眼原则 | 无                                     | 无  |

### 3.6 配额与计量

| 能力    | 企业级要求            | 现状                      | 状态 |
| ----- | ---------------- | ----------------------- | -- |
| 上限配置  | 按资源键配置上限，`-1` 不限 | 有 `tenant_quota` + 读写接口 | 有  |
| 套餐默认值 | 按 plan 预置一组配额    | 无 plan 概念               | 无  |
| 用量计量  | 采集用量（席位、调用量、存储）  | 无                       | 无  |
| 执行    | 超限拒绝并统一 `42900`  | 无执行点（ENT-14）            | 无  |
| 变更审计  | 配额变更可追溯          | 有 `tenant.quota_change` | 有  |

### 3.7 配置、合规与运营

| 能力                | 企业级要求            | 现状                                                    | 状态 |
| ----------------- | ---------------- | ----------------------------------------------------- | -- |
| 租户级配置覆盖           | 按租户覆盖超时、策略、开关    | 无（配置是全局静态）                                            | 无  |
| Feature flag / 灰度 | 按租户/百分比灰度        | 无（ENT-11）                                             | 无  |
| 数据驻留 / 租户级密钥      | 按租户选择区域、独立 KEK   | 无                                                     | 无  |
| DSAR 导出/删除、保留策略   | 可验证的删除与导出        | 无（ENT-15）                                             | 无  |
| 租户健康与指标           | 状态分布、守卫拒绝数、缓存命中率 | 无任何租户指标                                               | 无  |
| 客户通知              | 停用/终止前通知客户       | 无（ENT-10）                                             | 无  |
| 按租户审计视图           | 一次查询拿到该租户全部事件    | 可按 `resource_id` 查，但租户事件 `merchant_id` 为空，无法与业务事件一次拉齐 | 部分 |

### 3.8 API 工程

| 能力   | 企业级要求                           | 现状                                      | 状态 |
| ---- | ------------------------------- | --------------------------------------- | -- |
| 分页   | 所有列表分页且有上限                      | 租户列表有；**成员/配额无分页**，`total = len(items)` | 部分 |
| 条件更新 | `If-Match` / `expected_version` | 返回 `version` 但**不接受**；客户端无法表达「基于我看到的版本」 | 部分 |
| 幂等   | 创建类接口支持幂等键                      | 依赖全局中间件                                 | 部分 |
| 错误码  | 4xx/5xx 语义稳定                    | 存在「非法参数 → 500」的映射缺口（P0-2）               | 部分 |
| 契约   | OpenAPI 全量覆盖 + 版本化              | 11 个端点全覆盖，`admin.yaml` 7.2.0            | 有  |

---

## 四、与 ENT-09 验收口径的差距

原始条目（`docs/production-readiness-gaps.md:440`）要求：**租户注册 / 审核 / 启停 / 套餐配额 / 退出；停用与 IAM、缓存、令牌联动**。  
验收：**停用后 ≤60s 拒绝访问；退出后数据按策略处置；全链路审计**。

| 验收项            | 实际                             | 判定                      |
| -------------- | ------------------------------ | ----------------------- |
| 停用后 ≤ 60s 拒绝访问 | `app.Guard` 30s TTL + B 端鉴权链拦截 | 达成（但见 P1-5：同进程失效机制并未生效） |
| 与 IAM 联动       | 成员表与 IAM 零打通，加成员不产生角色绑定        | **未达成**                 |
| 与令牌联动          | 停用不撤销会话/令牌，登录仍可成功              | **未达成**                 |
| 审核             | 无审批流，`pending → active` 直接放行   | **未达成**                 |
| 套餐配额           | 只有上限表，无套餐、无执行                  | **部分**                  |
| 退出后数据按策略处置     | 仅状态位，处置归 ENT-15                | **未达成**                 |
| 全链路审计          | 事件齐全，但与业务非原子，失败被静默丢弃           | **部分**                  |

> **结论**：`production-readiness-implementation-plan.md` §十二 把 ENT-09 标为「完成」是超前的。建议改为「**主体完成，验收缺口见 tenant-enterprise-review.md**」，待 B3/B4 关闭后再置完成。

---

## 五、缺陷清单

### 5.1 P0：已复现，会造成数据或接口语义错误

#### P0-1 PATCH 成员「部分提交」（B1 已修复）

- **位置**：`internal/tenant/transport/rest/handler_member.go:84-110`
- **现象**：同一请求先调 `SetRole` 再调 `SetStatus`，两次独立写入、两条审计、无事务。第二步失败时第一步已提交。
- **实测**：`PATCH /admin/v1/tenants/{id}/members/u1`，body `{"role":"admin","status":"bogus"}` → 返回 `500`，随后查列表得到 `"role":"admin"`。
- **影响**：客户端看到失败并重试，实际权限已被提升；审计出现两条不对称记录（一条成功、一条失败）。
- **修复方向**：新增单一用例 `MemberService.Update`，先校验全部入参再一次性 `UpsertMember`，审计只写一条并在 attrs 里记录 `from/to`。
- **验收**：新增用例断言「非法 status 时角色保持不变且返回 400」；`handler_member.go` 中不再出现两次 service 调用。

#### P0-2 非法 `status` 返回 500 而非 400（B1 已修复）

- **位置**：`internal/tenant/domain/member.go:133-136`（裸 `fmt.Errorf`）→ `internal/tenant/app/service.go:328-362`（`translate` 落到 `default`）
- **现象**：`MemberStatus` 非法值没有类型化错误，`translate` 无法识别，被当作内部故障。
- **实测**：同上请求返回 `{"code":50000,"message":"internal error"}`。
- **影响**：参数错误被报成服务端故障，前端无法定位，值班被误导；与项目「4xx 归因于调用方」的约定冲突。
- **修复方向**：新增 `InvalidStatusError`（或 `ErrMemberStatusInvalid`），在 `translate` 中映射为 400；同时检查 `Tenant.Transition` 的 `unsupported status` 是否也应类型化（当前不可从 HTTP 触达，但属于同型隐患）。
- **验收**：`PATCH` 非法 status、`Add` 非法 role、`PUT` 非法 resource 三类均返回 `40000`，并加入表驱动测试。

#### P0-3 终止态租户的读接口语义不一致（B1 已修复）

- **位置**：`internal/tenant/app/quota.go:80-83`（用 `mutableTenant`）vs `internal/tenant/app/member.go:128-131`（用 `tenants.Get`）
- **现象**：同一终止租户，`GET /members` 返回 200，`GET /quotas` 返回 409「tenant is terminated and cannot be changed」。
- **实测**：`成员列表 err=<nil>`；`配额列表 err=tenant is terminated and cannot be changed`。
- **影响**：终止租户恰恰是取证、对账、客诉回溯最需要读的场景；写守卫被误用到读路径。
- **修复方向**：`QuotaService.List` 改用 `tenants.Get`；在 `mutableTenant` 注释中明确「只用于写用例」，并加注释级约束防回归。
- **验收**：终止租户下 `GET /members`、`GET /quotas`、`GET /tenants/{id}` 均 200；写接口仍 409。

### 5.2 P1：正确性与架构，平时不显形但会在生产出现

#### P1-4 「最后一名 owner」存在 TOCTOU（B2 已修复）

- **位置**：`internal/tenant/app/member.go:152-160`（`SetRole`）、`:189-198`（`SetStatus`）
- **现象**：`CountActiveByRole` → 判断 `<=1` → `UpsertMember` 三步无事务、无行锁。
- **失败路径**：租户有 A、B 两名 owner，两个请求并发把 A、B 各降级为 admin。两者都读到 `count=2`，都通过校验，最终 **0 名 owner**。
- **影响**：租户失去管理者，只能靠 A 端人工修复；属静默数据损坏。
- **修复方向**：写路径改为事务内 `SELECT id FROM tenant WHERE id=? FOR UPDATE` 串行化同一租户的成员变更；memory 实现用租户级互斥；或引入 `tenant.version` 作为成员变更的 CAS 依据。
- **验收**：并发测试（两个 goroutine 同时降级不同 owner）断言「恰好一个成功、另一个 409」，且最终 owner 数 ≥1。

#### P1-5 状态缓存失效器挂在一个永不服务请求的实例上（B2 已修复）

- **位置**：`cmd/internal/tenantruntime/runtime.go:46-58`、`cmd/biz-api/main.go:64`
- **现象**：`Runtime.New`（admin-api 使用）自建一个 `Guard` 并作为 `Options.Invalidator`；但真正拦请求的 Guard 是 biz-api 通过 `tenantruntime.NewGuard` **另外构造**的实例。admin-api 的 Guard 不参与任何请求判定，`Invalidate` 清的是空缓存；biz-api 又没有租户变更入口，永远不会调用 `Invalidate`。
- **影响**：`docs/adr/0006:56` 的「同进程迁移后立即失效」不成立；实际收敛完全依赖 30s TTL。仍满足 ≤60s 验收，但机制描述与实现不符，容易让后续维护者误判失效边界。
- **修复方向**：二选一并写清：
  1. 承认 TTL-only：删除 admin-api 侧的 Guard 与 `Invalidator` 注入，ADR 与注释改为「跨副本/同副本均按 TTL 收敛」；
  2. 真做同进程失效：admin 与 biz 合并部署时共享实例，或引入跨进程失效通道（Redis pub/sub），成本更高。
- **验收**：ADR、代码注释、runbook 三处表述一致；若选 1，`Guard.Invalidate` 保留但注明「仅测试与同进程复用场景」。

#### P1-6 复活成员的响应 `created_at` 与库中不一致（B1 已修复）

- **位置**：`internal/tenant/app/member.go:111-124`
- **现象**：复活已停用成员时返回 `domain.NewMember` 生成的当前时间，而 UPSERT 有意保留首次加入时间。
- **实测**：返回 `07:49:47.7671707`，库中 `07:49:47.7635098`。
- **影响**：前端展示「加入时间」会跳变；若下游据此做留存统计会算错。
- **修复方向**：命中已停用分支时复用 `existing.CreatedAt` 构造返回实体（`member.CreatedAt = existing.CreatedAt`）。
- **验收**：返回体 `created_at` 等于库中值；补一条断言。

#### P1-7 审计与状态变更非原子，且失败被静默丢弃

- **位置**：`internal/tenant/app/service.go:83-84`（`_ = a.emitter.Emit(...)`）
- **现象**：状态已落库，审计是另一条独立 SQL；`SQLEmitter` 的错误被丢弃。
- **影响**：审计库不可用或超时时，出现「租户已停用但查不到是谁停的」，直接违反 AUD-03 的必录要求，且无告警。
- **修复方向**（需先定口径，见 D-01）：
  - 强一致：状态变更与审计同事务（`SQLEmitter` 接受 `*sql.Tx`）；
  - 最终一致：写 Outbox（仓库已有 `pkg/outbox`），由 worker 投递，失败可重试；
  - 最低限度：失败计数 + 结构化错误日志 + 指标 `tenant_audit_emit_failed_total`。
- **验收**：注入必失败的 emitter，断言业务结果与审计结果符合选定口径；指标可被 scrape。

#### P1-8 成员表与 IAM 完全未打通

- **位置**：`internal/tenant`（无任何 IAM 引用）、`internal/iam`（无任何 tenant 引用）
- **现象**：加成员不创建角色绑定，成员拿不到任何权限；IAM 绑定也不校验成员关系。`tenant_member.role` 只是一个标签。
- **影响**：ENT-09 验收项「与 IAM 联动」未达成；B 端用户即使被加进租户也无法获得任何操作能力，租户模块与授权体系是两套平行数据。
- **修复方向**：明确映射（建议在 ADR 追加）——`owner/admin/member` → 租户级角色绑定模板；成员变更时同步绑定，或由 IAM 在解析时反查成员表。必须选一条，不能两边都不做。
- **验收**：新增成员后，该主体在 B 端能获得与其角色匹配的最小权限；移除/停用后权限立即失效；有集成用例覆盖。

#### P1-9 停用不阻断登录，也不撤销令牌

- **位置**：`internal/identity`（登录路径无租户状态校验）、B 端登录为公开路由（无 Principal，守卫不介入）
- **现象**：停用租户的用户仍可登录拿到令牌，随后每个业务请求 403。
- **影响**：用户体验极差（能登录、什么都点不动），且令牌持续有效，安全语义含糊。
- **修复方向**：登录时按 `merchant_id` 校验租户状态（跨域用接口反转依赖，由 platform 注入 `TenantStatusReader`），停用返回明确业务码；是否同时撤销存量会话见 D-02。
- **验收**：停用租户登录返回可读错误；守卫与登录行为一致。

#### P1-10 身份域建号不校验 `merchant_id` 是否为已登记租户

- **位置**：`internal/identity/app`（建号路径未引用租户域）
- **现象**：可用任意 `merchant_id` 建号，形成孤儿账号；只有首次业务请求被守卫 403 才暴露。
- **影响**：`000010` 回填之后新产生的孤儿账号不会被回填覆盖，属于持续产生的脏数据。
- **修复方向**：建号时校验租户存在且非 `terminated`（同样走 `TenantStatusReader` 接口）；或在账号表加软校验并对账任务。
- **验收**：建号指定不存在的 `merchant_id` 返回 400；对账脚本可列出历史孤儿账号。


### 5.3 P2：工程细节与卫生问题

| #     | 位置                                                                   | 问题                                                        | 建议                                                  |
| ----- | -------------------------------------------------------------------- | --------------------------------------------------------- | --------------------------------------------------- |
| P2-11 | `internal/tenant/app/guard.go:68,164-183`                            | 全部 B 端请求共用一把 `sync.Mutex`；map 无容量上限、过期项只在被再次访问时删除         | 改 `RWMutex`；加容量上限 + 定期清扫（或引入 LRU）                   |
| P2-12 | `internal/platform/tenantguard.go:43-46`                             | `merchant_id` 为空直接放行（fail-open）。B 端令牌若缺商户声明会静默绕过全部租户校验    | B 端默认拒绝无商户令牌，配置开关保留迁移期例外                            |
| P2-13 | `internal/tenant/app/service.go:166`                                 | `status_reason` 每次迁移被覆盖；`resume` 时空串会抹掉停用原因               | 保留停用原因（独立列 `suspend_reason`），或 `resume` 时不覆盖        |
| P2-14 | `internal/tenant/infrastructure/persistence/mysql_tenant.go:184-187` | `LIKE` 转义未声明 `ESCAPE`，依赖服务端未开启 `NO_BACKSLASH_ESCAPES`     | 显式追加 `ESCAPE '\\'`（SQL 字面量），并在配置文档标注 sql_mode 要求    |
| P2-15 | `transport/rest/handler_member.go:32,129`                            | 成员/配额列表 `total = len(items)`，无分页无上限                       | 加分页参数与上限；成员列表默认 50                                  |
| P2-16 | `internal/platform/bootstrap.go:46`、`tenantguard.go:67`              | `Deps.TenantGuard` 只写不读；`ErrTenantGuardMissing` 定义后无人使用   | 接线使用或删除                                             |
| P2-17 | `scripts/ci/check-audit.sh`                                          | 只校验 8 个基线动作；新增的 5 个 `tenant.member.*` 靠人肉同步 EVENTS.md     | 增加「代码常量 ↔ EVENTS.md」对账（见 T-16）                      |
| P2-18 | 迁移与仓储                                                                | `INSERT ... AS new` 需 MySQL ≥ 8.0.19；`FIELD()` 为 MySQL 方言 | 在 DATA_STANDARDS 或迁移注释中写明最低版本；若要支持 5.7 需改写          |
| P2-19 | `internal/tenant/app/service.go:279`                                 | `actionFor` 的 `default` 返回 `ActionCreate`，未知目标状态会被记成「创建」  | 未知目标返回错误或专用 `tenant.transition_unknown`，不要复用 create |

---

## 六、分批开发计划

> 原则：**先修正确性，再补能力，最后做治理**。每批独立可交付、可回滚，批次内任务按编号顺序执行。  
> 每批完成的定义：代码 + 单测/集成用例 + 相关门禁通过 + 文档回写，缺一不算完成。

### B1 缺陷修复（P0）— 预计 0.5 天

| ID   | 任务                           | 改动文件                                                            | 验收                      | 状态           |
| ---- | ---------------------------- | --------------------------------------------------------------- | ----------------------- | ------------ |
| T-01 | `PATCH` 成员改为单一用例原子提交         | `app/member.go`（新增 `Update`）、`transport/rest/handler_member.go` | 非法 status 时角色不变；写库只发生一次 | ✅ 2026-11-03 |
| T-02 | `MemberStatus` 非法值类型化并映射 400 | `domain/errors.go`、`domain/member.go`、`app/member.go`           | 非法状态返回 `40000`，不再 50000 | ✅ 2026-11-03 |
| T-03 | 读路径与写守卫分离                    | `app/quota.go:80`                                               | 终止态读接口 200，写接口仍 409     | ✅ 2026-11-03 |
| T-04 | 复活成员回填 `created_at`          | `app/member.go`                                                 | 返回体与库中一致                | ✅ 2026-11-03 |

**批次验收命令**

```bash
go test ./internal/tenant/... -count=1
golangci-lint run ./internal/tenant/...
make check-openapi
```

**风险**：T-01 不改变审计动作集合（仍按「改角色」「启停」两个维度各记一条），但失败请求不再产生半截事件，且审计只在写库成功后发出。`docs/audit/EVENTS.md` 无需新增动作。

### B2 正确性与一致性（P1）— 预计 1.5～2 天

| ID | 任务 | 关键点 | 验收 | 状态 |
|---|---|---|---|---|
| T-05 | owner 不变量加锁 | `MemberRepository.Mutate`：MySQL 事务 + 租户行 `FOR UPDATE`，计数走加锁读；memory 用独立互斥 | 并发双降级「一成一败」，owner=1 | ✅ 2026-11-03 |
| T-06 | 修正失效器语义 | 采纳方案 1（TTL-only）：删除 `Options.Invalidator` 与 A 端未使用的 `Guard`；ADR/runbook/注释三处同步 | 三处表述一致，无死代码 | ✅ 2026-11-03 |
| T-07 | 审计一致性（D-01=②） | `tenant.audit` topic + OutboxEmitter；状态写入与 outbox 同事务；worker 注册 handler | outbox 写失败则业务失败；重复投递不重复落审计 | 待做 |
| T-21 | 停用撤销会话（D-02=①） | `SessionRevoker` 端口由 identity 实现；suspend/terminate **先**撤销在册会话再迁移状态；撤销失败即停用失败 | 停用后旧令牌立即失效，不能换新 | ✅ 2026-11-03 |
| T-08 | 登录期租户校验 | 定义 `TenantStatusReader` 接口，platform 注入，避免 identity→tenant 直接依赖 | 停用租户登录失败且错误可读 | 待做 |
| T-09 | 无商户令牌 fail-closed | B 端默认拒绝，配置项保留迁移期例外 | 缺 `merchant_id` 的 B 端令牌返回 401/403 | 待做 |
| T-10 | 建号校验 `merchant_id` | 复用 T-08 的接口 | 不存在/已终止租户建号返回 400 | 待做 |

**批次验收命令**

```bash
go test ./internal/tenant/... ./internal/identity/... ./internal/platform/... -count=1 -race
# 真实 MySQL（沿用 tests/integration/tenant 的基座）
TENANT_TEST_MYSQL_DSN='...' go test ./tests/integration/tenant/... -count=1 -v
```

**风险**：T-08/T-10 跨域改动，需确认不引入 `identity → tenant` 的包依赖（用接口反转）；`-race` 必须开，否则 T-05 无法证明。

### B3 企业级能力补齐 — 预计 3～5 天

| ID | 任务 | 范围 | 验收 |
|---|---|---|---|
| T-11 | 租户元数据 | 迁移新增 `code`（唯一）、联系人、行业、时区/语言、外部 ID；`PATCH /tenants/{id}` 改名与改元数据 | `code` 全局唯一；改名走审计；OpenAPI 升 7.3.0 |
| T-24 | 套餐模型（D-03=①） | `tenant_plan` 目录 + `tenant.plan_id`；`tenant_quota` 默认值由套餐派生；存量租户回填默认套餐 | 新建租户自动获得套餐默认配额；租户覆盖优先于套餐 |
| T-12 | 开通前置 | `activate` 要求 ≥1 active owner；`Create` 支持可选 `owner_subject_id`；默认配额取套餐 | 0 成员租户无法开通，错误可读 |
| T-13 | 成员分页与席位上限 | 列表分页；`Add` 前读套餐/租户的 `user` 上限 | 超席位返回 409，附当前值与上限 |
| T-14 | 邀请流 | `tenant_invitation` 表（token hash、过期、状态）、创建/接受/撤销/重发接口 | 过期邀请不可用；重复接受幂等；全流程审计 |
| T-15 | 条件更新 | 迁移/写接口接受 `If-Match` 或 `expected_version` | 版本不匹配返回 409 + 当前版本 |
| T-22 | 当前租户上下文（D-04=①） | B 端 claims 增 `current_tenant` + 切换端点；契约 major 版本 | 一人多租户可切换；切换写审计；旧令牌行为有明确定义 |
| T-23 | 数据生命周期（D-05） | `docs/tenant-data-lifecycle.md`：保留期 / 导出窗口 / 硬删除边界；到期任务与证据链 | 到期流程可演练；导出可校验；硬删有审计与报告 |

**批次验收命令**

```bash
make check-migrations && make check-openapi && make check-audit
go test ./internal/tenant/... -count=1
```

**风险**：T-11 的 `code` 唯一索引在存量数据上需要回填策略（建议 `id` 兜底后再人工改）；T-14 涉及邮件投递，依赖 ENT-10，未就绪前可只落库不发送。

### B4 治理与收口 — 预计 2 天

| ID   | 任务                 | 交付                                                                                                                                                                   |
| ---- | ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| T-16 | 租户指标               | `tenant_status`（Gauge，按状态）、`tenant_guard_decisions_total{result}`、`tenant_guard_cache_hit_total`、`tenant_transition_total{from,to}`、`tenant_audit_emit_failed_total` |
| T-17 | 域门禁 `check-tenant` | ① 代码中 `Action*` 常量必须出现在 `docs/audit/EVENTS.md`；② 路由 ↔ OpenAPI 双向对账；③ 状态机迁移矩阵快照（改白名单必须同步改测试）；并入 `make ci`                                                             |
| T-18 | IAM 联动（P1-8）       | 选定映射方案并实现；成员变更同步绑定；集成用例覆盖授权生效与失效                                                                                                                                     |
| T-19 | 清理与文档              | 删除或接线 `Deps.TenantGuard` / `ErrTenantGuardMissing`；ADR-0006 更正；计划状态表把 ENT-09 从「完成」改为「主体完成 + 验收缺口链接本文」                                                                |
| T-20 | 运维补充               | `tenant-suspend.md` 补「登录仍成功」的现状与处置；新增 `tenant-onboarding.md`（回填 + 开通检查清单）                                                                                            |

**批次验收命令**

```bash
make check-tenant   # 新增
make ci
```

**风险**：T-18 是跨域改造，建议单独 ADR（`0007-tenant-member-iam-binding.md`）后再动手，避免边写边改映射。

### 6.1 批次依赖与顺序

```text
B1（P0，无依赖）
  └─► B2（P1，T-07 依赖 D-01 决策；T-08/T-10 可并行）
        ├─► B3（能力，T-13 依赖 B1 的成员原子写；T-14 依赖 ENT-10 可选）
        └─► B4（治理，T-18 需独立 ADR；T-17 可在 B1 后先做）
```

> 若排期紧张：**B1 + T-05 + T-06 是「不做会出事」的最小集**，其余可按产品优先级裁剪。

### 6.2 B1 实施记录（2026-11-03）

**改动**

| 文件                                                 | 改动                                                                                                                                                                                                        |
| -------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/tenant/domain/errors.go`                 | 新增 `InvalidStatusError`；注释说明它只服务入参校验，`Member.Validate` 仍返回裸错误（脏数据应是 500，不是 400）                                                                                                                           |
| `internal/tenant/domain/member.go`                 | 新增 `ValidateMemberStatus`；`WithStatus` 改用类型化错误                                                                                                                                                            |
| `internal/tenant/app/member.go`                    | 新增 `UpdateMemberInput` / `MemberService.Update`：入参全部校验通过才读库；角色与状态合并为一次 `UpsertMember`；`ensureOwnerRemains` 按变更后终态判断；审计按实际变化维度发出。`SetRole` / `SetStatus` 降为 `Update` 的薄封装；`Add` 复活时回填 `existing.CreatedAt` |
| `internal/tenant/app/quota.go`                     | `List` 改用 `tenants.Get`，读路径不再套写守卫                                                                                                                                                                         |
| `internal/tenant/app/service.go`                   | `isInvalidEnum` 纳入 `InvalidStatusError`；`mutableTenant` 注释明确「只给写用例」                                                                                                                                       |
| `internal/tenant/transport/rest/handler_member.go` | `PATCH` 只调用一次 `Members.Update`，handler 不再做「至少给一个字段」的重复校验                                                                                                                                                  |

**行为变化**

- 非法 `status`：`50000` → `40000`，且不再产生任何写入与审计。
- 角色 + 状态同时变更：一次写库；审计按 `role_change` + `disable`/`enable` 各一条（与变更维度一致，不是按请求条数）。
- 无实际变化：不写库、不写审计（幂等），与旧 `SetRole` 行为一致。
- 终止态租户：`GET /members`、`GET /quotas` 均 200；`PUT /quotas` 仍 409。
- 复活成员：响应 `created_at` 等于库中首次加入时间。

**验证**

```bash
go test ./internal/tenant/... -count=1     # 全绿（新增 6 个回归用例）
golangci-lint run ./internal/tenant/...     # 0 issues
go test ./... -count=1                      # 全仓全绿
```

新增用例：`TestUpdateRejectsInvalidStatusWithoutSideEffect`、`TestUpdateAppliesRoleAndStatusInOneWrite`、`TestUpdateWithoutChangesIsIdempotent`、`TestReadsAllowedOnTerminatedTenant`、`TestUpdateMemberIsAtomicOverHTTP`、`TestUpdateMemberAppliesBothFields`；并在 `TestAddMemberAndDuplicate` 中补 `created_at` 一致性断言。

**未包含**：T-05 的并发 owner 保护（仍需事务/行锁，属 B2）、T-07 审计原子性（依赖 D-01 决策）。

### 6.3 B2 实施记录（2026-11-03）

**T-05 owner 并发保护**

| 文件 | 改动 |
|---|---|
| `internal/tenant/domain/repository.go` | `MemberRepository` 新增 `Mutate(ctx, tenantID, fn)`：把「读当前状态 → 统计 owner → 写入」收进同一临界区 |
| `internal/tenant/infrastructure/persistence/mysql_tenant.go` | 新增事务感知的 `exec` / `query` 助手（复用 `pkg/db.TxFromContext`）；`Mutate` 用 `WithinUnitOfWork` + `SELECT id FROM tenant … FOR UPDATE`；`CountActiveByRole` 在事务内追加 `FOR UPDATE` |
| `internal/tenant/infrastructure/persistence/memory_tenant.go` | 独立 `txMu` 模拟临界区（与读写锁分离，避免自锁） |
| `internal/tenant/app/member.go` | `Update` 整体移入 `Mutate`；锁内重新读租户与成员，不使用锁外快照 |

为什么计数也要加锁读：REPEATABLE READ 下普通 `SELECT` 可能复用事务开始时的快照，
即使拿到了租户行锁，仍会看到别人提交前的 `count=2`，行锁就白加了。

**T-06 失效器语义**

删除 `app.Options.Invalidator`、`app.StatusInvalidator` 与 `tenantruntime.Runtime.Guard`。
原因：A 端 `Runtime.New` 自建的 `Guard` 不参与任何请求判定，真正拦流量的是 B 端
`NewGuard` 构造的另一个实例；`Invalidate` 永远清不到生效缓存。收敛改为完全由
30s TTL 保证（验收上限 60s）。ADR-0006、runbook、`Guard.Invalidate` 注释同步更正。

**验证**

```bash
go test ./internal/tenant/... -count=1
TENANT_TEST_MYSQL_DSN='...' go test ./tests/integration/tenant/ -count=5 -run ConcurrentOwner
golangci-lint run ./internal/tenant/...
```

新增用例：`TestConcurrentOwnerDemotionKeepsOneOwner`（内存）、
`TestConcurrentOwnerDemotionOnMySQL`（真实 MySQL，连跑 5 次通过）。
本机无 gcc，`-race` 未执行，记入待补项。

**T-21 停用撤销会话（D-02=①）**

| 文件 | 改动 |
|---|---|
| `internal/tenant/app/revoker.go` | 新增 `SessionRevoker` 端口：`RevokeByTenant(ctx, tenantID) (int, error)`，租户域不 import 身份域 |
| `internal/tenant/app/service.go` | `Transition` 改为「预检 → 撤销 → 事务」三段：停用 / 终止先撤销会话再迁移；撤销失败 → 整体失败、状态不变；撤销数写进审计 `attrs.revoked_sessions` |
| `internal/identity/domain/session.go` | 新增 `RevokeReasonTenantDisabled`（`tenant_disabled`） |
| `internal/identity/app/tenant_revoker.go` | 新增 `TenantSessionRevoker`：按商户分页枚举 B 端账号，活跃账号 `BumpTokenVersion` + `RevokeByAccount`（旧访问令牌立即失效），停用账号仅撤会话 |
| `cmd/internal/identityruntime/runtime.go` | `Runtime.TenantRevoker` 暴露撤销器 |
| `cmd/internal/tenantruntime/runtime.go` / `cmd/admin-api/register.go` | `tenantruntime.New(deps, revoker)` 接收撤销器；admin-api 从 identity 运行时提取并注入 |

**一致性边界（诚实记录）**：identity 事务不与租户域共享，撤销按账号逐个提交。
顺序选了「先撤销、后迁移」——撤销成功但迁移失败会出现「会话已撤、租户未停」，
方向 fail-closed（用户重新登录即恢复）；反过来（先迁移后撤销）失败会留下
「已停用、令牌还能换新」，正是 D-02 禁止的状态。已写入代码注释与本文。

**验证**：identity 侧 `TestTenantSessionRevoker`（活跃账号升版 + 会话全撤、
停用账号仅撤会话、其他商户不受影响、未知商户幂等返回 0）；租户侧 5 组
（suspend/terminate 触发、撤销失败状态不变且 50000、activate 不触发、
nil 撤销器可停用、审计含 revoked_sessions）。全仓测试与 lint 全绿。

**未包含**：T-08～T-10（登录期校验 / 无商户令牌 fail-closed / 建号校验，共用
`TenantStatusReader`）。

---

## 七、决策记录（2026-11-03 已定）

| ID | 决策点 | 结论 | 派生任务与约束 |
|---|---|---|---|
| D-01 | 审计失败时业务是否失败 | **② Outbox 最终一致** | T-07：新增 `tenant.audit` topic 与 `OutboxEmitter`，业务写入与 outbox 记录同事务（复用 `pkg/db.WithinUnitOfWork`），worker 注册消费 handler。约束：审计可以延迟，但**不可以丢失**——outbox 写失败必须让业务失败，否则又回到静默丢审计 |
| D-02 | 停用是否撤销存量会话/令牌 | **① 立即撤销** | T-21：tenant 定义 `TenantSessionRevoker` 端口，identity 实现；`suspend` / `terminate` 成功后按 `merchant_id` 撤销该租户全部在册会话。撤销失败 → 业务失败（不允许「状态已停用、令牌还能换新」） |
| D-03 | 是否引入套餐 | **① 引入 plan/tier** | T-24：`tenant_plan` 目录表 + `tenant.plan_id`；配额默认值由套餐派生、租户可覆盖；迁移需把存量租户回填到默认套餐。T-12 的开通前置与 T-13 的席位上限改为读套餐默认值 |
| D-04 | 多租户当前上下文 | **① 令牌携带 `current_tenant`** | T-22：B 端 claims 增加 `current_tenant` + 切换端点；**属于契约 major 变更**。执行顺序要求：必须晚于 T-14（邀请/多租户成员真实存在），否则所有令牌多一个恒等于 `merchant_id` 的字段，是 ADR-0005 明确警告过的「预留半成品」 |
| D-05 | 终止后数据处置 | **保留期 + 导出窗口 + 硬删除边界三段式，合同与证据闭环** | T-23：`docs/tenant-data-lifecycle.md` 定义三段时间、责任人、合同条款映射；配置 `tenant.retention.*`；到期流程为「生成可验证导出 → 校验通过 → 按边界硬删 → 写 `tenant.data_purge` 审计 + 出报告」。归口 ENT-15，本文只定义租户侧接口与验收 |

> **D-04 的顺序风险**：`current_tenant` 在「一个用户属于多个租户」成为真实场景前没有收益，
> 却会同时改动契约、令牌 claims、缓存键与审计字段。若 T-14 尚未落地，本项应保持「已定未做」。

## 八、门禁与文档回写清单

- [ ] `scripts/ci/check-tenant.sh`（T-17）并接入 `make ci`
- [ ] `docs/audit/EVENTS.md`：补充 `tenant.member.*` 的触发路径变化（B1 后）
- [ ] `docs/adr/0006-tenant-as-isolation-boundary.md`：修正失效机制表述（T-06）
- [ ] `docs/production-readiness-implementation-plan.md` §十二：ENT-09 状态改为「主体完成 + 验收缺口」，链接本文
- [ ] `docs/runbooks/tenant-suspend.md`：补登录行为与 503/403 之外的新错误码
- [ ] `docs/iam/PERMISSIONS.md`：若 T-18 引入租户级角色，需同步权限目录与 `internal/iam/catalog`

---

## 附录 A：缺陷复现脚本

以下为评审时使用的一次性断言（确认后已删除，勿提交）。

```go
// 放在 internal/tenant/app/ 下，package app_test
func TestScratchReactivationCreatedAtMismatch(t *testing.T) {
    ctx := context.Background()
    repo := persistence.NewMemoryRepository()
    svc, _ := app.NewMemberService(repo, repo, scratchEmitter{}, app.Options{Service: "t"})
    now := time.Now()
    _ = repo.Create(ctx, domain.Tenant{ID: "t1", Name: "T", Status: domain.StatusActive,
        Version: 1, CreatedAt: now, UpdatedAt: now})
    actor := app.Actor{SubjectID: "admin", ActorType: "user", Channel: "admin"}
    _, _ = svc.Add(ctx, app.AddMemberInput{TenantID: "t1", SubjectID: "u1",
        Role: domain.MemberRoleMember, Actor: actor})
    _, _ = svc.SetStatus(ctx, app.SetMemberStatusInput{TenantID: "t1", SubjectID: "u1",
        Status: domain.MemberStatusDisabled, Actor: actor})
    time.Sleep(3 * time.Millisecond)
    got, _ := svc.Add(ctx, app.AddMemberInput{TenantID: "t1", SubjectID: "u1",
        Role: domain.MemberRoleAdmin, Actor: actor})
    stored, _ := repo.GetMember(ctx, "t1", "u1")
    if !got.CreatedAt.Equal(stored.CreatedAt) {
        t.Errorf("返回 %v，库中 %v", got.CreatedAt, stored.CreatedAt)
    }
}

// 终止态读一致性
func TestScratchTerminatedReadInconsistent(t *testing.T) {
    ctx := context.Background()
    repo := persistence.NewMemoryRepository()
    members, _ := app.NewMemberService(repo, repo, scratchEmitter{}, app.Options{Service: "t"})
    quotas, _ := app.NewQuotaService(repo, repo, scratchEmitter{}, app.Options{Service: "t"})
    now := time.Now()
    _ = repo.Create(ctx, domain.Tenant{ID: "t2", Name: "T", Status: domain.StatusTerminated,
        Version: 3, CreatedAt: now, UpdatedAt: now, TerminatedAt: &now})
    _, memberErr := members.List(ctx, "t2", false)
    _, quotaErr := quotas.List(ctx, "t2")
    if memberErr == nil && quotaErr != nil {
        t.Errorf("成员 nil，配额 %v", quotaErr)
    }
}
```

`PATCH` 部分提交的复现走 HTTP：建租户 → 加成员（`role=member`）→ `PATCH {"role":"admin","status":"bogus"}`，观察返回码与随后列表中的 `role`。

---

## 附录 B：本次评审覆盖的文件

| 范围  | 文件                                                                                                                     |
| --- | ---------------------------------------------------------------------------------------------------------------------- |
| 领域  | `internal/tenant/domain/{tenant,member,quota,errors,repository}.go`                                                    |
| 应用  | `internal/tenant/app/{service,member,quota,guard}.go`                                                                  |
| 持久化 | `internal/tenant/infrastructure/persistence/{memory_tenant,mysql_tenant}.go`                                           |
| 传输  | `internal/tenant/transport/rest/{handler,handler_member}.go`、`transport/admin/router.go`                               |
| 装配  | `cmd/internal/tenantruntime/runtime.go`、`cmd/{admin,biz,c}-api/main.go`、`internal/platform/{bootstrap,tenantguard}.go` |
| 数据  | `migrations/000008`～`000011`（含 down）                                                                                   |
| 契约  | `api/openapi/admin.yaml`、`api/openapi/{paths,schemas}/admin/tenant.yaml`                                               |
| 治理  | `docs/adr/0005`、`docs/adr/0006`、`docs/runbooks/tenant-suspend.md`、`docs/audit/EVENTS.md`、`docs/iam/PERMISSIONS.md`     |
