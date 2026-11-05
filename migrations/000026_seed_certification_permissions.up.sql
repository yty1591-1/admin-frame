-- P1-7 访问复核权限点。
--
-- 与权限目录（internal/iam/catalog）和 PERMISSIONS.md 同步；读取 / 管理 / 裁决
-- 分开授予，便于把「发起复核」与「裁决回收」交给不同角色（职责分离）。
-- 只授予平台级 system_admin：季度复核是平台治理动作。
SET @now = UTC_TIMESTAMP(3);

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('iam:certification:read', '查看访问复核', 'admin', 0, 345, 'active', @now, @now),
    ('iam:certification:write', '管理访问复核', 'admin', 0, 355, 'active', @now, @now),
    ('iam:certification:decide', '裁决复核任务', 'admin', 0, 365, 'active', @now, @now)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    channel = VALUES(channel),
    sort = VALUES(sort),
    status = 'active',
    updated_at = @now;

SET @role_id := (SELECT id FROM iam_role
                 WHERE channel = 'admin' AND merchant_id = '' AND code = 'system_admin' LIMIT 1);

INSERT INTO iam_role_permission (role_id, permission_code, granted_by, created_at)
SELECT @role_id, code, 'migration-access-certification', @now
FROM (
    SELECT 'iam:certification:read' AS code
    UNION ALL SELECT 'iam:certification:write'
    UNION ALL SELECT 'iam:certification:decide'
) AS permissions
WHERE @role_id IS NOT NULL
ON DUPLICATE KEY UPDATE granted_by = 'migration-access-certification';
