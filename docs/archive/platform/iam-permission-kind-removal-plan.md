# IAM 权限模型精简与菜单资源改造清单

> 状态：**已实施 · 已归档**（2026-11-02 归档核对：`migrations/000002` 已建 `iam_menu` 且 `iam_permission` 仅保留 `code/name/channel/resource(生成列)/data_bearing/sort/status` 等列；`internal/iam/catalog.Entry` 已无 `Kind`/`ParentCode`/`Path`/`Action`。下方清单勾选框未回填，属台账滞后，以代码为准）
> 日期：2026-10-27；归档：2026-11-02
> 决策来源：
> 1. 前端改用 `v-access:code` 的并集 / 交集判定按钮显隐，权限粒度统一收敛为「接口权限码」；
> 2. 权限目录收敛为平铺表，菜单 / 路由资源独立建表，权限表不再兼任任何树职责；
> 3. 权限码的 `resource` / `action` 不再作为可写列存储：`action` 删除，`resource` 改为生成列。
>
> 关联：`migrations/000002_create_iam_tables.up.sql`、`internal/iam/catalog`、`docs/FRONTEND_INTEGRATION_REVIEW.md`（GAP-07 / GAP-09）

---

## 1. 结论与依据

| 判断 | 依据 |
|---|---|
| `kind` 不是安全边界 | 鉴权中间件只校验权限码是否在快照中，路由注册处挂的是 `perm("iam:role:write")`，全链路不读 `kind` |
| `kind` 在库内无查询使用 | 全仓仅 `catalog.go`（枚举校验）与 `dto.go:205`（响应透出）引用 |
| `kind` 三个取值只有 `api` 有数据 | `000003` 种子 21 条全部 `api`；`menu` / `button` 零数据、零消费方 |
| `parent_code` 未生效 | 种子 21 条全为 `NULL`，仅 `query_service.go:157` 拼树时使用，结果恒为平铺列表 |
| `path` 是死列 | 种子 21 条全为空串；仅 `dto.go` 透传，无任何过滤 / 排序 / 子树查询逻辑读取 |
| `action` 是纯派生列 | `Entry.Action()` 从码末段派生；无 SQL 读取、无索引、无业务消费 |
| `resource` 有外部语义 | `iam_role_data_scope.resource` 的取值域基准 + 授权页分组依据；应保留但不可手工写入 |
| 菜单信息无法由权限表承载 | 前端路由需要 `component`、`route name`、`icon`、`redirect`、`hideInMenu`、`keepAlive` 等 10+ 个字段，且一个权限点可对应多个菜单入口 |
| 前端并集已是现成能力 | `hasAccessByCodes` 为「任一命中」即并集；交集需补齐（见 §5） |

**结论：删除 `kind` / `parent_code` / `path` / `action` 四列，`resource` 改为 STORED 生成列；新建 `iam_menu` 承载菜单 / 路由资源。**

---

## 2. 变更范围

### 本清单包含

- [ ] 删除 `iam_permission.kind`（列、CHECK、目录字段、DTO、OpenAPI、种子、测试、文档）
- [ ] 删除 `iam_permission.parent_code`（含自引用外键、自引用 CHECK、目录父节点校验、拼树逻辑）
- [ ] 删除 `iam_permission.path`
- [ ] 删除 `iam_permission.action`（含 `Entry.Action()` 与一致性断言）
- [ ] `resource` 改为 `GENERATED ALWAYS AS (SUBSTRING_INDEX(code, ':', 1)) STORED`
- [ ] 新建 `iam_menu` 表与种子，新增菜单查询接口（含权限裁剪）
- [ ] 前端补「交集」判定（`hasAllAccessByCodes` + `v-access:code.all`），并切换到后端菜单模式

### 本清单不包含（保持现状）

- [ ] 「前端权限码 ⊆ 后端目录」的 CI 门禁
- [ ] `resource` 段的中文显示名映射（授权页分组用，见 §4.7）

> 更新：A 端菜单 CRUD 后端已另行落地（000008 版本列 + 000009 权限点），前端管理页仍后置。

---

## 3. 后端变更：精简 `iam_permission`

### 3.1 目标结构（`000002_create_iam_tables.up.sql`）

