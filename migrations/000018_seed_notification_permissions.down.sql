-- 000018 (down): 回滚通知域权限点种子。
--
-- 只删权限点与授权；system_admin 角色不属于本迁移，不动。

DELETE FROM iam_role_permission WHERE permission_code IN ('notification:read', 'notification:write');
DELETE FROM iam_permission WHERE code IN ('notification:read', 'notification:write');
