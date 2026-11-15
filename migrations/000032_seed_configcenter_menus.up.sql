-- 000032 (up): 配置中心管控菜单（M1 四页）。
--
-- 将基线占位 system.config（disabled menu）升级为 catalog，并挂载：
--   items / edit / versions / releases。
-- edit、versions 默认 hide_in_menu：从列表跳转，避免无 id 的死入口。
-- 权限：读挂 config:read；编辑挂 config:write；发布页读入口仍用 config:read，
-- 写 / 审批 / 发布按钮由前端 v-access 与后端中间件二次校验。

SET @now = UTC_TIMESTAMP(3);

UPDATE iam_menu
SET node_type = 'catalog',
    route_name = 'SystemConfig',
    route_path = '/system/config',
    component = '',
    redirect = '/system/config/items',
    title = '配置中心',
    title_key = 'page.system.config',
    icon = 'lucide:sliders-horizontal',
    permission_code = NULL,
    sort = 40,
    hide_in_menu = 0,
    status = 'active',
    updated_by = 'migration-configcenter-menus',
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config';

INSERT INTO iam_menu
    (id, channel, code, parent_code, node_type, route_name, route_path, component, redirect,
        title, title_key, icon, permission_code, sort, hide_in_menu, keep_alive, external_url,
        status, updated_by, created_at, updated_at)
VALUES
    (1910000000000000104, 'admin', 'system.config.items', 'system.config', 'menu',
        'SystemConfigItems', 'items', '/system/config/items/index.vue', '',
        '配置列表', 'page.system.configItems', 'lucide:list', 'config:read', 10, 0, 0, '',
        'active', 'migration-configcenter-menus', @now, @now),
    (1910000000000000105, 'admin', 'system.config.edit', 'system.config', 'menu',
        'SystemConfigEdit', 'edit', '/system/config/edit/index.vue', '',
        '配置编辑', 'page.system.configEdit', 'lucide:pencil', 'config:write', 20, 1, 0, '',
        'active', 'migration-configcenter-menus', @now, @now),
    (1910000000000000106, 'admin', 'system.config.versions', 'system.config', 'menu',
        'SystemConfigVersions', 'versions', '/system/config/versions/index.vue', '',
        '版本历史', 'page.system.configVersions', 'lucide:history', 'config:read', 30, 1, 0, '',
        'active', 'migration-configcenter-menus', @now, @now),
    (1910000000000000107, 'admin', 'system.config.releases', 'system.config', 'menu',
        'SystemConfigReleases', 'releases', '/system/config/releases/index.vue', '',
        '发布与回滚', 'page.system.configReleases', 'lucide:rocket', 'config:read', 40, 0, 0, '',
        'active', 'migration-configcenter-menus', @now, @now)
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
