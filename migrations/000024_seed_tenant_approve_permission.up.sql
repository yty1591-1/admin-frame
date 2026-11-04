-- ENT-09 审核流：新增审批权限点 tenant:approve。
--
-- 与 tenant:write 分开授予：提交申请的人不应同时具备批准自己申请的权限。
-- 领域层还有第二道校验（提交人 ≠ 审批人），权限拆分让职责分离在授权层就成立。
-- 只授予平台级 system_admin：租户审核是平台运营动作（与 000009 同口径）。
SET @now = UTC_TIMESTAMP(3);

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('tenant:approve', '审核租户入驻', 'admin', 0, 296, 'active', @now, @now)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    channel = VALUES(channel),
    sort = VALUES(sort),
    status = 'active',
    updated_at = @now;

SET @role_id := (SELECT id FROM iam_role
                 WHERE channel = 'admin' AND merchant_id = '' AND code = 'system_admin' LIMIT 1);

INSERT INTO iam_role_permission (role_id, permission_code, granted_by, created_at)
SELECT @role_id, 'tenant:approve', 'migration-tenant-approval', @now
WHERE @role_id IS NOT NULL
ON DUPLICATE KEY UPDATE granted_by = 'migration-tenant-approval';
