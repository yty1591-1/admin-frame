# web-admin IAM 前端全量对接实施计划

> 状态：已实施（2026-10-28，验收结果见 §10）
> 范围：`frontend/apps/web-admin` 全量 IAM 页面 + 契约补齐（角色模板）+ 菜单种子迁移
> 关联：`docs/FRONTEND_INTEGRATION_REVIEW.md` FEINT-06 ~ FEINT-13、GAP-04 ~ GAP-08；`docs/rbac-iam.md`
> 已确认决策：仅 A 端（web-admin）；全量 IAM；允许新增迁移启用 `iam.*` 菜单

---

## 1. 摘要

后端 IAM 域已具备角色、权限目录、绑定、审批、破玻璃、SoD、角色模板、菜单资源管理的完整生产能力，但前端目前只有一个基于 Nitro Mock 的「角色列表」页面，且 `iam.*` 菜单种子全部为 `disabled`。

本计划把 web-admin 的 IAM 前端一次性补齐，按「基础设施 → 核心 RBAC → 治理能力 → 模板/菜单」三批实施，最终交付：

```text
系统管理
  ├─ 角色管理（真实 API：CRUD / 继承 / 授权 / 数据范围 / 约束 / 克隆）
  └─ 菜单管理（菜单树 / 新建 / 编辑 / 启停 / 页面组件选择）

权限管理
  ├─ 权限目录（只读）
  ├─ 授权绑定（查询 / 绑定 / 解绑 / 主体权限快照）
  ├─ 角色模板（CRUD / 权限基线 / 漂移 / 同步 / 实例化）
  ├─ 变更审批（列表 / 详情 / 通过 / 拒绝 / 撤销 / 应用 / 新建）
  ├─ 破玻璃申请（申请 / 审批 / 撤销）
  ├─ 互斥角色组 SoD（CRUD / 预检 / 违规清单）
  └─ 模拟鉴权（authorize 判定）
```

---

## 2. 现状分析

### 2.1 前端

| 位置 | 现状 |
|---|---|
| `src/views/system/role/index.vue` | 只读列表，字段 `name/status(0\|1)/remark/createTime`，数据来自 Nitro Mock |
| `src/api/system/role.ts` | `GET /system/role/list`，Mock 分页参数 `pageSize` |
| `src/views/iam/**` | 不存在 |
| `src/router/routes/modules` | 只有 `profile.ts`；页面路由完全由后端菜单驱动（`accessMode: 'backend'`） |
| 菜单种子 | `iam.permission/binding/template/change/breakglass/check`、`system.menu` 均为 `disabled` |
| 权限指令 | `v-access:code` 已注册（`@vben/access`），可直接用于按钮/操作显隐 |
| 弹窗能力 | `@vben/common-ui` 已导出 `useVbenModal`、`useVbenDrawer`、`useVbenForm`、`Page` |

### 2.2 后端

| 能力 | 端点 | 契约状态 |
|---|---|---|
| 权限目录 | `GET /iam/permissions` | 有 |
| 角色 CRUD / 授权 / 约束 / 克隆 | `/iam/roles*` | 有 |
| 绑定 / 主体权限 / 模拟鉴权 | `/iam/bindings*`、`/iam/subjects/...`、`/iam/authorize` | 有 |
| 审批单 | `/iam/change-requests*` | 有 |
| 破玻璃 | `/iam/breakglass*` | 有（无列表端点） |
| SoD | `/iam/sod-groups*` | 有（无菜单种子） |
| 角色模板 | `/iam/role-templates*` 9 个端点 | **后端已实现，OpenAPI 未登记** |
| 菜单管理 | `/iam/menus*` | 有 |

### 2.3 关键缺口

1. **角色模板契约缺失**：`internal/iam/transport/admin/router.go:42-59` 已注册端点，`api/openapi` 无对应 path/schema；前端无法按契约生成，需先补契约。
2. **破玻璃无列表端点**：只能通过审批单列表 `GET /iam/change-requests?action=breakglass_request` 展示；`BreakglassGrant.id` 即审批单 ID（`breakglass.go:44-56`），审批/撤销可直接复用。
3. **A 端无商户目录端点**：`data_scopes.scope_type=merchant_set` 的 `merchant_ids` 无法做商户选择器；本期用标签输入（≤100）兜底。
4. **审批拦截与版本冲突同为 40900**：前端不做文案解析，统一弹「刷新详情 / 提交审批单」二选一。

---

## 3. 目标与非目标

