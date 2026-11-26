# IAM 权限点目录（PERMISSIONS）

> **状态**：生效中（50 条：46 条活跃 + 4 条已弃用）
> **唯一真相**：`internal/iam/catalog/catalog.go` 的 `entries`；本文件、`internal/iam/perm.go` 与 `migrations/*_seed_*.up.sql` 由 Go 测试强制一致
> **门禁**：`go test ./internal/iam/catalog/...`（已纳入 `make test-ci` → `make ci`）
> **关联**：[`docs/rbac-iam.md`](../rbac-iam.md) §4.3 / §5.3.1、[`docs/adr/0004-rbac-iam-domain.md`](../adr/0004-rbac-iam-domain.md)

---

## 1. 命名规则

| 规则 | 约束 | 原因 |
|------|------|------|
| 字符集 | 全小写 `[a-z0-9_]`，段间用 `:` | 避免大小写歧义；MySQL 排序规则不敏感，应用层再规范化一道 |
| 段数 | 2～4 段 | `resource:action`（如 `iam:role:read`）或 `domain:resource:action`（如 `iam:role:write`） |
| 通配符 | **禁止** `*` | 超级权限只能由 `breakglass` 角色在审批有效期内动态展开，不能靠一条目录记录长期存在 |
| 语义 | 首段 = 资源，末段 = 动作 | 数据范围按资源匹配；动作决定读写与审计级别 |
| 稳定性 | 发布后**不得改名、不得复用** | 权限码是跨版本契约；复用会让历史审计与旧令牌产生错误解释 |
| 长度 | ≤ 128 字节 | 与 `iam_permission.code` 列宽一致 |

`channel` 取值：

- `admin`：仅 A 端（平台）可授予；
- `biz`：仅 B 端（商户）可授予；
- `both`：两端均可授予，实际可见范围仍受端别与 `merchant_id` 隔离约束。

`data_bearing = 是` 表示授予该权限时**必须**同时配置数据范围（`iam_role_data_scope`），否则授权请求返回 `40000`。

---

## 2. 目录基线

