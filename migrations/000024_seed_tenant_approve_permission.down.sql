-- 回滚：撤销权限点（先删角色绑定，避免外键约束）。
DELETE FROM iam_role_permission WHERE permission_code = 'tenant:approve';
DELETE FROM iam_permission WHERE code = 'tenant:approve';
