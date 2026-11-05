# IAM 模块生产级代码审计与架构评估报告

| 项目 | 内容 |
|------|------|
| 审计对象 | `internal/iam`（含与 `internal/identity`、`internal/platform` 的集成边界） |
| 审计基线 | `d1d2c1e`（2026-11-05），工作区当前状态 |
| 审计日期 | 2026-11-05 |
| 审计范围 | 领域层、应用层、基础设施（MySQL/Redis/Outbox）、传输层、迁移脚本、配置与部署制品、CI 门禁 |
| 审计方法 | 静态代码走查 + 依赖/契约核对 + 实测（`go test`、`go vet`、`golangci-lint`、覆盖率统计） |
| 总体结论 | **不满足“无条件生产全量上线”标准；满足“修复 P0 后进入 staging 灰度”的条件** |

---

## 1. 结论摘要

### 1.1 总判定

IAM 模块已具备企业级 RBAC 的核心骨架：RBAC1 角色继承、RBAC2 约束、SoD 互斥、数据范围、变更审批、破玻璃、菜单派生、审计 Outbox、两级授权快照均已落地，分层与错误码体系完整，`go vet` 与 `golangci-lint` 零告警，全部 IAM 单元测试通过。

但当前存在 **2 个上线阻断项** 和 **7 个灰度前必须关闭项**，集中在：

1. **安全控制可被绕过**：角色模板的同步/实例化直接写入角色，不进入审批单（破坏 4-eyes）。
2. **授权一致性存在竞态**：权限撤销后旧快照可能回填 L2 缓存并存活至 5 分钟，与文档承诺的 ≤3s 不符。
3. **生产配置治理**：生产样例文件内嵌可用密钥与 root DSN，且启动期/CI 未强制 `iam.mode=enforce`、未强制审批开启、未强制幂等键。
4. **依赖单点**：限流与幂等对 Redis 采用 fail-closed，Redis 故障会放大为全站写请求不可用。
5. **质量门禁偏窄**：模块总覆盖率 57.2%，`infrastructure/persistence` 仅 21.3%，CI 只对 domain 设 70% 门槛（当前 71.7%，几乎没有缓冲）。

### 1.2 分维度评级

| 维度 | 评级 | 结论 |
|------|------|------|
| 架构与分层 | L3 生产级 | `transport → app → domain` 单向依赖由 CI 强制，infrastructure 实现 domain 端口 |
| 核心授权能力 | L3 生产级 | RBAC1/2、SoD、数据范围、破玻璃、审批状态机完整 |
| 身份认证与令牌 | L2 可用 | 登录/刷新/登出/MFA/会话撤销完备；OIDC/SAML/SCIM 仅预留 |
| 生产可用性 | L2 可用 | 多副本/HPA/PDB/反亲和/探针齐备；Redis 单点与降级策略不足 |
| 安全与合规 | L2 可用 | 审计同事务、加密与密钥托管到位；审批覆盖、配置密钥、访问复核有缺口 |
| 可观测性 | L2+ | 12 类指标与 trace 覆盖；缺授权决策级审计 |
| 工程质量 | L3 生产级 | vet/lint 全绿、错误码统一、命名与注释规范 |
| 测试质量 | L2 可用 | 总覆盖率 57.2%，包间分布不均，关键 SQL 仓储无 CI 覆盖率门槛 |
| 文档 | L2+ | 设计文档体系完整，存在与实现漂移 |

### 1.3 上线建议

- **不建议**在当前状态下直接全量生产上线。
- **建议**先关闭 P0-1、P0-2，并完成 P1-1～P1-5 后进入 staging 灰度；P1-6、P1-7 作为企业客户接入前的准入条件。
- 灰度期间保留 `iam.mode=shadow` 观测窗口仅用于验证决策一致性，切换 `enforce` 前必须完成缓存竞态修复。

---

## 2. 模块边界与功能设计全景

### 2.1 职责边界