```sql
CREATE TABLE IF NOT EXISTS iam_permission (
    code             VARCHAR(128) NOT NULL COMMENT '权限点稳定标识，全小写；发布后不可改名或复用',
    name             VARCHAR(128) NOT NULL COMMENT '显示名，用于授权勾选',
    channel          VARCHAR(16)  NOT NULL COMMENT '可授予端别：admin / biz / both',
    resource         VARCHAR(64)  GENERATED ALWAYS AS (SUBSTRING_INDEX(code, ':', 1)) STORED
                                  COMMENT '资源段，由权限码首段派生；供数据范围匹配与 SQL 侧分组 / 联表',
    data_bearing     TINYINT      NOT NULL DEFAULT 0 COMMENT '是否涉及数据范围：1=是 / 0=否',
    sort             INT          NOT NULL DEFAULT 0 COMMENT '排序值，升序',
    status           VARCHAR(16)  NOT NULL DEFAULT 'active' COMMENT 'active / deprecated',
    created_at       DATETIME(3)  NOT NULL COMMENT '创建时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)  NOT NULL COMMENT '最后更新时间（UTC，毫秒精度）',
    PRIMARY KEY (code),
    KEY idx_iam_permission_channel_status (channel, status, sort)
        COMMENT '按端别 + 状态过滤并按排序输出',
    KEY idx_iam_permission_resource (resource)
        COMMENT '按资源段分组 / 联表查询',
    CONSTRAINT ck_iam_permission_channel CHECK (channel IN ('admin', 'biz', 'both')),
    CONSTRAINT ck_iam_permission_status CHECK (status IN ('active', 'deprecated')),
    CONSTRAINT ck_iam_permission_data_bearing CHECK (data_bearing IN (0, 1))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 权限点目录，代码所有、库内只读同步；不存放角色授权关系';
```

执行要点：

- [ ] 删除 L25 `action`、L26 `kind`、L28 `parent_code`、L29 `path` 四列
- [ ] L24 `resource` 改为上表生成列写法，**不要加 `NOT NULL`**（生成列约束行为因版本而异，`SUBSTRING_INDEX` 恒非空）
- [ ] 删除 L36–L37 外键 `fk_iam_permission_parent`
- [ ] 删除 L39 `ck_iam_permission_kind`、L42 `ck_iam_permission_parent_self`
- [ ] 新增 `KEY idx_iam_permission_resource (resource)`
- [ ] 改写文件头 L17「权限目录父节点改为 NULL + 自引用外键……」为「权限目录为平铺表，树形结构由 `iam_menu` 承载」
- [ ] `000002_create_iam_tables.down.sql` 无需改（仅 DROP TABLE）

### 3.2 `migrations/000003_seed_iam_permissions.up.sql`

列数 **13 → 8**（`resource` 为生成列，不参与写入），最终可写列序：

```text
code, name, channel, data_bearing, sort, status, created_at, updated_at
```

- [ ] L13 列清单删除 `resource`、`action`、`kind`、`parent_code`、`path`
- [ ] L15–L35 共 21 行：逐行删除对应 5 个值，注意逗号与空格
- [ ] `ON DUPLICATE KEY UPDATE`（L38–L46）删除 `resource = new.resource,`、`action = new.action,`、`kind = new.kind,`、`parent_code = new.parent_code,`、`path = new.path,`
- [ ] **禁止**在 `ON DUPLICATE` 中引用生成列 `resource`，否则报 `The value specified for generated column ... is not allowed`
- [ ] `000003_seed_iam_permissions.down.sql` 只按 code 删除，无需改

### 3.3 `internal/iam/catalog/catalog.go`

- [ ] 删除 `Kind` 类型与 `KindAPI` / `KindMenu` / `KindButton` 常量（L32–L43）
- [ ] 删除 `Entry.Kind`（L73–L74）、`Entry.ParentCode`（L81–L82）、`Entry.Path`（L83–L84）字段
- [ ] 改写 L64 注释：`Resource` 由 `Code` 派生且库内为生成列；`Action` 不再保留
- [ ] 删除 `Entry.Validate()` 中的 kind 分支（L125–L129）与 parent_code 格式校验（L138–L139）
- [ ] 删除父节点存在性校验循环（L396–L401）
- [ ] 删除 `Entry.Action()` 方法及 21 条目录项中的 `Kind:`、`ParentCode:`、`Path:` 字面量
- [ ] **保留** `Entry.Resource()`（数据范围匹配与授权页分组仍依赖它）