### 目标

1. 角色页从 Mock 切到真实 `/iam/roles`，支持完整生命周期与 RBAC1/RBAC2 能力。
2. 新增权限目录、绑定、模板、审批、破玻璃、SoD、模拟鉴权、菜单管理页面。
3. 所有写操作正确处理 `version` 乐观锁、409（冲突/审批）、403、404 语义。
4. `iam.*` 菜单通过新迁移启用，登录后侧边栏可见。
5. 按钮与页面入口按 `iam:*` 权限码显隐。

### 非目标

1. web-biz 的 `member/*` 页面。
2. 审计日志、系统配置、文件上传（后端无端点，另开任务）。
3. 商户目录/选择器（无端点，本期标签输入）。
4. 菜单变更的实时热更新（菜单写入后需刷新页面或重新登录）。
5. 登录、MFA、会话管理（已于上一阶段完成）。

---

## 4. 总体设计

### 4.1 前端分层

```text
views/…            页面 + 业务交互（不直接拼 URL）
api/iam/*          端点封装、分页映射、DTO 类型、错误语义
api/iam/types.ts   全部 IAM DTO（与 OpenAPI 一一对应，手写窄类型）
views/iam/components  权限选择器、数据范围编辑器、角色选择器、409 兜底弹窗
```

### 4.2 错误与冲突约定

| 场景 | 识别 | 前端行为 |
|---|---|---|
| 版本冲突 | 409 + 操作含 `version` | 弹「数据已被他人修改」→ 刷新详情，不自动重试 |
| 审批拦截 | 409 + 写操作 | 弹「可能需走审批」→ 两个按钮：刷新详情 / 提交审批单（预填 action + payload） |
| code 重复 | 409 + 创建角色/模板 | 聚焦 `code` 字段并提示 |
| 404 | 详情/操作 | 提示「资源不存在或无权限访问」，不跳全局 404 |
| 400 | 参数 | 展示后端 `message`，定位字段 |

统一封装 `api/iam/errors.ts`：

```ts
export function isConflict(error: unknown): boolean
export function isNotFound(error: unknown): boolean
export function getConflictMessage(error: unknown): string
```

### 4.3 分页

`api/iam/types.ts` 提供：

```ts
export interface PageQuery { page?: number; pageSize?: number }
export function toPageQuery(q: PageQuery & Record<string, unknown>) {
  const { page = 1, pageSize = 20, ...rest } = q;
  return { page, page_size: Math.min(pageSize, 100), ...rest };
}
```

### 4.4 权限显隐

| 操作 | 指令 |
|---|---|
| 新增/编辑/删除角色、约束、克隆 | `v-access:code="'iam:role:write'"` |
| 授权（权限/数据范围） | `v-access:code="'iam:role:grant'"` |
| 绑定/解绑 | `v-access:code="'iam:binding:write'"` |
| 审批通过/拒绝/应用 | `v-access:code="'iam:change:approve'"` |
| 破玻璃申请 | `v-access:code="'iam:breakglass:request'"` |
| 破玻璃审批/撤销 | `v-access:code="'iam:breakglass:approve'"` |
| SoD 写操作 | `v-access:code="'iam:sod:write'"` |
| 菜单写操作 | `v-access:code="'iam:menu:write'"` |
| 模板写操作 | `v-access:code="'iam:role:write'"` |

---

## 5. 改动清单

### 5.1 契约：补齐角色模板（后端已实现）

**新增** `api/openapi/paths/admin/iam-role-templates.yaml`：

| 方法 | 路径 | 请求 | 响应 |
|---|---|---|---|
| GET | `/admin/v1/iam/role-templates` | `channel/status/keyword/page/page_size` | `RoleTemplateListData` |
| POST | 同上 | `CreateRoleTemplateRequest` | `RoleTemplate` |
| GET | `/{id}` | — | `RoleTemplate` |
| PATCH | `/{id}` | `UpdateRoleTemplateRequest`（`version` 必填） | `RoleTemplate` |
| DELETE | `/{id}` | `DeleteRoleTemplateRequest`（`version`） | `IdOnly` |
| PUT | `/{id}/permissions` | `ReplaceTemplatePermissionsRequest` | `RoleTemplate` |
| GET | `/{id}/drift` | — | `TemplateDriftListData` |
| POST | `/{id}/sync` | `SyncTemplateRequest` | `TemplateDriftListData` |
| POST | `/{id}/instantiate` | `InstantiateTemplateRequest` | `Role` |

