-- 回滚 000012：移除拆分权限的授权与权限点。
--
-- 注意：先删授权再删权限点，避免外键约束失败；回滚后旧 write 权限仍可完成原操作。

DELETE rp FROM iam_role_permission rp
JOIN iam_permission p ON p.code = rp.permission_code
WHERE p.code IN (
    'identity:account:credential',
    'identity:account:delete',
    'identity:account:session'
);

DELETE FROM iam_permission
WHERE code IN (
    'identity:account:credential',
    'identity:account:delete',
    'identity:account:session'
);
