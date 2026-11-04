-- 000021 (down): 回滚特性开关域权限点种子。
--
-- 只删权限点与授权；system_admin 角色不属于本迁移，不动。

DELETE FROM iam_role_permission WHERE permission_code IN ('featureflag:read', 'featureflag:write');
DELETE FROM iam_permission WHERE code IN ('featureflag:read', 'featureflag:write');
