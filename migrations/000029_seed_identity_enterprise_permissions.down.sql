-- 000029 (down): 回滚企业身份扩展权限点种子。
--
-- 只删权限点与授权；system_admin 角色不属于本迁移，不动。
DELETE FROM iam_role_permission
 WHERE permission_code IN (
        'identity:m2m:read', 'identity:m2m:write', 'identity:m2m:credential',
        'identity:scim:read', 'identity:scim:write'
     );

DELETE FROM iam_permission
 WHERE code IN (
        'identity:m2m:read', 'identity:m2m:write', 'identity:m2m:credential',
        'identity:scim:read', 'identity:scim:write'
     );