**新增** `api/openapi/schemas/admin/iam-role-templates.yaml`，字段严格对齐 `internal/iam/transport/rest/template.go:16-90`：

- `RoleTemplate`: `id/channel/code/name/description/status/version/permissions/data_scopes/created_by/updated_by/created_at/updated_at`
- `RoleTemplateListData`、`TemplateDrift`、`TemplateDriftListData`
- `CreateRoleTemplateRequest`、`UpdateRoleTemplateRequest`、`ReplaceTemplatePermissionsRequest`、`DeleteRoleTemplateRequest`、`SyncTemplateRequest`、`InstantiateTemplateRequest`
- `status` 枚举 `active|disabled`；`channel` 复用 `common.yaml#Channel`

**修改** `api/openapi/admin.yaml`：

- 增加 6 条 path 装配；
- `info.version` 由 `5.1.0` 升至 `6.0.0`：实施中发现后端要求角色 `scope_type` 为
  `platform|merchant`，原契约枚举 `admin|merchant` 属登记错误，按 API-07 提升 major 更正；
- 描述追加 `6.0.0：角色 scope_type 枚举由 admin 更正为 platform（破坏性）；登记角色模板端点与契约。`

**验证**：`make openapi` 通过（major 已提升，oasdiff 允许 intentional breaking）；
`make openapi-bundle` 产物含 7 处 role-templates 路径。

### 5.2 迁移：启用 IAM 菜单

**新增** `migrations/000008_enable_iam_pages.up.sql`：

```sql
UPDATE iam_menu
SET status = 'active', updated_by = 'seed', updated_at = UTC_TIMESTAMP(3)
WHERE channel = 'admin'
  AND code IN ('system.menu','iam.permission','iam.binding','iam.template',
               'iam.change','iam.breakglass','iam.check');

INSERT INTO iam_menu
  (id, channel, code, parent_code, node_type, route_name, route_path, component,
   redirect, title, title_key, icon, permission_code, sort, hide_in_menu,
   keep_alive, external_url, status, updated_by, created_at, updated_at)
VALUES
  (1910000000000000117, 'admin', 'iam.sod', 'iam', 'menu', 'IamSod', 'sod',
   '/iam/sod/index.vue', '', '互斥角色组', 'page.iam.sod', 'lucide:shield-alert',
   'iam:sod:read', 55, 0, 0, '', 'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
ON DUPLICATE KEY UPDATE
  status = 'active', updated_by = 'seed', updated_at = UTC_TIMESTAMP(3);
```

**新增** `migrations/000008_enable_iam_pages.down.sql`：

- `iam.sod` 置 `disabled`（不物理删除，保留历史）；
- 其余 7 个 code 置 `disabled`。

**不修改** `000003_seed_iam_baseline.up.sql`（迁移已应用，改 checksum 会破坏历史）。

### 5.3 前端 API 层

**删除** `src/api/system/role.ts`，**新增**：

| 文件 | 内容 |
|---|---|
| `src/api/iam/types.ts` | 全部 DTO、`PageQuery/PageResult`、`toPageQuery`、枚举联合类型 |
| `src/api/iam/errors.ts` | `isConflict/isNotFound/getConflictMessage` |
| `src/api/iam/permission.ts` | `listPermissionsApi`、`listAllPermissionsApi`（按 100/页遍历） |
| `src/api/iam/role.ts` | list/get/create/update/remove/replacePermissions/getConstraints/putConstraints/clone |
| `src/api/iam/binding.ts` | list/create/unbind/getSubjectPermissions |
| `src/api/iam/authorize.ts` | `simulateAuthorizeApi` |
| `src/api/iam/change-request.ts` | list/create/approve/reject/cancel/apply |
| `src/api/iam/breakglass.ts` | request/approve/revoke（列表复用 change-request） |
| `src/api/iam/sod.ts` | list/get/create/update/remove/preflight/listViolations |
| `src/api/iam/template.ts` | list/get/create/update/remove/replacePermissions/drift/sync/instantiate |
| `src/api/iam/menu.ts` | list/tree/components/get/create/update/disable/enable |
| `src/api/iam/index.ts` | 统一导出 |

**修改** `src/api/index.ts`：`export * from './iam'`。

约定：

- 路径统一相对 baseURL `/admin/v1`，如 `/iam/roles`。
- 写操作把 `version` 放 body；`reason` 按表单透传。
- 删除角色/模板/SoD/菜单使用对应请求体，不使用 204 语义（IAM 端点均返回 200 信封）。
- `listAllPermissionsApi` 以 `page_size=100` 循环拉全量，供选择器分组。

