-- 回滚 IAM 域基线种子：菜单 → 破玻璃角色 → 权限目录（逆序，先删被引用方）。
-- DATA-02：若权限点仍被 iam_role_permission 引用，外键 RESTRICT 会使本迁移显式失败；
-- 此时必须先走正常撤权流程，禁止在迁移里静默删除授权关系（会绕过审计）。
-- 破玻璃角色若存在历史绑定，同样由外键阻止删除（保留审计线索，需人工评估）。

DELETE FROM iam_menu WHERE channel = 'admin' AND code IN (
    'system.role', 'system.menu', 'system.audit', 'system.config'
);
DELETE FROM iam_menu WHERE channel = 'admin' AND code = 'system';

DELETE FROM iam_menu WHERE channel = 'admin' AND code IN (
    'iam.permission', 'iam.binding', 'iam.template', 'iam.change', 'iam.breakglass',
    'iam.check', 'iam.sod'
);
DELETE FROM iam_menu WHERE channel = 'admin' AND code = 'iam';

DELETE FROM iam_menu WHERE channel = 'biz' AND code IN (
    'member.list', 'member.role', 'member.binding', 'member.template', 'member.change'
);
DELETE FROM iam_menu WHERE channel = 'biz' AND code = 'member';

DELETE FROM iam_role
WHERE id = 1900000000000000001
  AND channel = 'admin'
  AND role_type = 'breakglass';

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
    'config:write',
    'iam:change:request',
    'iam:change:review',
    'iam:menu:read',
    'iam:menu:write',
    'iam:sod:read',
    'iam:sod:write',
    'identity:account:read',
    'identity:account:write'
);
