-- 启用 IAM 前端页面菜单：页面实现后把占位节点从 disabled 切为 active。
-- 不修改 000003 已应用的种子（避免 checksum 漂移）；新环境按 000003 → 000008 顺序执行。
-- iam.sod 为本次新增菜单，权限码已在 000007 种子里存在。

UPDATE iam_menu
SET status = 'active', updated_by = 'seed', updated_at = UTC_TIMESTAMP(3)
WHERE channel = 'admin'
  AND code IN (
    'system.menu',
    'iam.permission',
    'iam.binding',
    'iam.template',
    'iam.change',
    'iam.breakglass',
    'iam.check'
  );

INSERT INTO iam_menu
    (id, channel, code, parent_code, node_type, route_name, route_path, component, redirect,
        title, title_key, icon, permission_code, sort, hide_in_menu, keep_alive, external_url,
        status, updated_by, created_at, updated_at)
VALUES
    (1910000000000000117, 'admin', 'iam.sod', 'iam', 'menu', 'IamSod', 'sod',
        '/iam/sod/index.vue', '',
        '互斥角色组', 'page.iam.sod', 'lucide:shield-alert', 'iam:sod:read', 55, 0, 0, '',
        'active', 'seed', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
ON DUPLICATE KEY UPDATE
    status          = 'active',
    parent_code     = 'iam',
    node_type       = 'menu',
    route_name      = 'IamSod',
    route_path      = 'sod',
    component       = '/iam/sod/index.vue',
    title           = '互斥角色组',
    title_key       = 'page.iam.sod',
    icon            = 'lucide:shield-alert',
    permission_code = 'iam:sod:read',
    sort            = 55,
    updated_by      = 'seed',
    updated_at      = UTC_TIMESTAMP(3);