### 5.4 共享组件

| 文件 | 职责 |
|---|---|
| `views/iam/components/permission-picker.vue` | 全量权限按 `resource` 分组、全选/反选、搜索、`v-model:value="string[]"` |
| `views/iam/components/data-scope-editor.vue` | 行编辑 `{resource, scope_type, merchant_ids}`；A 端仅 `all/merchant_set`；商户 ID 标签输入（≤100） |
| `views/iam/components/role-select.vue` | 远程搜索角色（channel=admin、active、keyword），返回 `{id, code, name}` |
| `views/iam/components/subject-input.vue` | `subject_type=staff` 固定 + `subject_id` 输入 + 「查看主体权限」入口 |
| `views/iam/components/conflict-modal.vue` | 409 兜底：刷新详情 / 提交审批单；接收 `action/targetId/payload` |
| `views/iam/components/json-preview.vue` | payload 只读展示（审批详情、模拟鉴权结果） |

### 5.5 页面

#### 5.5.1 角色管理 `views/system/role/index.vue`（重写）

- 列表：`keyword/status` + `page/page_size`；列 `code/name/role_type/status/parent_id/binding_count(详情)/updated_at`。
- 新建：`code/name/description/scope_type=admin/parent_id/reason`。
- 编辑：`name/description/status/reason/version`；`role_type=system|breakglass` 禁止编辑/删除。
- 授权抽屉：`permission-picker` + `data-scope-editor` → `PUT /roles/{id}/permissions`，展示 `added/removed`。
- 约束弹窗：`max_bindings`（null=不限）+ `required_role_ids` → `PUT /roles/{id}/constraints`。
- 克隆：`code/name` → `POST /roles/{id}/clone`，提示新角色为 `disabled`。
- 删除：确认框展示 `binding_count`，body `{version, reason}`；409 走冲突弹窗。
- 所有写操作按 5.1 权限码显隐。

#### 5.5.2 权限目录 `views/iam/permission/index.vue`

- 只读表格：`code/name/channel/resource/data_bearing/status/sort`。
- 筛选：`keyword/channel`；分页 ≤100。
- 顶部说明：目录由迁移维护，页面不提供写操作。

#### 5.5.3 授权绑定 `views/iam/binding/index.vue`

- 查询二选一：按角色（`role-select`）或按主体（`staff` + `subject_id`），未选不发请求。
- 列表列：`subject_id/role_id/expires_at/granted_by/created_at`。
- 新建：`role_id/subject_id/expires_at/reason`。
- 解绑：确认框 + `reason`；`DELETE /bindings/{id}`。
- 「查看主体权限」抽屉：`GET /iam/subjects/staff/{subject_id}/permissions`，展示 roles/permissions/data_scopes/snapshot_version。

#### 5.5.4 模拟鉴权 `views/iam/check/index.vue`

- 表单：`subject_type=staff`、`subject_id`、`permission`（权限目录选择）、可选 `resource.type/id/merchant_id`。
- 结果：`allowed` Tag、`reason`、`matched_roles`、`data_scope`、`snapshot_version`，并保留 JSON 预览。

#### 5.5.5 变更审批 `views/iam/change/index.vue`

- 列表：`status/action/requested_by` + 分页；列 `action/target_type/status/requested_by/requested_at/version`。
- 详情抽屉：`reason/payload/approved_by/approved_at/applied_at` + 状态时间线。
- 操作：通过/拒绝（`reason+version`）、撤销（申请人本人）、应用（`approved` 才可）。
- 「新建审批单」：`action` 下拉 + `target_id` + `payload` JSON 编辑器（按 action 提供模板占位）+ `reason`；前端校验 JSON 与 16 KiB 上限。

#### 5.5.6 破玻璃 `views/iam/breakglass/index.vue`

- 申请表单：`subject_id/duration_seconds`（1~7200，默认 1800）+ `reason`。
- 列表：`GET /iam/change-requests?action=breakglass_request`，从 `payload` 映射 `subject_id/duration_seconds`；列 `status/requested_by/requested_at/expires_at`。
- 审批：`POST /iam/breakglass/{id}/approve`（id=审批单 ID）；拒绝走 `change-requests/{id}/reject`；撤销走 `/breakglass/{id}/revoke`。
- 403（`iam.breakglass.enabled=false`）展示空态说明「当前实例未开启破玻璃」。

