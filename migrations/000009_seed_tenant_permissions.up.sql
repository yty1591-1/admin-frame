-- ENT-09：租户域权限点种子。
--
-- 只授予平台级 system_admin：租户是平台运营动作，B 端商户不应管理自身租户记录
-- （否则等于让租户自行「恢复」被停用的账号，绕过停用语义）。
-- 权限逐项授予、不写通配符（iam.security.allow_wildcard=false）。
-- 不种菜单：租户管理页尚未交付，先种菜单会产生点不开的死入口（与 000003 的 disabled 约定一致）。

SET @now = UTC_TIMESTAMP(3);

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('tenant:read', '查看租户', 'admin', 0, 285, 'active', @now, @now),
    ('tenant:write', '管理租户', 'admin', 0, 295, 'active', @now, @now)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    channel = VALUES(channel),
    sort = VALUES(sort),
    status = 'active',
    updated_at = @now;

-- 授予已存在的 system_admin（000005 建立）；全新库上该角色此时已存在。
SET @role_id := (SELECT id FROM iam_role
                 WHERE channel = 'admin' AND merchant_id = '' AND code = 'system_admin' LIMIT 1);

INSERT INTO iam_role_permission (role_id, permission_code, granted_by, created_at)
SELECT @role_id, p.code, 'migration-tenant-domain', @now
FROM iam_permission p
WHERE p.code IN ('tenant:read', 'tenant:write')
  AND @role_id IS NOT NULL
ON DUPLICATE KEY UPDATE granted_by = 'migration-tenant-domain';
