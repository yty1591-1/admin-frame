-- 000030 (down): 回滚配置中心域权限点种子。
--
-- 只删权限点与授权；system_admin 角色不属于本迁移，不动。

DELETE FROM iam_role_permission
WHERE permission_code IN ('config:publish', 'config:approve', 'config:secret:read',
                          'config:secret:write', 'config:secret:approve');
DELETE FROM iam_permission
WHERE code IN ('config:publish', 'config:approve', 'config:secret:read',
               'config:secret:write', 'config:secret:approve');