### 3.4 `internal/iam/catalog/catalog_test.go`（最易漏）

`seedRow` / `docRow` 按列位置解析，删除 5 列后捕获组必须重排：

- [ ] `seedRow` 正则按 8 列重写，捕获组对齐 `code / name / channel / data_bearing / sort`（5 个）
- [ ] 删除 `resource` 断言（L144–L146）、`action` 断言（L147–L149）、`kind` 断言（L150–L152）
- [ ] `data_bearing` 由 `m[7]` → `m[4]`（L153–L158）
- [ ] `sort` 由 `m[8]` → `m[5]`（L160–L161）
- [ ] `docRow` 正则去掉 kind 段；删除 kind 断言（L210–L212）；`data_bearing` 由 `m[4]` → `m[3]`（L217–L218）
- [ ] 若 `docRow` 还存在 `resource` / `action` 列，一并删除

### 3.5 `internal/iam/transport/rest/dto.go`

- [ ] 删除 `PermissionDTO.Action`、`Kind`、`ParentCode`（L25）、`Path` 字段
- [ ] 删除对应映射（`Kind:` L205、`ParentCode:` L209、`Path:` L210 及 `Action:`）
- [ ] **保留** `Resource`，取值仍走 `entry.Resource()`

### 3.6 `internal/iam/app/query_service.go`

- [ ] 删除 `PermissionTree`、`PermissionNode` 与 `PermissionTreeDTO`：目录已平铺，树端点无消费方
- [ ] 同步删除 `GET /v1/iam/permissions/tree` 路由、OpenAPI 路径与 `PermissionTreeNode` schema
- [ ] 确认全仓无其它 `Path` / `ParentCode` 引用

### 3.7 `api/openapi/`

- [ ] `admin.yaml`：`Permission.required` 去掉 `action`、`kind`（L1483）；删除 `action`、`kind`（L1495–L1497）、`parent_code`（L1506）、`path` 属性；`resource` 改为 `readOnly: true`
- [ ] `biz.yaml`：同步删除 `action`、`kind`、`parent_code`（L1115）、`path`
- [ ] 删除两个文件中 `PermissionTree` 端点与 `PermissionTreeNode` schema 的残留

### 3.8 文档

- [ ] `docs/iam/PERMISSIONS.md`：删除 `kind` 列（表头、分隔行、21 行数据），否则 `TestPermissionCatalogMatchesDoc` 失败；若存在 `resource` / `action` 列一并删除
- [ ] `docs/rbac-iam.md`：
  - L217 表说明去掉 `kind` / `parent_code`，改为「平铺目录，`resource` 为生成列」
  - L264–L278 的 DDL 片段同步为 §3.1 的目标结构
  - L564、L584 的企业级加固条目改写为「权限目录改为平铺，树职责移交 `iam_menu`」
- [ ] `docs/FRONTEND_INTEGRATION_REVIEW.md` GAP-07：分组依据由「`kind=menu/button/api`」改为「按 `resource`」

---

## 4. 后端变更：新建 `iam_menu`

### 4.1 `migrations/000006_create_iam_menu.up.sql` / `.down.sql`

