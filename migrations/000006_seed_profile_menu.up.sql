-- 个人中心路由（A 端）：accessMode=backend 时，前端页面路由完全由 /iam/me/menus 下发，
-- 未在菜单表登记的页面不会被注册，用户下拉菜单点击「个人中心」会报 No match for Profile。
--
-- 说明：
--   * 挂在既有 system 目录下，用绝对路径 /profile，复用 BasicLayout；
--   * hide_in_menu=1：路由可达但不进入侧边栏；
--   * permission_code=NULL：登录即可见，个人中心属于自助页面，不额外要求权限点。

INSERT INTO iam_menu
    (id, channel, code, parent_code, node_type, route_name, route_path, component, redirect,
     title, title_key, icon, permission_code, sort, hide_in_menu, keep_alive, external_url,
     status, updated_by, created_at, updated_at)
VALUES
    (1910000000000000900, 'admin', 'system.profile', 'system', 'menu', 'Profile', '/profile',
     '/_core/profile/index.vue', '', '个人中心', 'page.profile.title', 'lucide:user',
     NULL, 90, 1, 0, '', 'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
AS new
ON DUPLICATE KEY UPDATE
    parent_code      = new.parent_code,
    node_type        = new.node_type,
    route_name       = new.route_name,
    route_path       = new.route_path,
    component        = new.component,
    redirect         = new.redirect,
    title            = new.title,
    title_key        = new.title_key,
    icon             = new.icon,
    permission_code  = new.permission_code,
    sort             = new.sort,
    hide_in_menu     = new.hide_in_menu,
    keep_alive       = new.keep_alive,
    external_url     = new.external_url,
    status           = new.status,
    updated_by       = new.updated_by,
    updated_at       = UTC_TIMESTAMP(3);
