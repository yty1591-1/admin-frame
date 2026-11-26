-- 000039 (down): 回滚 A 端菜单重组到迁移前父子关系与标题。

SET @now = UTC_TIMESTAMP(3);
SET @actor = 'migration-admin-menu-reorg-down';

UPDATE iam_menu
SET parent_code = 'system',
    route_path = 'role',
    title = '角色管理',
    title_key = 'page.system.role',
    icon = 'lucide:shield-check',
    sort = 10,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.role';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = 'permission',
    title = '权限目录',
    title_key = 'page.iam.permission',
    sort = 10,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.permission';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = 'binding',
    title = '授权绑定',
    title_key = 'page.iam.binding',
    sort = 20,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.binding';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = 'template',
    title = '角色模板',
    title_key = 'page.iam.template',
    sort = 30,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.template';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = 'change',
    title = '变更审批',
    title_key = 'page.iam.change',
    sort = 40,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.change';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = 'breakglass',
    title = '破玻璃申请',
    title_key = 'page.iam.breakglass',
    sort = 50,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.breakglass';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = 'sod',
    title = '互斥角色组',
    title_key = 'page.iam.sod',
    sort = 55,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.sod';

UPDATE iam_menu
SET parent_code = 'iam',
    route_path = 'check',
    title = '模拟鉴权',
    title_key = 'page.iam.check',
    sort = 60,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam.check';

UPDATE iam_menu
SET title = '权限管理',
    title_key = 'page.iam.title',
    icon = 'lucide:shield',
    redirect = '/iam/permission',
    sort = 20,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'iam';

UPDATE iam_menu
SET title = '配置中心',
    title_key = 'page.system.config',
    sort = 40,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config';

UPDATE iam_menu
SET title = '配置列表',
    title_key = 'page.system.configItems',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.items';

UPDATE iam_menu
SET title = '发布与回滚',
    title_key = 'page.system.configReleases',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.releases';

UPDATE iam_menu
SET title = '密钥管理',
    title_key = 'page.system.configSecrets',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.secrets';

UPDATE iam_menu
SET parent_code = 'system',
    route_path = 'audit',
    title = '审计日志',
    title_key = 'page.system.audit',
    icon = 'lucide:scroll-text',
    sort = 30,
    status = 'disabled',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.audit';

UPDATE iam_menu
SET redirect = '/system/role',
    sort = 10,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system';

UPDATE iam_menu
SET title = '菜单管理',
    title_key = 'page.system.menu',
    sort = 20,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.menu';

DELETE FROM iam_menu
WHERE channel = 'admin'
  AND code IN (
    'tenant', 'tenant.list',
    'identity', 'identity.account', 'identity.m2m', 'identity.scim',
    'governance', 'iam.certification',
    'messaging', 'messaging.notifications', 'messaging.templates',
    'audit', 'audit.exports',
    'system.config.featureflags'
  );