```sql
CREATE TABLE IF NOT EXISTS iam_menu (
    id              BIGINT       NOT NULL COMMENT '菜单主键，雪花 ID',
    channel         VARCHAR(16)  NOT NULL COMMENT '归属端别：admin=web-admin / biz=web-biz；两端菜单不合并',
    code            VARCHAR(128) NOT NULL COMMENT '菜单稳定标识，如 system.role；父子引用与种子幂等键',
    parent_code     VARCHAR(128) NULL DEFAULT NULL COMMENT '父菜单 code；根节点为 NULL',
    node_type       VARCHAR(16)  NOT NULL DEFAULT 'menu' COMMENT 'catalog=目录 / menu=页面 / iframe=内嵌 / link=外链',
    route_name      VARCHAR(64)  NOT NULL COMMENT '路由 name，PascalCase，端内唯一；keep-alive 与标签页依赖',
    route_path      VARCHAR(256) NOT NULL COMMENT '路由 URL：目录为 /system，子页可为相对段 role',
    component       VARCHAR(256) NOT NULL DEFAULT '' COMMENT 'BasicLayout / IFrameView / /system/role/index；catalog 与 link 为空',
    redirect        VARCHAR(256) NOT NULL DEFAULT '' COMMENT '目录默认跳转，catalog 必填',
    title           VARCHAR(128) NOT NULL COMMENT '显示名；i18n 缺失时兜底',
    title_key       VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'i18n key，如 page.system.role；非空时前端优先',
    icon            VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '图标注册表 key，如 lucide:settings',
    permission_code VARCHAR(128) NULL DEFAULT NULL COMMENT '可见所需权限点；NULL=登录即可见',
    sort            INT          NOT NULL DEFAULT 0 COMMENT '同级升序，映射 meta.order',
    hide_in_menu    TINYINT      NOT NULL DEFAULT 0 COMMENT '1=路由可达但不进侧边栏',
    keep_alive      TINYINT      NOT NULL DEFAULT 0 COMMENT '1=页面缓存',
    external_url    VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'iframe / link 目标地址',
    status          VARCHAR(16)  NOT NULL DEFAULT 'active' COMMENT 'active / disabled',
    updated_by      VARCHAR(64)  NOT NULL COMMENT '最后维护人；种子写入 seed',
    created_at      DATETIME(3)  NOT NULL COMMENT '创建时间（UTC）',
    updated_at      DATETIME(3)  NOT NULL COMMENT '更新时间（UTC）',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_menu_code (channel, code) COMMENT '端内菜单编码唯一',
    UNIQUE KEY uk_iam_menu_route_name (channel, route_name) COMMENT 'vue-router 同名路由会互相覆盖',
    KEY idx_iam_menu_tree (channel, status, parent_code, sort) COMMENT '菜单树按端别 + 状态 + 父节点排序输出',
    KEY idx_iam_menu_permission (permission_code) COMMENT '按权限点反查受影响菜单',
    CONSTRAINT fk_iam_menu_parent FOREIGN KEY (channel, parent_code) REFERENCES iam_menu (channel, code)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_iam_menu_permission FOREIGN KEY (permission_code) REFERENCES iam_permission (code)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_menu_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_menu_node_type CHECK (node_type IN ('catalog', 'menu', 'iframe', 'link')),
    CONSTRAINT ck_iam_menu_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT ck_iam_menu_flags CHECK (hide_in_menu IN (0, 1) AND keep_alive IN (0, 1)),
    CONSTRAINT ck_iam_menu_parent_self CHECK (parent_code IS NULL OR parent_code <> code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='前端菜单 / 路由资源；可见性由权限码推导，不存放授权关系';
```

- [ ] `000006_create_iam_menu.down.sql`：`DROP TABLE IF EXISTS iam_menu;`
- [ ] 复用 `000002` 的雪花 ID 策略与 UTC `DATETIME(3)` 约定

### 4.2 `migrations/000007_seed_iam_menus.up.sql` / `.down.sql`

- [ ] A 端：逐条对照 `frontend/apps/web-admin/src/router/routes/modules/system.ts` 登记（目录 + 页面），`component` 使用其 `pageMap` key
- [ ] B 端：逐条对照 `frontend/apps/web-biz/src/router/routes/modules/member.ts` 登记
- [ ] `permission_code` 必须取该页面实际调用的接口码，且该码已存在于 `catalog`（**不得新造码**）
- [ ] `ON DUPLICATE KEY UPDATE` 更新全部展示字段，保证种子可重放
- [ ] `down.sql` 按 `(channel, code)` 逆序删除（先子后父）

### 4.3 后端代码

- [ ] `internal/iam/domain/menu.go`：`Menu` 聚合 + `NodeType` / `Status` 枚举 + 校验（route_name 非空、catalog 必须有 redirect、menu 必须有 component、iframe/link 必须有 external_url）
- [ ] `internal/iam/domain/menu_repository.go`：`List(ctx, channel, status) ([]*Menu, error)` 等最小接口
- [ ] `internal/iam/infrastructure/persistence/mysql_menu.go`：按列清单扫描，`ORDER BY sort, code`
- [ ] `internal/iam/infrastructure/persistence/memory_menu.go`：测试基座
- [ ] `internal/iam/app/menu_service.go`：菜单裁剪（见 §4.5）
- [ ] `internal/iam/transport/rest/menu.go`：`MenuDTO` / `MenuTreeNodeDTO` 与递归组装
- [ ] `internal/iam/transport/admin/router.go`、`biz/router.go`：注册 `GET /v1/menus`（仅认证，不额外要求权限点）
- [ ] `cmd/internal/iamruntime/runtime.go`：装配 `MenuRepository` + `MenuService`（内存与 SQL 两条分支都要接）