| 能力 | 归属 | 说明 |
|------|------|------|
| 身份认证、凭据、MFA、令牌签发与撤销 | `internal/identity` | JWT 访问令牌 + 刷新轮换 + `TokenVersion` + jti 黑名单 |
| 授权（角色、权限、绑定、约束、审批、破玻璃） | `internal/iam` | 本报告主体 |
| 审计落库、查询、导出、保留清理 | `internal/shared/audit` + `internal/audit` + worker | IAM 只产生事件，查询由独立模块提供 |
| 租户/商户隔离 | `internal/tenant` | IAM 通过 `merchant_id` 与通道参数参与隔离 |

分层声明见 [internal/iam/doc.go](../internal/iam/doc.go#L3-L6)：`transport → app → domain`，`infrastructure` 实现 `domain` 端口；CI 门禁 LAYER-06 禁止 transport 依赖 infrastructure（[scripts/ci/check-layering.sh](../scripts/ci/check-layering.sh#L91-L98)）。

### 2.2 领域模型与核心不变量

| 聚合 | 关键不变量 | 证据 |
|------|-----------|------|
| 角色 `Role` | 端别/作用域/类型自洽、层级父角色同端同商户、版本乐观锁、`max_bindings` 基数、系统角色不可删、软删除 | [domain/role.go](../internal/iam/domain/role.go#L32-L85) |
| 绑定 `Binding` | 主体类型与端别匹配、B 端必须带商户、同主体同角色仅一条有效绑定、破玻璃绑定必须带有效期、撤销不可重复 | [domain/binding.go](../internal/iam/domain/binding.go#L11-L43) |
| 数据范围 `DataScope` | `all/merchant/self/merchant_set` 与端别/角色类型组合受限 | [domain/scope.go](../internal/iam/domain/scope.go#L117-L184) |
| 变更单 `ChangeRequest` | 状态机 `pending → approved → applied`、申请人不得自审、应用时重校验版本、过期不可用 | [domain/change_request.go](../internal/iam/domain/change_request.go#L80-L102)、`L354`、`L376` |
| 破玻璃 | 仅 admin、白名单主体、强制审批、最长 2 小时、绑定到期自动失效 | [domain/breakglass.go](../internal/iam/domain/breakglass.go) |
| 菜单 | 可见性由权限码推导、祖先链补齐、空目录剪枝、按钮节点不下发 | `internal/iam/menuview`、`internal/iam/domain/menu.go` |

数据库层使用复合外键与生成列把约束前移到库级：角色父级复合外键 `(channel, parent_code)`、绑定生效哨兵 `active_key` 生成列、权限 `resource` 由 `code` 首段派生（[migrations/000002_create_iam_tables.up.sql](../migrations/000002_create_iam_tables.up.sql#L33-L49)、`L93-L135`、`L207-L221`）。

### 2.3 核心链路

1. **鉴权链路**：`context 快照 → L1 进程内 → L2 Redis → DB 回源`，未命中时用 `singleflight` 合并并发回源；DB 不可用时 fail-closed；`shadow` 模式只记录不拦截（[app/authorize_service.go](../internal/iam/app/authorize_service.go#L82-L210)）。
2. **写链路**：`UnitOfWork` 单事务写业务 + `audit.Emit` 写 Outbox，提交后按角色闭包失效主体快照（[app/binding_service.go](../internal/iam/app/binding_service.go#L173-L198)、[app/outbox.go](../internal/iam/app/outbox.go)）。
3. **审批链路**：高风险直写命中审批开关后拒绝并返回 40900，引导创建变更单；审批人不得为申请人；应用时重新校验目标版本（`internal/iam/transport/rest/handler.go` `RequireApproval`、`rejectDirectWrite`）。
4. **模板链路**：模板为配置基线，实例化为复制而非继承；模板改版后通过 `Drift` 展示差异，`SyncTemplate` 批量改写已有角色权限（[app/template_service.go](../internal/iam/app/template_service.go#L343-L459)）。
5. **破玻璃链路**：申请生成变更单 → 他人审批 → 写入带过期时间的破玻璃绑定 → 快照合成通配权限（[app/breakglass_service.go](../internal/iam/app/breakglass_service.go)）。

### 2.4 端点规模

- A 端 IAM 路由约 50 条，B 端约 32 条（含 `me/permissions`、`me/menus`），均由 `authMW + perm(...)` 保护（[transport/admin/router.go](../internal/iam/transport/admin/router.go#L20-L134)、[transport/biz/router.go](../internal/iam/transport/biz/router.go#L23-L98)）。
- 注意：设计文档仍标注“M3 15 端点 / M4 13 端点”，与实际路由数量不符，属于文档漂移（见 P2-7）。
- 高风险写端点覆盖审批：角色创建/更新/删除/克隆/权限替换、绑定创建/解绑、RBAC2 约束、SoD 组（[transport/admin/router.go](../internal/iam/transport/admin/router.go#L87-L110)）。
- 通道隔离作用于装配期（A/B 各自注册路由），不接受请求参数切换端别；审批单端别来自挂载点，商户来自 `Principal.MerchantID`（[transport/rest/change_request.go](../internal/iam/transport/rest/change_request.go#L108-L121)）。

---

## 3. 开发进度与功能缺口

### 3.1 已完成（核心能力闭环）

- RBAC1 角色层级、权限目录、权限码校验与菜单派生。
- RBAC2 互斥组、前置角色、基数约束；创建/改组/启用前预检；绑定事务内 `SELECT ... FOR UPDATE`。
- SoD 申请人与审批人隔离；变更单状态机与目标版本重校验。
- 破玻璃双人审批、白名单、2 小时上限、到期失效。
- 授权快照两级缓存、singleflight、负缓存、故障回源。
- 审计与业务同事务写 Outbox，Outbox 多副本 `ClaimBatch` 防重复投递。
- 数据范围（all/merchant/self/merchant_set）与契约门禁。
- 角色模板、漂移对比、实例化、同步。
- IAM 指标、trace、访问日志、审计事件目录。

### 3.2 部分完成

| 能力 | 现状 | 缺口 |
|------|------|------|
| 审批覆盖 | 角色/绑定/约束/SoD 已覆盖 | **角色模板创建/更新/删除/权限替换/实例化/同步未覆盖**（见 P0-1） |
| 幂等 | 中间件与 Redis 存储已实现 | 生产 `required: false`，漏传即失去保护（见 P1-2） |
| 降级 | IAM 快照 Redis 读失败转 DB 回源 | 平台限流/幂等 fail-closed，Redis 故障时请求在 IAM 之前被拒绝（见 P1-4） |
| 身份联邦 | OIDC 授权码 + PKCE 已实现，并在 staging 用真实 Keycloak 完成冒烟 | SAML 2.0、SCIM、M2M 待交付；JWKS 轮换演练待执行（见 P1-6） |
| 访问复核 | Campaign/Task/Decision 领域、清单生成、决策回收、证据导出已实现 | 季度复核演练、证据归档与责任人规则待完成（见 P1-7） |
| 配置治理 | 生产样例已提供 | 启动期未强制 enforce/approval；样例内含明文密钥（见 P1-1、P1-3） |

### 3.3 未实现（企业级缺项）

- 机器身份/服务账号（API Key、mTLS、OAuth2 client credentials）。
- 访问认证（Access Certification）与权限使用分析。
- 权限变更审批 SLA、超时自动升级、审批委托。
- 角色软删除数据的归档/清理策略。
- 授权决策级审计（allow/deny 维度的请求级留痕）。

---

## 4. 生产级可用性评估

| 能力 | 结论 | 证据与说明 |
|------|------|-----------|
| 多环境适配 | 满足 | 模块化配置 `configs/modules/*` + `configs/prod-*.yaml`；Helm values 覆盖 dev/kind/staging/prod/canary |
| 高可用部署 | 满足 | prod 副本 3、HPA 3–10（worker 3–20）、PDB、topologySpread、NetworkPolicy、ServiceMonitor（[values-prod.yaml](../deploy/helm/admin-frame/values-prod.yaml#L122-L194)、`templates/pdb.yaml#L5`、`templates/hpa.yaml#L5`、`templates/_workload.tpl#L152`） |
| 缓存降级 | 部分满足 | L2 读错误按 miss 处理并回源 DB，写失败不阻断鉴权；但无限流回源保护，Redis 长时间故障会压垮 DB（[cache/snapshot.go](../internal/iam/infrastructure/cache/snapshot.go#L122-L215)） |
| 依赖降级 | 不满足 | 限流 `failure_mode: fail_closed`、幂等存储不可用返回 503；Redis 单点故障放大为全站写不可用（[configs/prod/modules/platform.yaml.example](../configs/prod/modules/platform.yaml.example)、[pkg/idempotency/errors.go](../pkg/idempotency/errors.go#L16-L17)） |
| 异常容错 | 基本满足 | 快照加载超时、singleflight、Outbox 重试与死信、请求体限制、超时中间件、上下文透传 |
| 数据安全 | 满足 | IAM 不落敏感明文；身份域使用 AES-GCM 密文 + HMAC 索引；审计仅存主体 ID 与操作元数据 |
| 敏感信息防护 | 部分满足 | Vault/File/Env Provider + gitleaks/gosec + CORS/限流/体限制；但生产样例含可用密钥（P1-1） |
| 可观测性 | 满足 | 授权结果、缓存命中、快照加载、审批、破玻璃、Outbox 等 12 类指标 + trace（[observability/metrics.go](../internal/iam/observability/metrics.go#L54-L90)） |
| 运维就绪 | 部分满足 | 独立迁移 Job、备份、Runbook 齐备；M5 的 staging 故障演练与容量验证尚未完成（[docs/rbac-iam.md](./rbac-iam.md#L4)） |
| 合规就绪 | 部分满足 | 审计与 4-eyes 基线具备；缺访问复核与决策审计 |

---

## 5. 工程规范评估

### 5.1 实测数据

| 指标 | 数值 |
|------|------|
| IAM Go 文件 | 119 个（其中测试 42 个） |
| IAM 代码行 | 27,555 |
| 测试函数/基准 | 271 |
| 模块总覆盖率（`-coverpkg`） | **57.2%** |
| `go vet ./internal/iam/...` | 通过（0 告警） |
| `golangci-lint run ./internal/iam/...` | **0 issues** |

分包自覆盖率（CI 口径）：

| 包 | 覆盖率 | 评价 |
|----|--------|------|
| `iam/app` | 73.1% | 良好，审批/模板大服务仍有盲区 |
| `iam/domain` | 71.7% | 刚过 70% 门禁，几乎无缓冲 |
| `iam/catalog` | 59.1% | 偏低 |
| `iam/infrastructure/cache` | 77.8% | 良好 |
| `iam/infrastructure/persistence` | **21.3%** | 严重偏低（MySQL 仓储主要靠集成测试） |
| `iam/menuview` | 87.5% | 良好 |
| `iam/observability` | 49.0% | 偏低 |
| `iam/transport/admin` / `biz` | 100% | 仅路由注册，不代表 handler 逻辑 |
| `iam/transport/rest` | 无包内测试；间接覆盖 **40.1%** | handler 逻辑覆盖率不足 |

### 5.2 结论

- **优点**：分层边界清晰且由脚本强制；错误码统一为领域错误 + `apperr` 映射；命名、包组织、注释质量高；依赖面克制；vet/lint 零告警。
- **不足**：
  1. 覆盖率结构失衡，SQL 仓储与 HTTP handler 是主要盲区；CI 仅对 domain 设门槛（[scripts/ci/check-test-gates.sh](../scripts/ci/check-test-gates.sh#L22)），IAM 其余层无门槛。
  2. MySQL 集成测试独立于 `make ci`（[Makefile](../Makefile#L59-L61)），本地与 PR 默认流水线无法发现 SQL 回归。
  3. 单文件过大，职责集中：`change_request_service.go` 984 行、`role_service.go` 650 行、`transport/rest/handler.go` 654 行、`template_service.go` 约 520 行、`mysql_role.go` 581 行。
  4. 缓存失效在请求链路内同步执行，且按主体逐个删除，缺少批量与异步化。
  5. memory 仓储与 MySQL 仓储存在语义差异（内存端手工过滤已删除/过期数据，SQL 端依赖查询条件），测试通过不代表生产等价。

---

## 6. 问题清单与整改建议

> 风险等级定义：**P0** 上线阻断；**P1** 灰度/生产前必须关闭；**P2** 规模化或合规优化项。

### 6.1 P0（上线阻断）

#### P0-1 角色模板写操作绕过审批（4-eyes 失效）

> **修复状态：已修复（2026-11-05）**。新增 `role_template_create/update/delete/permission_replace/instantiate/sync` 六个变更动作与 `iam_role_template` 目标类型；模板写端点接入 `rejectDirectWrite`（`sync` 仅拦截非 dry-run）；变更单 `Apply` 分发到 `TemplateService`，实例化回填新角色 ID。`SyncTemplate` 的批次幂等键未纳入本次修复，随 P1-2 幂等治理统一处理。

- **证据**
  - 模板路由直接注册在 admin/biz 路由，未经过 `rejectDirectWrite`：[transport/admin/router.go](../internal/iam/transport/admin/router.go#L44-L59)、[transport/biz/router.go](../internal/iam/transport/biz/router.go#L77-L91)。
  - 审批拦截只覆盖 `handler.go` 中显式调用 `RequireApproval` 的方法，`template.go` 的创建/更新/删除/权限替换/同步/实例化均未调用。
  - 变更单动作枚举没有模板类动作，无法用审批单表达：[domain/change_request.go](../internal/iam/domain/change_request.go#L80-L102)。
  - `SyncTemplate` 逐角色调用 `ReplacePermissions`，可直接批量改写已有角色权限：[app/template_service.go](../internal/iam/app/template_service.go#L416-L459)；`Instantiate` 直接创建角色：`L343`。
- **影响范围**：全部 A/B 端角色与权限；平台管理员可凭 `iam:role:grant` 单点改写任意商户角色，绕过双人复核。
- **整改方案**
  1. 在变更单动作枚举中新增 `role_template_create/update/delete/permissions_replace/instantiate/sync`。
  2. 为模板端点接入与角色直写相同的审批守卫；审批流通过后在 `Apply` 分支执行对应模板动作。
  3. `SyncTemplate` 增加整体批次标识与幂等键，避免审批通过后重复执行。
- **验收标准**：审批开启时，缺少审批单调用任一模板写端点均返回 40900；审计事件可关联到审批单号；补充对应单测与集成测试。

#### P0-2 权限快照缓存脏写竞态导致撤销延迟可达 L2 TTL

> **修复状态：已修复（2026-11-05）**。失效时先写存活 `InvalidateGuard`（默认 3s）的撤权墓碑再删 L1/L2；`Set` 写前与写后各检查墓碑，命中则拒绝回填并清理刚写入的旧值。补充并发用例验证「在途回源 + 撤权」不会把旧快照写回缓存，撤销可见窗口回到 L1 TTL ≤3s。

- **证据**
  - 鉴权未命中时先读 DB，再无条件写缓存：[app/authorize_service.go](../internal/iam/app/authorize_service.go#L169-L198)。
  - `SnapshotCache.Set` 没有版本比较或“仅在不存在时写入”的约束：[infrastructure/cache/snapshot.go](../internal/iam/infrastructure/cache/snapshot.go#L187-L216)。
  - 写操作在事务提交后删除 L1/L2：[app/role_service.go](../internal/iam/app/role_service.go#L567-L592)。
  - 文档承诺撤销生效 ≤3s，最坏 5 分钟仅在删除失败时发生（[docs/rbac-iam.md](./rbac-iam.md#L28)）。
- **竞态链路**：读者在 T0 读 DB（旧权限）→ 写者在 T1 提交并删除缓存 → 读者在 T2 将旧快照写回 L2 → 其他副本在 L1 过期后读到旧 L2，最长残留 `iam.cache.l2_ttl`（默认 5 分钟）。该场景不需要缓存删除失败即可发生。
- **影响范围**：所有被撤回的角色/绑定；高权限角色撤销后仍可能继续通过鉴权，属于安全一致性缺陷。
- **整改方案**
  1. 为每个主体/角色引入单调修订号（DB 侧 `revision` 或在失效时写入 Redis `epoch:{key}` 短 TTL 墓碑），写入快照前比较修订号，过期数据拒绝回填。
  2. 或改为 `SET NX` + 失效后短暂负缓存，避免旧值覆盖。
  3. 增加并发用例：读请求与撤权并发时，缓存最终必须为撤销后状态。
- **验收标准**：并发撤权测试通过；压测下撤销可见时间 ≤3s；文档与实现一致。

### 6.2 P1（灰度前必须关闭）

| ID | 问题 | 证据 | 影响范围 | 整改方案 | 验收标准 |
|----|------|------|----------|----------|----------|
| P1-1 | 生产配置样例内嵌可用密钥与 root DSN，密钥门禁未覆盖 `configs/prod-*.yaml` | [configs/prod/admin.yaml.example](../configs/prod/admin.yaml.example)；[check-secret-provider.sh](../scripts/ci/check-secret-provider.sh#L102-L115) 仅扫描 `configs/prod/modules/*.example` | 所有环境；误部署即使用公开密钥 | 将“本地演练配置”与“生产样例”拆分；生产样例密钥字段留空并由 Provider 注入；对 `prod-*.yaml` 增加明文/示例值扫描；启动期拒绝已知示例密钥与非生产 DSN | 门禁可拦截；启动期校验失败即失败退出 |
| P1-2 | 幂等键非强制 | [configs/prod/modules/platform.yaml.example](../configs/prod/modules/platform.yaml.example) `required: false`；[pkg/idempotency/middleware.go](../pkg/idempotency/middleware.go#L21-L24) | 审批单、绑定、破玻璃、模板同步等写操作 | 生产开启 `required: true`；前端统一注入 `Idempotency-Key`；对无键写请求返回明确错误码 | 生产配置为 true；E2E 覆盖重复提交仅生效一次 |
| P1-3 | 生产安全开关缺少硬校验 | [internal/platform/config.go](../internal/platform/config.go#L2105-L2202) 仅在 enforce 时校验 Redis/async，未强制 `iam.mode=enforce` 或 `approval.enabled=true`；生产样例当前值正确但无兜底 | 全部 A/B 端；误配可静默降级为 shadow/关闭审批 | 在 prod 校验中硬性要求 `iam.mode=enforce`、`iam.approval.enabled=true`、`idempotency.required=true`；CI 增加生产配置断言 | 篡改任一开关后启动失败、CI 失败 |
| P1-4 | Redis 单点放大为全站写不可用 | [configs/prod/modules/platform.yaml.example](../configs/prod/modules/platform.yaml.example) 限流 fail-closed；[pkg/idempotency/errors.go](../pkg/idempotency/errors.go#L16-L17) 存储不可用返回错误；`pkg/redis` 无 Sentinel/Cluster 客户端 | 所有写请求与登录链路 | 生产使用托管 Redis/集群；增加熔断与本地降级（限流降级为宽松策略并告警，幂等降级为“允许但标记”）；明确 IAM 快照回源限流 | Redis 故障演练中核心写链路可用；告警触发 |
| P1-5 | 测试与门禁覆盖不足 | 实测 57.2% / persistence 21.3% / rest 40.1%；[check-test-gates.sh](../scripts/ci/check-test-gates.sh#L22) 仅 domain ≥70；[Makefile](../Makefile#L59-L61) 集成测试不在 `make ci` | SQL 仓储、HTTP 契约、模板/审批分支 | 对 `iam/infrastructure` 与 `iam/transport` 设独立覆盖率门槛；将 IAM MySQL 集成测试纳入 PR 必跑；补审批、模板、缓存竞态、SQL 边界用例 | 覆盖率门禁生效；PR 缺测试即失败 |
| P1-6 | 企业身份能力缺口 | OIDC 仅预留（[docs/identity-authn.md](./identity-authn.md)）；仓库无 OIDC/SAML/SCIM 实现；无机器身份 | 企业 SSO 接入、自动化开通、服务间调用 | 分阶段实现 OIDC（授权码 + PKCE）→ SAML → SCIM 用户/组同步；M2M 采用 client credentials + 短令牌 | 至少一种 IdP 端到端接入；服务账号可审计轮换 |
| P1-7 | 访问复核无工具化 | [docs/rbac-iam.md](./rbac-iam.md#L1314-L1318) 仅为人工导出流程 | 等保/SOC2 周期性复核证据 | 增加认证周期任务：生成待复核清单、责任人确认、超期自动回收或升级；复核结果写审计 | 认证周期可执行、可导出、有留痕 |

> **修复状态（2026-11-05）**：P1-1～P1-3 已修复；P1-4 已完成应用侧熔断降级，部署侧（托管 Redis/集群、IAM 快照回源限流）待完成；P1-5 门禁已生效，app（≥75%）与 transport/rest（≥60%）的长尾覆盖率目标已达成；P1-6 的 OIDC 阶段已在 staging 用真实 Keycloak 完成端到端冒烟，SAML/SCIM/M2M 待交付；P1-7 工具链已实现，季度复核演练与证据归档待完成。
> - P1-1：`validateSampleCredentials` 在 `env=prod` 拒绝仓库已公开的演练密钥与本地 root DSN；`check-secret-provider.sh` 增加 `configs/prod-*.yaml` 扫描。
> - P1-2：`configs/modules/platform.yaml` 与生产样例改为 `idempotency.required: true`；缺键仍返回 400 `ErrMissingKey`。
> - P1-3：生产 A/B 端启动硬校验 `iam.mode=enforce`、`iam.approval.enabled=true`、`idempotency.enabled/required=true`。
> - P1-4：限流新增连续故障熔断降级（`X-RateLimit-Degraded`），幂等存储不可用时可配置放行并标记（`X-Idempotency-Degraded`）；仍建议生产使用托管 Redis/集群。
> - P1-5：`check-iam-coverage.sh` 以 `-coverpkg` 合并 IAM 全包与 MySQL 集成测试，按包设门槛（domain 80、app 75、catalog 60、cache 75、persistence 50、menuview 90、observability 45、rest 60、admin/biz 90、total 69）；`check-iam-diff-coverage.sh` 校验 PR 变更行 ≥70%；新增角色克隆/删除、绑定解绑、模板增改删、互斥组增改删审批执行用例，以及 rest 包内 SOD/模板/约束/菜单/审批撤销 HTTP 用例。实测（含 MySQL 集成测试）domain 85.6%、app 76.5%、catalog 63.6%、cache 79.6%、persistence 57.3%、menuview 100%、observability 49.0%、rest 63.5%、total 69.9%。
> - P1-6：OIDC 授权码 + PKCE、JWKS 缓存/轮换、显式开关与可观测性已落地；2026-11-05 在 staging 用真实 Keycloak 完成冒烟。JWKS 轮换演练步骤见 [identity-authn.md](./identity-authn.md#L493-L499)；SAML/SCIM/M2M 未交付。
> - P1-7：Campaign/Task/Decision 领域与用例、清单生成、决策回收、证据导出已实现；待完成一次季度复核演练与证据归档。

### 6.3 P2（规模化/合规优化）

| ID | 问题 | 证据 | 整改建议 |
|----|------|------|----------|
| P2-1 | 无授权决策级审计 | 仅有指标计数器（[observability/metrics.go](../internal/iam/observability/metrics.go#L54-L90)）；`POST /authorize` 无审计事件 | 对高风险接口/模拟鉴权记录 allow/deny 决策（含策略版本、trace_id），采用采样与分级保留 |
| P2-2 | 缓存失效同步 N+1 | [app/role_service.go](../internal/iam/app/role_service.go#L567-L592) 每角色递归列出后代并对每个绑定逐个删除 | 引入批量 `DeleteSubject` 管道或 MQ 异步失效；失败入重试队列并告警；对超大角色树设置上限 |
| P2-3 | 模板同步非原子 | [app/template_service.go](../internal/iam/app/template_service.go#L416-L459) 逐角色独立事务，失败中断留下部分同步 | 引入批次号与断点续跑；返回逐角色结果；失败可一键回滚到批次前快照 |
| P2-4 | 大文件复杂度 | `change_request_service.go` 984 行、`role_service.go` 650 行、`handler.go` 654 行、`mysql_role.go` 581 行 | 按用例拆分（起草/审批/应用；角色 CRUD/授权/约束）并保持事务边界不变 |
| P2-5 | memory 与 MySQL 语义差异 | [memory_snapshot.go](../internal/iam/infrastructure/persistence/memory_snapshot.go#L30-L50) 手工过滤 vs SQL 查询条件 | 建立仓储契约测试，同一套用例跑两种实现 |
| P2-6 | 角色软删除无清理策略 | 角色使用 `deleted_at`；worker 仅执行身份清理与审计保留（[cmd/worker/main.go](../cmd/worker/main.go#L134-L147)） | 增加归档/清理任务与保留期配置，清理前校验无有效绑定 |
| P2-7 | 文档与实现漂移 | 文档仍提 `iam_outbox`、`before_hash/after_hash`、未实现的 `approval.required_permissions`/`sod_*` 配置；端点数量仍标注 M3 15 / M4 13，实际约 50 / 32（[docs/rbac-iam.md](./rbac-iam.md#L1463)、[docs/rbac-iam.md](./rbac-iam.md#L4)） | 以代码为准修订文档；在 CI 增加配置项与端点数量断言 |
| P2-8 | 破玻璃默认关闭且无演练 SOP；缺少显式 `constraint_bypass` 标记 | [configs/prod/modules/iam.yaml.example](../configs/prod/modules/iam.yaml.example) `breakglass.enabled: false`；审计中无 `constraint_bypass` 字段 | 提供开通 SOP 与季度演练；破玻璃绑定事件显式标记 `constraint_bypass=breakglass` |
| P2-9 | OpenAPI 无“实现覆盖”漂移检查 | [check-openapi.sh](../scripts/ci/check-openapi.sh#L2) 仅做规范校验与 breaking 对比 | 增加路由表与 OpenAPI path 的自动差集检查，防止已实现未文档化端点 |
| P2-10 | 审计事件权限列表可能膨胀 | 审计记录 `added/removed` 完整权限码列表（[app/audit.go](../internal/iam/app/audit.go)） | 超过阈值时改为数量 + 摘要哈希，并保留可下载明细 |

---

## 7. 整改路线与上线判定

### 7.1 整改顺序

1. **安全控制**：P0-1（审批覆盖）→ P0-2（缓存竞态）。
2. **配置与依赖**：P1-1（密钥治理）→ P1-3（生产开关硬校验）→ P1-2（幂等强制）→ P1-4（Redis 降级）。
3. **质量门禁**：P1-5（覆盖率与集成测试入 PR）。
4. **企业能力**：P1-6（OIDC/SAML/SCIM）→ P1-7（访问复核）→ P2 系列。

### 7.2 上线判定

| 场景 | 判定 | 条件 |
|------|------|------|
| 生产全量上线 | **不通过** | P0 未关闭 |
| staging 灰度 | **有条件通过** | 关闭 P0-1/P0-2；P1-1～P1-4 完成；灰度期监控撤销时延与 503 率 |
| 企业客户接入 | **不通过** | OIDC 已通过 staging 真实验收；另需 SAML/SCIM/M2M 与 P1-7 季度复核演练完成 |

### 7.3 已确认通过的关键项

- 分层依赖由 CI 强制，transport 不直接触达仓储（LAYER-06）。
- 高风险直写统一返回审批引导；申请人与审批人隔离。
- RBAC2 预检 + 事务内 `FOR UPDATE`，并发绑定 TOCTOU 已处理。
- 审计事件与业务写入同事务（Outbox），回滚时审计一并回滚。
- 复合外键、生成列唯一键、撤销历史保留。
- 慢查询防护：快照按主体键读取、无全表扫描；角色层级有深度上限。
- 密钥托管抽象已接入启动路径，Provider 故障 fail-fast。
- `go vet` 与 `golangci-lint` 零告警；IAM 单元测试全部通过。

---

## 8. 附录：审计执行记录

```text
go test ./internal/iam/... -coverprofile=tmp_cov3.out -count=1     # 全部通过
go test ./internal/iam/... -coverpkg=./internal/iam/... \
  -coverprofile=tmp_all2.out -count=1                              # 总覆盖率 57.2%
go test ./internal/iam/transport/admin ./internal/iam/transport/biz \
  -coverpkg=./internal/iam/transport/rest -coverprofile=tmp_rest.out  # rest 间接覆盖 40.1%
go vet ./internal/iam/...                                          # 0 告警
golangci-lint run ./internal/iam/...                               # 0 issues
```

> 说明：本报告仅覆盖 IAM 模块及其直接集成边界；身份域细粒度审计、租户域与平台中间件需另行评估。
</content>