#### 5.5.7 SoD `views/iam/sod/index.vue`

- 列表：`keyword/status` + 分页；列 `code/name/max_active_roles/role_ids/status/version`。
- 新建/编辑：`code/name/description/max_active_roles/role_ids`（≥2）+ `reason`；编辑携带 `version`。
- 预检：选择 `role_ids` 后调 `POST /sod-groups/preflight`，弹窗展示违规主体。
- 违规清单：`GET /sod-groups/{id}/violations`。
- 删除：`DELETE` + `version/reason`。

#### 5.5.8 角色模板 `views/iam/template/index.vue`

- 列表：`channel/status/keyword`；列 `code/name/channel/status/version/updated_at`。
- 新建/编辑：`channel/code/name/description`；权限与数据范围通过抽屉（`PUT /permissions`）。
- 漂移：`GET /{id}/drift` 展示实例角色与模板版本差异。
- 同步：`POST /{id}/sync {dry_run, reason}`，先 dry-run 预览再确认执行。
- 实例化：`POST /{id}/instantiate {scope_type, code, name, reason}`，返回新角色。

#### 5.5.9 菜单管理 `views/system/menu/index.vue`

- 树表：`GET /iam/menus/tree?include_disabled=true`，支持展开/收起；列 `title/code/route_path/component/permission_code/status/sort/version`。
- 新建/编辑：`node_type/route_name/route_path/component`（从 `GET /iam/menus/components` 下拉）/`parent_code`（树选择）/`title/title_key/icon/permission_code/sort/hide_in_menu/keep_alive/external_url`；`code` 创建后不可改。
- 启停：`DELETE /menus/{id}`（停用）+ `POST /menus/{id}/enable`，均带 `version/reason`。
- 提示：菜单只影响入口展示；变更后刷新页面或重新登录生效。

### 5.6 国际化

`frontend/apps/web-admin/src/locales/langs/{zh-CN,en-US}/page.json` 的 `iam` 下新增：

```json
"sod": "互斥角色组"
```

其余页面操作文案沿用仓库现状使用中文常量，不新增 i18n 键。

---

## 6. 假设与决策

1. **角色模板走「先补契约」**：后端 9 个端点已实现，OpenAPI 未登记；本计划先补契约再写页面，避免前端继续「按实现猜字段」。
2. **破玻璃列表复用审批单**：无独立列表端点；`BreakglassGrant.id = ChangeRequest.id`，审批/撤销直接使用该 ID。
3. **409 不解析文案**：版本冲突与审批拦截同为 `40900`，统一弹「刷新详情 / 提交审批单」二选一，避免依赖中文 message。
4. **商户选择器暂用标签输入**：A 端无商户目录端点；`merchant_set` 的 `merchant_ids` 限制 100 个，后续补商户目录再接选择器。
5. **菜单启用用新迁移**：不修改已应用的 `000003`；`000008` 负责启用与新增 `iam.sod`。
6. **类型手写**：与登录阶段一致，不直接依赖 `dist/openapi/client`；字段严格对齐契约。
7. **菜单变更非实时**：写菜单后需刷新页面/重新登录才反映到侧边栏。

---

## 7. 实施顺序

### 批次 A：基础设施 + 核心 RBAC

1. 补角色模板契约（5.1）、新增菜单迁移（5.2）。
2. 新建 `api/iam/*`、`types.ts`、`errors.ts`，删除 `api/system/role.ts`。
3. 共享组件：`permission-picker`、`data-scope-editor`、`role-select`、`subject-input`、`conflict-modal`。
4. 重写角色页；新增权限目录、绑定、模拟鉴权页。
5. `make openapi` + typecheck。

### 批次 B：治理能力

6. 变更审批页 + 冲突/审批兜底弹窗联动。
7. 破玻璃页（列表复用审批单）。
8. SoD 页。
9. 菜单管理页。

### 批次 C：模板与验收

10. 角色模板页（契约已就绪）。
11. i18n、菜单迁移执行、逐页浏览器验收。
12. 全量 `make openapi`、`go test ./internal/iam/...`、`pnpm typecheck/build`、oxlint/oxfmt。

---

## 8. 验证

### 8.1 命令

```bash
make openapi                 # 模板契约 validate + breaking
make openapi-bundle
go test ./internal/iam/...
cd frontend && pnpm -F @vben/web-admin typecheck && pnpm -F @vben/web-admin build
```

迁移（本地 MySQL）：