| code | channel | data_bearing | 状态 | 说明 |
|------|---------|--------------|------|------|
| `iam:permission:read` | admin | 否 | 活跃 | 读取权限点目录，供菜单与授权页展示；不含角色授权关系 |
| `iam:role:read` | both | 否 | 活跃 | 查看角色定义与已授予权限；B 端限本商户 |
| `iam:role:write` | both | 否 | 活跃 | 创建、更新、停用、软删角色；不包含授权 |
| `iam:role:grant` | both | 否 | 活跃 | 替换角色权限点集合；强制 `Idempotency-Key` 并写审计 |
| `iam:binding:read` | both | 否 | 活跃 | 查看主体与角色的绑定关系；B 端限本商户 |
| `iam:binding:write` | both | 否 | 活跃 | 绑定或解绑主体角色；重复绑定幂等，解绑幂等 |
| `iam:check` | admin | 否 | 活跃 | 以指定主体与资源模拟鉴权结果，用于排障与灰度比对；不改变授权 |
| `iam:change:read` | admin | 否 | 活跃 | 查看高风险变更审批单与审批记录 |
| `iam:change:approve` | admin | 否 | 活跃 | 审批角色 / 绑定变更；申请人不得自审 |
| `iam:breakglass:request` | admin | 否 | 活跃 | 申请限时超级权限；必须填写原因与有效期，默认上限 2 小时 |
| `iam:breakglass:approve` | admin | 否 | 活跃 | 审批破玻璃申请；普通主体与申请人强制分离；system_admin 可自批 |
| `order:read` | both | 是 | 已弃用 | 历史遗留权限；保留既有授权，禁止新授 |
| `order:write` | both | 是 | 已弃用 | 历史遗留权限；保留既有授权，禁止新授 |
| `file:read` | both | 是 | 已弃用 | 历史遗留权限；保留既有授权，禁止新授 |
| `file:write` | both | 是 | 已弃用 | 历史遗留权限；保留既有授权，禁止新授 |
| `audit:read` | admin | 是 | 活跃 | 查询审计事件；敏感字段脱敏，批量导出需额外 `data:export` |
| `data:export` | both | 是 | 活跃 | 批量导出业务数据；必须限流并记录导出审计 |
| `config:read` | admin | 否 | 活跃 | 查看配置中心普通配置；敏感项脱敏，不返回密钥元数据与明文 |
| `config:write` | admin | 否 | 活跃 | 创建 / 修改配置草稿；须审计，不包含发布、审批与密钥操作 |
| `config:publish` | admin | 否 | 活跃 | 发布配置 Release 与回滚；与审批分离，须审计 |
| `config:approve` | admin | 否 | 活跃 | 审批配置发布；申请人不得自审 |
| `config:secret:read` | admin | 否 | 活跃 | 查看业务密钥列表、版本与授权元数据；不返回明文、密文或 wrapped DEK |
| `config:secret:write` | admin | 否 | 活跃 | 创建、轮换、吊销业务密钥并提交审批；明文一次性输入，永不回显 |
| `config:secret:approve` | admin | 否 | 活跃 | 审批密钥创建、轮换、吊销与授权；提交人不得自审 |
| `iam:change:request` | biz | 否 | 活跃 | 发起本商户变更申请，并查看本商户待办列表；`merchant_id` 只能来自 Principal |
| `iam:change:review` | biz | 否 | 活跃 | 审批本商户变更申请；申请人不得自审（SoD） |
| `iam:menu:read` | admin | 否 | 活跃 | 查看菜单 / 路由资源管理列表；不改变前端可见性 |
| `iam:menu:write` | admin | 否 | 活跃 | 创建、更新、停用菜单资源；仅影响入口展示，不构成鉴权边界 |
| `iam:sod:read` | both | 否 | 活跃 | 查看 RBAC2 互斥角色组及其存量违规；B 端限本商户 |
| `iam:sod:write` | both | 否 | 活跃 | 创建、更新、停用互斥角色组；变更前强制存量违规预检 |
| `identity:account:read` | admin | 否 | 活跃 | 查询账号列表 / 详情与目标账号会话；不包含任何处置动作 |
| `identity:account:write` | admin | 否 | 活跃 | 停用、启用、锁定、解锁、软删恢复；不包含凭据重置、删除与会话撤销 |
| `identity:account:credential` | admin | 否 | 活跃 | 重置目标账号密码 / MFA；账号接管高风险动作，须与生命周期权限分离 |
| `identity:account:delete` | admin | 否 | 活跃 | 软删目标账号；不可逆操作入口，不包含恢复与会话撤销 |
| `identity:account:session` | admin | 否 | 活跃 | 撤销目标账号指定会话或全部会话；不包含凭据与生命周期变更 |
| `identity:m2m:read` | admin | 否 | 活跃 | 查看 M2M 客户端列表 / 状态 / scope；不返回任何密钥材料 |
| `identity:m2m:write` | admin | 否 | 活跃 | 创建、停用、启用、软删 M2M 客户端；不包含密钥轮换 |
| `identity:m2m:credential` | admin | 否 | 活跃 | 轮换 M2M 客户端密钥（返回一次性明文）；与 write 分离以落实职责分离 |
| `identity:scim:read` | admin | 否 | 活跃 | 查看 SCIM 令牌指纹与组映射状态；不返回令牌原文 |
| `identity:scim:write` | admin | 否 | 活跃 | 签发与吊销 SCIM 令牌 |
| `tenant:read` | admin | 否 | 活跃 | 查询租户列表与详情（含状态与状态原因）；不包含任何状态变更 |
| `tenant:write` | admin | 否 | 活跃 | 创建租户与迁移租户状态（启用 / 停用 / 恢复 / 终止）；每次迁移写审计 |
| `tenant:approve` | admin | 否 | 活跃 | 入驻审核裁决（通过 / 驳回）；与 `tenant:write` 拆开授予，且领域层拒绝「提交人审批自己的申请」（职责分离） |
| `notification:read` | admin | 否 | 活跃 | 查询通知单、渠道投递明细与模板版本历史；正文与收件人明文不在任何视图暴露 |
| `notification:write` | admin | 否 | 活跃 | 代发通知（notification.send 审计）、DLQ 死信补偿重放（notification.dlq.replay 审计）与模板注册 / 停用 |
| `featureflag:read` | admin | 否 | 活跃 | 查询特性开关列表、灰度规则与遗留 flag 台账（永久 / 即将到期 / 已过期分组） |
| `featureflag:write` | admin | 否 | 活跃 | 创建 / 变更 / 启停 / 删除特性开关（featureflag.* 审计）；kill switch 秒级生效，变更全部留痕 |
| `iam:certification:read` | admin | 否 | 活跃 | 查看访问复核活动、任务清单并导出证据（CSV / JSON）；不包含裁决与回收 |
| `iam:certification:write` | admin | 否 | 活跃 | 创建 / 激活 / 关闭复核活动并生成任务清单；激活幂等，不覆盖已裁决任务 |
| `iam:certification:decide` | admin | 否 | 活跃 | 确认保留或驳回回收复核任务；驳回前二次校验绑定指纹，与 write 分离以落实职责分离 |

