-- 000021 (up): 特性开关域权限点种子（ENT-11 FF-05）。
--
-- 只授予平台级 system_admin：flag 管理是平台级治理能力（kill switch、
-- 灰度配比），B 端与 C 端不提供任何 flag 端点（不泄露功能面）。
-- 权限逐项授予、不写通配符（iam.security.allow_wildcard=false）。
-- 不种菜单：flag 管理页尚未交付，先种菜单会产生点不开的死入口。

SET @now = UTC_TIMESTAMP(3);

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('featureflag:read', '查看特性开关', 'admin', 0, 325, 'active', @now, @now),
    ('featureflag:write', '管理特性开关', 'admin', 0, 335, 'active', @now, @now)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    channel = VALUES(channel),
    sort = VALUES(sort),
    status = 'active',
    updated_at = @now;

-- 授予已存在的 system_admin（000005 建立）。
SET @role_id := (SELECT id FROM iam_role
                 WHERE channel = 'admin' AND merchant_id = '' AND code = 'system_admin' LIMIT 1);

INSERT INTO iam_role_permission (role_id, permission_code, granted_by, created_at)
SELECT @role_id, p.code, 'migration-featureflag-domain', @now
FROM iam_permission p
WHERE p.code IN ('featureflag:read', 'featureflag:write')
  AND @role_id IS NOT NULL
ON DUPLICATE KEY UPDATE granted_by = VALUES(granted_by);
