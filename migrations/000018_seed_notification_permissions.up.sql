-- 000018 (up): 通知域权限点种子（ENT-10 NT-06）。
--
-- 只授予平台级 system_admin：通知是平台运营触达能力，B 端只读自己租户的
-- 通知（无权限点，走 CurrentTenant 隔离的查询接口）。
-- 权限逐项授予、不写通配符（iam.security.allow_wildcard=false）。
-- 不种菜单：通知管理页尚未交付，先种菜单会产生点不开的死入口。

SET @now = UTC_TIMESTAMP(3);

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('notification:read', '查看通知', 'admin', 0, 305, 'active', @now, @now),
    ('notification:write', '管理通知', 'admin', 0, 315, 'active', @now, @now)
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
SELECT @role_id, p.code, 'migration-notification-domain', @now
FROM iam_permission p
WHERE p.code IN ('notification:read', 'notification:write')
  AND @role_id IS NOT NULL
ON DUPLICATE KEY UPDATE granted_by = VALUES(granted_by);
