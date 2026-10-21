# IAM 权限点目录（PERMISSIONS）

> **状态**：生效中（M1 基线，19 条）
> **唯一真相**：`internal/iam/catalog/catalog.go` 的 `entries`；本文件、`internal/iam/perm.go` 与 `migrations/000003_seed_iam_permissions.up.sql` 由 Go 测试强制一致
> **门禁**：`go test ./internal/iam/catalog/...`（已纳入 `make test-ci` → `make ci`）
> **关联**：[`docs/rbac-iam.md`](../rbac-iam.md) §4.3 / §5.3.1、[`docs/adr/0004-rbac-iam-domain.md`](../adr/0004-rbac-iam-domain.md)

---

## 1. 命名规则

| 规则 | 约束 | 原因 |
|------|------|------|
| 字符集 | 全小写 `[a-z0-9_]`，段间用 `:` | 避免大小写歧义；MySQL 排序规则不敏感，应用层再规范化一道 |
| 段数 | 2～4 段 | `resource:action`（如 `order:read`）或 `domain:resource:action`（如 `iam:role:write`） |
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

| code | channel | kind | data_bearing | 说明 |
|------|---------|------|--------------|------|
| `iam:permission:read` | admin | api | 否 | 读取权限点目录，供菜单与授权页展示；不含角色授权关系 |
| `iam:role:read` | both | api | 否 | 查看角色定义与已授予权限；B 端限本商户 |
| `iam:role:write` | both | api | 否 | 创建、更新、停用、软删角色；不包含授权 |
| `iam:role:grant` | both | api | 否 | 替换角色权限点集合；强制 `Idempotency-Key` 并写审计 |
| `iam:binding:read` | both | api | 否 | 查看主体与角色的绑定关系；B 端限本商户 |
| `iam:binding:write` | both | api | 否 | 绑定或解绑主体角色；重复绑定幂等，解绑幂等 |
| `iam:check` | admin | api | 否 | 以指定主体与资源模拟鉴权结果，用于排障与灰度比对；不改变授权 |
| `iam:change:read` | admin | api | 否 | 查看高风险变更审批单与审批记录 |
| `iam:change:approve` | admin | api | 否 | 审批角色 / 绑定变更；申请人不得自审 |
| `iam:breakglass:request` | admin | api | 否 | 申请限时超级权限；必须填写原因与有效期，默认上限 2 小时 |
| `iam:breakglass:approve` | admin | api | 否 | 审批破玻璃申请；与申请人强制分离并触发高危告警 |
| `order:read` | both | api | 是 | 查询订单列表与详情；受数据范围约束 |
| `order:write` | both | api | 是 | 创建、更新、取消订单；受数据范围约束 |
| `file:read` | both | api | 是 | 下载或预览文件；受数据范围约束 |
| `file:write` | both | api | 是 | 上传、替换、删除文件；受数据范围约束 |
| `audit:read` | admin | api | 是 | 查询审计事件；敏感字段脱敏，批量导出需额外 `data:export` |
| `data:export` | both | api | 是 | 批量导出业务数据；必须限流并记录导出审计 |
| `config:read` | admin | api | 否 | 查看运行时配置；敏感项脱敏 |
| `config:write` | admin | api | 否 | 修改运行时配置；须审计并支持回滚 |

> 当前基线无 `menu` / `button` 形态，前端菜单树由后续 PR 追加；追加时 `sort` 取当前最大值 + 10，且不得改动既有条目。

---

## 3. 数据范围与权限点的关系

功能权限与数据范围**正交**，判定顺序为：

1. 主体是否拥有该 `resource:action`（功能权限）；
2. 若 `data_bearing = 是`，再按 `iam_role_data_scope` 收缩到 `all` / `merchant` / `self` / `merchant_set`；
3. 两步任一不满足即拒绝（默认拒绝）。

因此 `order:read` 与 `order:write` 可以共享同一条数据范围记录（`resource=order`），避免为每个动作重复配置。

---

## 4. 变更流程（硬约束）

新增或废弃权限点必须一次 MR 内完成以下四处修改：

1. `internal/iam/catalog/catalog.go`：追加 `entries` 条目（唯一真相）；
2. `internal/iam/perm.go`：新增 `Perm*` 常量；
3. `migrations/000003_seed_iam_permissions.up.sql`：追加一行（行首为 `('`）；
4. 本文件：追加表格行。

`internal/iam/catalog` 的单元测试会校验：

- 权限码格式、唯一性、`sort` 严格递增、父节点存在；
- 每个 `Perm*` 常量都在目录中，且目录中每一条都有常量；
- 种子迁移的 `code / name / channel / resource / action / kind / data_bearing` 与目录完全一致；
- 本文件表格的 `code / channel / kind / data_bearing` 与目录完全一致；
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
| admin | `ops` | `order:read` / `order:write` / `file:read` / `file:write` | `all` 或 `merchant_set` |
| admin | `breakglass_admin` | 目录中不授予任何权限；仅在有效审批绑定时展开 `*` | `all` |
| biz | `merchant_owner` | `iam:role:read` / `write` / `grant`、`iam:binding:read` / `write`、`order:*`、`file:*` | `merchant` |
| biz | `merchant_staff` | `order:read`、`file:read` | `merchant` |

`system_admin` 不含 `iam:breakglass:approve`，以满足「申请 ≠ 审批」；破玻璃审批人单独配置。

---

## 6. 本地校验

```bash
go test ./internal/iam/catalog/... -run TestPermissionCatalog -count=1 -v
make check-migrations   # 迁移命名与 up/down 配对
```

> 目录一致性刻意用 Go 测试而不是 shell 解析：Go 可直接复用 `catalog` 与 `perm` 的类型、给出结构化差异，且不受 shell 引号 / 编码（Windows + 中文）影响。