### 4.4 `api/openapi/admin.yaml` / `biz.yaml`

- [ ] 新增 `MenuTreeNode`（`name` / `path` / `component` / `redirect` / `meta{title,titleKey,icon,order,hideInMenu,keepAlive,iframeSrc,link}` / `children` / `status`）
- [ ] 新增 `GET /admin/v1/menus`、`GET /biz/v1/menus`

### 4.5 裁剪规则（服务端强制）

- [ ] 取主体快照权限码集合
- [ ] 保留 `permission_code IS NULL` 或主体持有的节点
- [ ] **递归补齐祖先链**，否则子节点可见而父目录缺失，前端拼不出树
- [ ] 父目录裁剪后无可见子节点 → 整枝丢弃
- [ ] `status='disabled'` 不返回；`deprecated` 权限点已授予的仍可见
- [ ] 输出按 `sort` 升序，映射 `meta.order`

> 注意：接口**不得返回任何仅用于按钮的节点**；按钮权限继续由 `/v1/iam/me/permissions` 提供。

### 4.6 测试

- [ ] `internal/iam/app/menu_service_test.go`：无权限 → 空；单页面权限 → 自动带出父目录；父目录无子 → 整枝丢弃
- [ ] 菜单种子一致性测试（Go 侧扫 SQL）：`permission_code` 必须存在于 `catalog`；`(channel, code)` / `(channel, route_name)` 不重复；子节点 `parent_code` 必须同端存在
- [ ] 集成测试：MySQL 下 `fk_iam_menu_parent` 跨端挂载被库层拒绝

### 4.7 授权页分组说明

删除 `parent_code` 后，`/v1/iam/permissions`（平铺）只剩 `resource` 可作为分组依据。授权页按 `resource`（`iam` / `order` / `audit`）分组渲染，分组在查询层之外（前端或未来服务端合成）完成，不再落库。`resource → 显示名` 的映射列入后续事项。

---

## 5. 前端变更清单

### 5.1 `frontend/packages/effects/access/src/use-access.ts`

- [ ] 新增并导出交集判定：

  ```ts
  function hasAllAccessByCodes(codes: string[]) {
    const userCodesSet = new Set(accessStore.accessCodes);
    // 空数组必须显式挡掉：every([]) 恒为 true 会让按钮永远可见
    return codes.length > 0 && codes.every((item) => userCodesSet.has(item));
  }
  ```

- [ ] 保留现有 `hasAccessByCodes`（并集）语义不变

### 5.2 `frontend/packages/effects/access/src/directive.ts`

- [ ] 支持 `.all` 修饰符，按修饰符选择判定函数
- [ ] 更新文件头示例注释

### 5.3 `frontend/packages/effects/access/src/access-control.vue`

- [ ] 新增 `mode` prop：`'any' | 'all'`，默认 `'any'`（向后兼容）

### 5.4 菜单接入

- [ ] `apps/*/src/preferences.ts`：`accessMode` 由 `'frontend'` 改为 `'backend'`
- [ ] `api/core/menu.ts` 指向 `/menus`（两端按 base 自动拼接）
- [ ] 删除静态业务路由 `modules/system.ts` / `member.ts`（保留 `core`、`fallback`）
- [ ] 校正 `route name` 与 `component` 的对应关系，确保命中 `pageMap`

### 5.5 使用约定（写入前端规范）

- [ ] 并集：`v-access:code="['iam:role:write', 'iam:role:grant']"`
- [ ] 交集：`v-access:code.all="['iam:role:write', 'iam:role:grant']"`
- [ ] 页面内禁止裸写 `hasAccessByCodes(['a']) && hasAccessByCodes(['b'])`

---

## 6. 验证步骤