```bash
go run ./cmd/migrate -driver mysql -dsn "$DSN" -dir migrations -action up
go run ./cmd/migrate -driver mysql -dsn "$DSN" -dir migrations -action down -steps 1
```

### 8.2 浏览器验收矩阵

使用具备 `*` 权限的测试账号登录 web-admin：

| # | 页面 | 验收点 |
|---|---|---|
| 1 | 角色管理 | 新建→列表可见；编辑改名；授权勾选权限与数据范围；约束保存；克隆得到 disabled 角色；删除空角色成功 |
| 2 | 角色管理 | 两窗口并发改名，后提交者看到 409 冲突弹窗并可刷新 |
| 3 | 权限目录 | 分页/筛选生效；`page_size` 不超过 100；只读无写按钮 |
| 4 | 授权绑定 | 按角色/主体查询；绑定后主体权限抽屉出现该角色；解绑后消失 |
| 5 | 模拟鉴权 | 已授权权限 `allowed=true`，未授权 `allowed=false` 且 reason 正确 |
| 6 | 变更审批 | 新建 `role_update` 审批单→通过→应用；申请人与审批人相同被拒（403） |
| 7 | 破玻璃 | 申请 60s 授权→另一账号审批→列表出现生效记录→撤销；关闭开关时显示空态说明 |
| 8 | SoD | 预检返回违规主体；创建互斥组；编辑阈值；违规清单可查 |
| 9 | 角色模板 | 新建模板→实例化角色→修改模板后 drift 可查→dry-run 同步→正式同步 |
| 10 | 菜单管理 | 树表包含停用节点；新建测试菜单→编辑→停用→启用→清理 |
| 11 | 侧边栏 | 刷新后 8 个 IAM 入口可见；无对应权限码的账号看不到入口与按钮 |

---

## 9. 风险与后续

1. **页面数量大**：9 个页面 + 共享组件，按批次 A/B/C 提交，避免单次改动过大。
2. **审批模式默认关闭**：验收时需临时 `iam.approval.enabled=true` 验证 409 引导；关闭时直写正常。
3. **破玻璃依赖开关与双人**：需两个账号（申请人 ≠ 审批人），验收前准备。
4. **模板契约属新增登记**：`make openapi` 必须通过；若后端 DTO 与计划字段有差异，以 `template.go` 为准同步 schema。
5. **菜单写入影响入口**：菜单管理页的测试数据需在验收后停用/清理，避免污染侧边栏。

---

## 10. 实施与验收结果（2026-10-28）

### 10.1 命令门禁

| 命令 | 结果 |
|---|---|
| `make openapi` | 通过；admin 升至 `6.0.0`，oasdiff 判定 major 已提升（intentional breaking） |
| `make openapi-bundle` | 通过；bundle 含 7 处 role-templates 路径 |
| `go test ./internal/iam/...` | 全部通过 |
| `pnpm -F @vben/web-admin typecheck` | 通过 |
| `pnpm -F @vben/web-admin build` | 通过（7610 模块，42s） |
| `oxlint`（改动目录 31 个文件） | 0 warning / 0 error |
| `oxfmt --check`（改动目录） | 通过（角色页已格式化） |
| 迁移 `000008` | up / down / 再 up 均成功；8 个 IAM 菜单为 active |

### 10.2 浏览器验收（Playwright + 系统 Edge）

- 角色管理：新建 → 列表可见 → 授予 `iam:role:read` → 保存授权成功。
- SoD：新建互斥角色组 → 列表可见 → 删除成功。
- 角色模板：新建 → 列表可见 → 删除成功。
- 9 个 IAM 页面均可加载，控制台无报错；侧边栏 8 个 IAM 入口可见；`*` 快照用户按钮可见。
- 验收残留（临时角色、测试菜单）已清理。

### 10.3 接口级验收

- 角色：创建 → 绑定 → 主体快照出现该角色 → 按角色查绑定 → 解绑 → 快照移除 → 删除。
- 变更审批：创建 `role_update` 审批单 → 申请人自审返回 40300/403 → 撤销成功。
- 菜单：新建 → 改名 → 停用 → 启用 → 停用，生命周期与乐观锁均正常。
- 破玻璃：未做双人端到端（需 `iam.breakglass.enabled=true` 与第二个审批账号），页面空态与列表复用
  `GET /iam/change-requests?action=breakglass_request` 已验证。
- 审批通过/应用：未做双人端到端（需非申请人账号），创建/自审拒绝/撤销已验证。
