-- 000041 (up): 向 system_admin 补授 iam:breakglass:approve。
--
-- 背景：000005 刻意排除该权限以满足「申请 ≠ 审批」；现约定超级管理员
-- 持有平台全量权限，破玻璃可自批并仅记审计（sod_bypass）。
-- 影响：存量 system_admin 获得破玻璃审批能力；普通主体 SoD 不变。

SET @now = UTC_TIMESTAMP(3);

SET @role_id := (SELECT id FROM iam_role
                 WHERE channel = 'admin' AND merchant_id = '' AND code = 'system_admin' LIMIT 1);

INSERT INTO iam_role_permission (role_id, permission_code, granted_by, created_at)
SELECT @role_id, 'iam:breakglass:approve', 'migration-system-admin-breakglass', @now
WHERE @role_id IS NOT NULL
ON DUPLICATE KEY UPDATE granted_by = 'migration-system-admin-breakglass';
