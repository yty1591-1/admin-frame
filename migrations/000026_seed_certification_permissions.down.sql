-- 回滚：先删角色授权关系，再删权限点，避免外键约束。
DELETE FROM iam_role_permission WHERE permission_code IN (
    'iam:certification:read', 'iam:certification:write', 'iam:certification:decide'
);
DELETE FROM iam_permission WHERE code IN (
    'iam:certification:read', 'iam:certification:write', 'iam:certification:decide'
);
