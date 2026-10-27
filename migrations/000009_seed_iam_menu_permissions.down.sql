-- 回滚菜单管理权限点。
-- DATA-02：若权限点仍被 iam_role_permission 引用，外键 RESTRICT 会使本迁移显式失败；
-- 此时必须先走正常撤权流程，禁止在迁移里静默删除授权关系。

DELETE FROM iam_permission
WHERE code IN (
    'iam:menu:read',
    'iam:menu:write'
);
