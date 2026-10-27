-- 菜单种子：与前端静态路由 modules/system.ts、modules/member.ts 一一对应。
-- 幂等：重复执行按 (channel, code) 更新展示字段，便于调整标题 / 排序 / 图标。
-- 固定主键放在保留段（191xxxxxxxxxxxxxxxxxx），避免与雪花 ID 冲突。
-- permission_code 只能引用 iam_permission 中已存在的权限码，不得新造。

INSERT INTO iam_menu
    (id, channel, code, parent_code, node_type, route_name, route_path, component, redirect,
        title, title_key, icon, permission_code, sort, hide_in_menu, keep_alive, external_url,
        status, updated_by, created_at, updated_at)
VALUES
    (1910000000000000001, 'admin', 'system', NULL, 'catalog', 'System', '/system', '', '/system/role',
        '系统管理', 'page.system.title', 'lucide:settings', NULL, 10, 0, 0, '',
        'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000002, 'admin', 'system.role', 'system', 'menu', 'SystemRole', 'role',
        '/system/role/index.vue', '',
        '角色管理', 'page.system.role', 'lucide:shield-check', 'iam:role:read', 10, 0, 0, '',
        'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000003, 'biz', 'member', NULL, 'catalog', 'Member', '/member', '', '/member/list',
        '成员管理', 'page.member.title', 'lucide:users', NULL, 10, 0, 0, '',
        'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    (1910000000000000004, 'biz', 'member.list', 'member', 'menu', 'MemberList', 'list',
        '/member/list/index.vue', '',
        '成员列表', 'page.member.list', 'lucide:contact', NULL, 10, 0, 0, '',
        'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
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
