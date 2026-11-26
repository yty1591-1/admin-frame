-- 000034 (up): 配置中心密钥管理菜单（M2）。

SET @now = UTC_TIMESTAMP(3);

INSERT INTO iam_menu
    (id, channel, code, parent_code, node_type, route_name, route_path, component, redirect,
        title, title_key, icon, permission_code, sort, hide_in_menu, keep_alive, external_url,
        status, updated_by, created_at, updated_at)
VALUES
    (1910000000000000108, 'admin', 'system.config.secrets', 'system.config', 'menu',
        'SystemConfigSecrets', 'secrets', '/system/config/secrets/index.vue', '',
        '密钥管理', 'page.system.configSecrets', 'lucide:key-round', 'config:secret:read', 50, 0, 0, '',
        'active', 'migration-configcenter-secret-menus', @now, @now)
ON DUPLICATE KEY UPDATE
    parent_code = VALUES(parent_code),
    node_type = VALUES(node_type),
    route_name = VALUES(route_name),
    route_path = VALUES(route_path),
    component = VALUES(component),
    title = VALUES(title),
    title_key = VALUES(title_key),
    icon = VALUES(icon),
    permission_code = VALUES(permission_code),
    sort = VALUES(sort),
    hide_in_menu = VALUES(hide_in_menu),
    status = 'active',
    updated_by = VALUES(updated_by),
    updated_at = @now;
