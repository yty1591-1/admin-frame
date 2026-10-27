-- IAM 域基线种子：权限目录、破玻璃角色与菜单 / 路由资源。
-- 与 internal/iam/catalog、internal/iam/perm.go、docs/iam/PERMISSIONS.md 保持一致。
-- 幂等：重复执行只更新元数据，不删除既有授权；新增权限点只能追加，禁止改写既有 code 语义。
-- 每条权限点独占一行，行首为 "('"，供 internal/iam/catalog 的单元测试做一致性解析。
-- 时间列由数据库取 UTC，避免迁移机器本地时区污染。
--
-- 权限目录约定：
--   * channel：admin=仅 A 端可授予 / biz=仅 B 端可授予 / both=两端均可；
--   * resource 是生成列（由 code 首段派生），本文件不得写入；
--   * order:* / file:* 属于已移除的演示域，保留码值以解释历史审计，status 固定 deprecated；
--   * iam:change:request / iam:change:review 为 B 端专属，A 端审批仍用 iam:change:read / approve；
--   * iam:menu:read / iam:menu:write 为 A 端菜单资源管理入口，不构成接口鉴权边界。
--
-- 破玻璃角色（docs/rbac-iam.md §8.5）不持有任何权限点：只有主体在审批有效期内的绑定存在时，
-- 快照才动态展开 "*"。幂等：重复执行不覆盖既有状态（避免把运维手工停用的角色重新激活）。
--
-- 菜单种子按 IAM 域能力设计，分两类：
--   * active：已实现页面，前端可访问（A 端角色管理、B 端成员列表）；
--   * disabled：全量能力占位（权限目录 / 授权绑定 / 角色模板 / 审批 / 破玻璃 /
--     菜单管理 / 审计 / 配置，以及 B 端角色与授权），前端页面实现后再启用。
--     disabled 节点不会下发到 `/v1/menus`，因此不会产生 404；启用前必须先创建对应 .vue。
-- 幂等：重复执行按 (channel, code) 更新展示字段。固定主键放在保留段（19xxxxxxxxxxxxxxxxxx），
-- 避免与雪花 ID 冲突。permission_code 只能引用已存在的权限码，不得新造。

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('iam:permission:read', '查看权限目录', 'admin', 0, 10, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:role:read', '查看角色', 'both', 0, 20, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:role:write', '管理角色', 'both', 0, 30, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:role:grant', '授予角色权限', 'both', 0, 40, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:binding:read', '查看绑定', 'both', 0, 50, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:binding:write', '管理绑定', 'both', 0, 60, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:check', '模拟鉴权', 'admin', 0, 70, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:read', '查看审批单', 'admin', 0, 80, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:approve', '审批变更', 'admin', 0, 90, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:breakglass:request', '申请破玻璃', 'admin', 0, 100, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:breakglass:approve', '审批破玻璃', 'admin', 0, 110, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('order:read', '查询订单', 'both', 1, 120, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('order:write', '变更订单', 'both', 1, 130, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('file:read', '查看文件', 'both', 1, 140, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('file:write', '管理文件', 'both', 1, 150, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('audit:read', '查询审计', 'admin', 1, 160, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('data:export', '导出数据', 'both', 1, 170, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('config:read', '查看配置', 'admin', 0, 180, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('config:write', '修改配置', 'admin', 0, 190, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:request', '发起变更申请', 'biz', 0, 200, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:review', '审批本商户变更', 'biz', 0, 210, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:menu:read', '查看菜单资源', 'admin', 0, 220, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:menu:write', '管理菜单资源', 'admin', 0, 230, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
AS new
ON DUPLICATE KEY UPDATE
    name         = new.name,
    channel      = new.channel,
    data_bearing = new.data_bearing,
    sort         = new.sort,
    status       = new.status,
    updated_at   = UTC_TIMESTAMP(3);

INSERT INTO iam_role
    (id, channel, scope_type, merchant_id, code, name, description, role_type, status,
        version, created_by, updated_by, created_at, updated_at, deleted_at)
VALUES
    (1900000000000000001, 'admin', 'platform', '', 'breakglass_admin', '破玻璃超级权限',
        '系统内置：仅在有效破玻璃审批绑定期间由快照展开 *，不得直接授予',
        'breakglass', 'active', 1, 'system', 'system', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3), NULL)
ON DUPLICATE KEY UPDATE id = id;

INSERT INTO iam_menu
    (id, channel, code, parent_code, node_type, route_name, route_path, component, redirect,
        title, title_key, icon, permission_code, sort, hide_in_menu, keep_alive, external_url,
        status, updated_by, created_at, updated_at)
VALUES
    -- A 端：系统管理（已实现页面 + 占位）
    (1910000000000000001, 'admin', 'system', NULL, 'catalog', 'System', '/system', '', '/system/role',
        '系统管理', 'page.system.title', 'lucide:settings', NULL, 10, 0, 0, '',
        'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000002, 'admin', 'system.role', 'system', 'menu', 'SystemRole', 'role',
        '/system/role/index.vue', '',
        '角色管理', 'page.system.role', 'lucide:shield-check', 'iam:role:read', 10, 0, 0, '',
        'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000101, 'admin', 'system.menu', 'system', 'menu', 'SystemMenu', 'menu',
        '/system/menu/index.vue', '',
        '菜单管理', 'page.system.menu', 'lucide:list-tree', 'iam:menu:read', 20, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000102, 'admin', 'system.audit', 'system', 'menu', 'SystemAudit', 'audit',
        '/system/audit/index.vue', '',
        '审计日志', 'page.system.audit', 'lucide:scroll-text', 'audit:read', 30, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000103, 'admin', 'system.config', 'system', 'menu', 'SystemConfig', 'config',
        '/system/config/index.vue', '',
        '系统配置', 'page.system.config', 'lucide:sliders-horizontal', 'config:read', 40, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    -- A 端：权限管理（全部为占位，页面实现后启用）
    (1910000000000000110, 'admin', 'iam', NULL, 'catalog', 'Iam', '/iam', '', '/iam/permission',
        '权限管理', 'page.iam.title', 'lucide:shield', NULL, 20, 0, 0, '',
        'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000111, 'admin', 'iam.permission', 'iam', 'menu', 'IamPermission', 'permission',
        '/iam/permission/index.vue', '',
        '权限目录', 'page.iam.permission', 'lucide:key-round', 'iam:permission:read', 10, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000112, 'admin', 'iam.binding', 'iam', 'menu', 'IamBinding', 'binding',
        '/iam/binding/index.vue', '',
        '授权绑定', 'page.iam.binding', 'lucide:link', 'iam:binding:read', 20, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000113, 'admin', 'iam.template', 'iam', 'menu', 'IamTemplate', 'template',
        '/iam/template/index.vue', '',
        '角色模板', 'page.iam.template', 'lucide:copy', 'iam:role:read', 30, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000114, 'admin', 'iam.change', 'iam', 'menu', 'IamChange', 'change',
        '/iam/change/index.vue', '',
        '变更审批', 'page.iam.change', 'lucide:clipboard-check', 'iam:change:read', 40, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000115, 'admin', 'iam.breakglass', 'iam', 'menu', 'IamBreakglass', 'breakglass',
        '/iam/breakglass/index.vue', '',
        '破玻璃申请', 'page.iam.breakglass', 'lucide:siren', 'iam:breakglass:request', 50, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000116, 'admin', 'iam.check', 'iam', 'menu', 'IamCheck', 'check',
        '/iam/check/index.vue', '',
        '模拟鉴权', 'page.iam.check', 'lucide:scan-search', 'iam:check', 60, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    -- B 端：成员管理（成员列表为已实现页面，其余为占位）
    (1910000000000000003, 'biz', 'member', NULL, 'catalog', 'Member', '/member', '', '/member/list',
        '成员管理', 'page.member.title', 'lucide:users', NULL, 10, 0, 0, '',
        'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000004, 'biz', 'member.list', 'member', 'menu', 'MemberList', 'list',
        '/member/list/index.vue', '',
        '成员列表', 'page.member.list', 'lucide:contact', NULL, 10, 0, 0, '',
        'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000201, 'biz', 'member.role', 'member', 'menu', 'MemberRole', 'role',
        '/member/role/index.vue', '',
        '角色管理', 'page.member.role', 'lucide:shield-check', 'iam:role:read', 20, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000202, 'biz', 'member.binding', 'member', 'menu', 'MemberBinding', 'binding',
        '/member/binding/index.vue', '',
        '授权绑定', 'page.member.binding', 'lucide:link', 'iam:binding:read', 30, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000203, 'biz', 'member.template', 'member', 'menu', 'MemberTemplate', 'template',
        '/member/template/index.vue', '',
        '角色模板', 'page.member.template', 'lucide:copy', 'iam:role:read', 40, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000204, 'biz', 'member.change', 'member', 'menu', 'MemberChange', 'change',
        '/member/change/index.vue', '',
        '变更申请', 'page.member.change', 'lucide:clipboard-list', 'iam:change:request', 50, 0, 0, '',
        'disabled', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
AS new
ON DUPLICATE KEY UPDATE
    parent_code     = new.parent_code,
    node_type       = new.node_type,
    route_path      = new.route_path,
    component       = new.component,
    redirect        = new.redirect,
    title           = new.title,
    title_key       = new.title_key,
    icon            = new.icon,
    permission_code = new.permission_code,
    sort            = new.sort,
    hide_in_menu    = new.hide_in_menu,
    keep_alive      = new.keep_alive,
    external_url    = new.external_url,
    status          = new.status,
    updated_by      = new.updated_by,
    updated_at      = UTC_TIMESTAMP(3);
