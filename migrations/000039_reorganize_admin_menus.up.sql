-- 000039 (up): 按 A 端画布重组菜单目录与标题。
--
-- 范围：仅调整父节点、标题、排序、图标与必要绝对路径；既有 component 不变。
-- 新增：租户 / 身份 / 治理 / 消息 / 审计目录，以及对应页面入口与特性开关入口。
-- 幂等：目录与叶子按 (channel, code) upsert；存量节点 UPDATE 按 code。

SET @now = UTC_TIMESTAMP(3);
SET @actor = 'migration-admin-menu-reorg';

-- 1) 新目录与叶子入口
INSERT INTO iam_menu
    (id, channel, code, parent_code, node_type, route_name, route_path, component, redirect,
        title, title_key, icon, permission_code, sort, hide_in_menu, keep_alive, external_url,
        status, updated_by, created_at, updated_at)
VALUES
    (1910000000000000301, 'admin', 'tenant', NULL, 'catalog', 'Tenant', '/tenant', '', '/tenant/list',
        '租户运营', 'page.tenant.title', 'lucide:building-2', NULL, 5, 0, 0, '',
        'active', @actor, @now, @now),
    (1910000000000000302, 'admin', 'tenant.list', 'tenant', 'menu', 'TenantList', 'list',
        '/tenant/list/index.vue', '',
        '租户管理', 'page.tenant.list', 'lucide:building', 'tenant:read', 10, 0, 0, '',
        'active', @actor, @now, @now),

    (1910000000000000310, 'admin', 'identity', NULL, 'catalog', 'Identity', '/identity', '', '/identity/account',
        '身份管理', 'page.identity.title', 'lucide:users', NULL, 10, 0, 0, '',
        'active', @actor, @now, @now),
    (1910000000000000311, 'admin', 'identity.account', 'identity', 'menu', 'IdentityAccount', 'account',
        '/identity/account/index.vue', '',
        '管理员账号', 'page.identity.account', 'lucide:user-cog', 'identity:account:read', 10, 0, 0, '',
        'active', @actor, @now, @now),
    (1910000000000000312, 'admin', 'identity.m2m', 'identity', 'menu', 'IdentityM2M', 'm2m',
        '/identity/m2m/index.vue', '',
        '机器身份', 'page.identity.m2m', 'lucide:bot', 'identity:m2m:read', 20, 0, 0, '',
        'active', @actor, @now, @now),
    (1910000000000000313, 'admin', 'identity.scim', 'identity', 'menu', 'IdentityScim', 'scim',
        '/identity/scim/index.vue', '',
        '目录同步', 'page.identity.scim', 'lucide:folder-sync', 'identity:scim:read', 30, 0, 0, '',
        'active', @actor, @now, @now),

    (1910000000000000330, 'admin', 'governance', NULL, 'catalog', 'Governance', '/governance', '', '/iam/change',
        '访问治理', 'page.governance.title', 'lucide:scale', NULL, 30, 0, 0, '',
        'active', @actor, @now, @now),
    (1910000000000000331, 'admin', 'iam.certification', 'governance', 'menu', 'IamCertification', '/iam/certification',
        '/iam/certification/index.vue', '',
        '访问复核', 'page.iam.certification', 'lucide:clipboard-list', 'iam:certification:read', 40, 0, 0, '',
        'active', @actor, @now, @now),

    (1910000000000000340, 'admin', 'messaging', NULL, 'catalog', 'Messaging', '/messaging', '', '/messaging/notifications',
        '消息运营', 'page.messaging.title', 'lucide:mail', NULL, 50, 0, 0, '',
        'active', @actor, @now, @now),
    (1910000000000000341, 'admin', 'messaging.notifications', 'messaging', 'menu', 'MessagingNotifications', 'notifications',
        '/messaging/notifications/index.vue', '',
        '通知单', 'page.messaging.notifications', 'lucide:inbox', 'notification:read', 10, 0, 0, '',
        'active', @actor, @now, @now),
    (1910000000000000342, 'admin', 'messaging.templates', 'messaging', 'menu', 'MessagingTemplates', 'templates',
        '/messaging/templates/index.vue', '',
        '通知模板', 'page.messaging.templates', 'lucide:file-text', 'notification:read', 20, 0, 0, '',
        'active', @actor, @now, @now),

    (1910000000000000350, 'admin', 'audit', NULL, 'catalog', 'Audit', '/audit', '', '/system/audit',
        '合规审计', 'page.audit.title', 'lucide:scroll-text', NULL, 60, 0, 0, '',
        'active', @actor, @now, @now),
    (1910000000000000351, 'admin', 'audit.exports', 'audit', 'menu', 'AuditExports', '/system/audit/exports',
        '/system/audit/exports/index.vue', '',
        '导出任务', 'page.audit.exports', 'lucide:download', 'audit:read', 20, 0, 0, '',
        'active', @actor, @now, @now),

    (1910000000000000109, 'admin', 'system.config.featureflags', 'system.config', 'menu',
        'SystemConfigFeatureFlags', 'feature-flags', '/system/config/featureflags/index.vue', '',
        '特性开关', 'page.system.configFeatureFlags', 'lucide:toggle-right', 'featureflag:read', 60, 0, 0, '',
        'active', @actor, @now, @now)