> `order:*` / `file:*` 为历史遗留权限点：目录条目与常量保留以解释既有授权与审计，
> 状态统一为 `deprecated`（随目录种子 `migrations/000003_seed_iam_baseline.up.sql` 写入）。
>
> 权限目录为平铺结构，不含层级：`resource`（权限码首段）由服务端生成并为生成列，
> 菜单与路由树由 `iam_menu` 承载。追加条目时 `sort` 取当前最大值 + 10，且不得改动既有条目。
>
> `iam:menu:read` / `iam:menu:write` 仅控制 A 端菜单资源的管理入口；菜单可见性仍由各菜单的
> `permission_code` 决定，接口鉴权仍以权限点中间件为准。

---

## 3. 数据范围与权限点的关系

功能权限与数据范围**正交**，判定顺序为：

1. 主体是否拥有该 `resource:action`（功能权限）；
2. 若 `data_bearing = 是`，再按 `iam_role_data_scope` 收缩到 `all` / `merchant` / `self` / `merchant_set`；
3. 两步任一不满足即拒绝（默认拒绝）。

因此同一资源下的多个动作（如 `iam:role:read` 与 `iam:role:write`）可以共享同一条数据范围记录（`resource=iam`），避免为每个动作重复配置。

---

## 4. 变更流程（硬约束）

新增或废弃权限点必须一次 MR 内完成以下四处修改：

1. `internal/iam/catalog/catalog.go`：追加 `entries` 条目（唯一真相）；
2. `internal/iam/perm.go`：新增 `Perm*` 常量；
3. `migrations/*_seed_*.up.sql`：新增一行（行首为 `('`；只追加，不改写已应用的种子）；
4. 本文件：追加表格行。

`internal/iam/catalog` 的单元测试会校验：

- 权限码格式、唯一性、`sort` 严格递增；
- 每个 `Perm*` 常量都在目录中，且目录中每一条都有常量；
- 种子迁移的 `code / name / channel / data_bearing / sort` 与目录完全一致；
- 本文件表格的 `code / channel / data_bearing` 与目录完全一致；
- down 迁移覆盖全部种子 `code`。

任一处遗漏都会失败并打印差异，修复方向即上述四处。

### 废弃而非删除

- 将 `status` 改为 `deprecated`，保留既有授权；新授权请求拒绝该权限码；
- 常量与目录条目**不得删除**，否则历史审计无法解释；
- 至少在两个发布周期、确认 `iam_role_permission` 无引用后，才可通过新迁移清理。

---

## 5. 系统角色（非目录）

系统角色是**授权的组合**，不是权限点，因此不在本目录：

| channel | code | 权限 | 数据范围 |
|---------|------|------|----------|
| admin | `system_admin` | 全部 `iam:*` 管理权限 + `audit:read` + `config:read` / `config:write` | `all` |
| admin | `ops` | `iam:permission:read` / `iam:role:read` / `iam:binding:read` / `iam:change:read` / `audit:read` / `config:read` | `all` 或 `merchant_set` |
| admin | `breakglass_admin` | 目录中不授予任何权限；仅在有效审批绑定时展开 `*` | `all` |
| biz | `merchant_owner` | `iam:role:read` / `write` / `grant`、`iam:binding:read` / `write`、`iam:change:request` / `review` | `merchant` |
| biz | `merchant_staff` | `iam:change:request` | `merchant` |

> B 端变更审批的 SoD：`merchant_staff` 只有 `iam:change:request`（能提不能批），
> `merchant_owner` 同时持有 request / review，但领域层强制「申请人 ≠ 审批人」，
> 因此同一 owner 仍需第二个具备 review 的主体才能完成审批。

`system_admin` 持有平台全量权限（含 `iam:breakglass:approve`）；高风险操作可直写或自批，
应用层记 `sod_bypass` 审计。普通主体仍须申请人 ≠ 审批人。

---

## 6. 本地校验

```bash
go test ./internal/iam/catalog/... -run TestPermissionCatalog -count=1 -v
make check-migrations   # 迁移命名与 up/down 配对
```

> 目录一致性刻意用 Go 测试而不是 shell 解析：Go 可直接复用 `catalog` 与 `perm` 的类型、给出结构化差异，且不受 shell 引号 / 编码（Windows + 中文）影响。
