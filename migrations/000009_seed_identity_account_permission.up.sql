-- 身份域 A 端账号管理权限点（IDN-P0-02）。
-- 端点：/admin/v1/auth/accounts/{id}/(disable|enable|unlock|mfa/reset)。
-- 只追加、不改写已应用种子；幂等：重复执行只刷新元数据。
-- 授权由运维按最小权限流程授予，迁移不自动绑定任何角色。
-- 与 internal/iam/perm.go、internal/iam/catalog、docs/iam/PERMISSIONS.md 保持一致。

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('identity:account:read', '查看身份账号', 'admin', 0, 255, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('identity:account:write', '管理身份账号', 'admin', 0, 260, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
AS new
ON DUPLICATE KEY UPDATE
    name         = new.name,
    channel      = new.channel,
    data_bearing = new.data_bearing,
    sort         = new.sort,
    status       = new.status,
    updated_at   = UTC_TIMESTAMP(3);