ON DUPLICATE KEY UPDATE
    parent_code = VALUES(parent_code),
    node_type = VALUES(node_type),
    route_name = VALUES(route_name),
    route_path = VALUES(route_path),
    component = VALUES(component),
    redirect = VALUES(redirect),
    title = VALUES(title),
    title_key = VALUES(title_key),
    icon = VALUES(icon),
    permission_code = VALUES(permission_code),
    sort = VALUES(sort),
    hide_in_menu = VALUES(hide_in_menu),
    status = 'active',
    updated_by = VALUES(updated_by),
    updated_at = @now;

-- 2) 访问控制：复用 iam 目录，并迁入角色管理
UPDATE iam_menu
SET title = '访问控制',
    title_key = 'page.access.title',
    icon = 'lucide:shield-check',
    redirect = '/system/role',
    sort = 20,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = '/system/role',
    title = '角色管理',
    title_key = 'page.access.role',
    icon = 'lucide:shield',
    sort = 5,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.role';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = '/iam/binding',
    title = '角色绑定',
    title_key = 'page.access.binding',
    sort = 20,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.binding';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = '/iam/template',
    title = '角色模板',
    title_key = 'page.access.template',
    sort = 30,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.template';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = '/iam/permission',
    title = '权限目录',
    title_key = 'page.access.permission',
    sort = 40,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.permission';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = '/iam/check',
    title = '授权检查',
    title_key = 'page.access.check',
    sort = 50,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.check';

-- 3) 访问治理：迁入审批 / SoD / 应急授权
UPDATE iam_menu
SET parent_code = 'governance',
    route_path = '/iam/change',
    title = '变更审批',
    title_key = 'page.governance.change',
    sort = 10,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.change';

UPDATE iam_menu
SET parent_code = 'governance',
    route_path = '/iam/sod',
    title = '职责分离',
    title_key = 'page.governance.sod',
    sort = 20,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.sod';

UPDATE iam_menu
SET parent_code = 'governance',
    route_path = '/iam/breakglass',
    title = '应急授权',
    title_key = 'page.governance.breakglass',
    sort = 30,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.breakglass';

-- 4) 配置与发布
UPDATE iam_menu
SET title = '配置与发布',
    title_key = 'page.config.title',
    icon = 'lucide:sliders-horizontal',
    sort = 40,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config';

UPDATE iam_menu
SET title = '配置项',
    title_key = 'page.config.items',
    sort = 10,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.items';

UPDATE iam_menu
SET title = '发布单',
    title_key = 'page.config.releases',
    sort = 40,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.releases';

UPDATE iam_menu
SET title = '业务密钥',
    title_key = 'page.config.secrets',
    sort = 50,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.secrets';

-- 5) 合规审计：启用审计事件并挂到新目录
UPDATE iam_menu
SET parent_code = 'audit',
    route_path = '/system/audit',
    title = '审计事件',
    title_key = 'page.audit.events',
    icon = 'lucide:list',
    sort = 10,
    status = 'active',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.audit';

-- 6) 系统管理：仅保留菜单资源与个人中心
UPDATE iam_menu
SET redirect = '/system/menu',
    sort = 90,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system';

UPDATE iam_menu
SET title = '菜单资源',
    title_key = 'page.system.menu',
    sort = 10,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.menu';