- [ ] `grep -rn "KindAPI\|KindMenu\|KindButton\|\.Kind\b" internal/` 无残留
- [ ] `grep -rn "ParentCode\|parent_code" internal/` 无残留（`iam_menu` 相关除外）
- [ ] `grep -rn "\.Path\b\|\.Action\b" internal/iam/` 无残留
- [ ] `grep -rn "kind\|parent_code\|path\|action" migrations/000002_create_iam_tables.up.sql migrations/000003_seed_iam_permissions.up.sql` 无残留（`route_path` 除外）
- [ ] `grep -rn "parent_code" api/openapi/` 仅剩 `MenuTreeNode` 相关定义（如有）
- [ ] `go build ./...`
- [ ] `go test ./internal/iam/...`（重点：`catalog` 两个一致性测试、`menu_service_test`）
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] 前端：`pnpm --filter @vben/effects build`（或仓库对应的 typecheck 脚本）
- [ ] **生成列可用性验证**（需真实 MySQL 8.0.19+）：插入 `code='order:read'` 后，`resource` 自动为 `order`；`INSERT ... AS new ON DUPLICATE KEY UPDATE` 不涉及 `resource` 时无报错
- [ ] 手工验证：只有 `iam:role:read` 的账号，菜单只出现「角色管理」且父目录 `system` 自动补齐
- [ ] 手工验证：`v-access:code="['a','b']"` 只有 `a` 时显示；`v-access:code.all="['a','b']"` 只有 `a` 时隐藏

---

## 7. 风险与回滚

| 风险 | 等级 | 处置 |
|---|---|---|
| OpenAPI `Permission` 少四个字段、新增 `menus` 端点 | 中 | 破坏性契约变更，写 `BREAKING CHANGE`，CHANGELOG 注明升级方式 |
| 生成列写入报错（种子 / `ON DUPLICATE` 引用 `resource`） | 高 | §3.2 明确禁止写入；集成测试覆盖 |
| 漏改 `catalog_test.go` 的列下标 / 正则 | 高（易漏） | §3.4 逐项核对，先让测试红再改绿 |
| 删 `parent_code` 后授权页失去层级 | 中 | 授权页按 `resource` 分组（§4.7），不依赖库内树 |
| `component` 字符串依赖前端 `pageMap` 实现细节 | 中 | 菜单只由代码种子维护 + 一致性测试，不开放手填 |
| `all` 空数组导致按钮全亮 | 中 | §5.1 的 `codes.length > 0` 必须保留 |
| 菜单裁剪漏补祖先导致前端树断裂 | 高 | §4.5 的递归补齐 + 专项测试 |
| 误以为隐藏按钮等于做了权限 | 低 | 明示：安全边界仍是接口码 |

回滚：逐提交 revert。`000002` / `000003` 为基线迁移（未发布），直接还原文件即可；`000006` / `000007` 提供 down 脚本。

---

## 8. 提交拆分建议（遵循 `.trae/rules/git-commit-message.md`）

1. `refactor(iam)!: 精简权限目录字段并将 resource 改为生成列`
   - 迁移 + 种子 + catalog + 测试 + DTO + OpenAPI + 文档（同一变更闭环）
   - footer：`BREAKING CHANGE: Permission 响应与 OpenAPI 移除 kind、parent_code、path、action 字段`
2. `feat(iam): 新增菜单资源表与按权限裁剪的菜单接口`
   - `000006` / `000007` + domain + persistence + app + transport + openapi + runtime + 测试
3. `feat: 新增权限码交集判定并切换后端菜单模式`
   - 前端；**scope 白名单暂无前端域**，按规则省略 scope（如需，另行提案将 `web` 加入白名单）

---

## 9. 验收标准

- [ ] 全仓 `kind` / `parent_code` / `path` / `action` 在权限域引用为 0
- [ ] `iam_permission` 为平铺表，`resource` 为生成列且与 `code` 首段恒一致
- [ ] `iam_menu` 是唯一的树结构来源
- [ ] `go test ./...` 全绿（含 `catalog` 一致性、菜单裁剪、菜单种子一致性）
- [ ] `GET /{admin|biz}/v1/menus` 返回的树满足：无空目录、无 button 节点、祖先链完整
- [ ] 前端并集 / 交集两种声明方式均按预期生效
- [ ] 前端 `accessMode=backend` 下动态菜单与静态菜单视觉一致

---

## 10. 执行顺序

1. 精简 `iam_permission`（迁移 → catalog → 测试 → DTO → query_service → OpenAPI → 文档）
2. 新建 `iam_menu`（迁移 → 种子 → domain → persistence → app → transport → openapi → runtime）
3. 菜单与权限裁剪测试、种子一致性测试
4. 前端 access 交集能力 + 切换到后端菜单模式
5. 全量验证 → 按 §8 分三次提交
