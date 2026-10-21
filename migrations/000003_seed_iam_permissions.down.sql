-- 回滚权限点目录种子。
-- DATA-02：若这些权限点仍被 iam_role_permission 引用，外键 RESTRICT 会使本迁移显式失败。
-- 此时必须先通过正常撤权流程清理授权，禁止在迁移里静默删除授权关系（会绕过审计）。

DELETE FROM iam_permission
WHERE code IN (
    'iam:permission:read',
    'iam:role:read',
    'iam:role:write',
    'iam:role:grant',
    'iam:binding:read',
    'iam:binding:write',
    'iam:check',
    'iam:change:read',
    'iam:change:approve',
    'iam:breakglass:request',
    'iam:breakglass:approve',
    'order:read',
    'order:write',
    'file:read',
    'file:write',
    'audit:read',
    'data:export',
    'config:read',
    'config:write'
);
