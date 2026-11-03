-- 回滚 ENT-09：删除租户域权限点及其授权。
--
-- 先删授权再删权限点：iam_role_permission.permission_code 对 iam_permission.code
-- 是逻辑引用（无外键），但保留孤儿授权会让快照展开时拿到已下线权限。

DELETE FROM iam_role_permission WHERE permission_code IN ('tenant:read', 'tenant:write');
DELETE FROM iam_permission WHERE code IN ('tenant:read', 'tenant:write');
