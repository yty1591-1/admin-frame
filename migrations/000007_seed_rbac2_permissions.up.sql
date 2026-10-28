-- RBAC2 权限点追加种子：互斥角色组（SOD）读 / 写。
-- 只追加、不改写已应用的 000003 基线；幂等：重复执行只刷新元数据。
-- 与 internal/iam/perm.go、internal/iam/catalog、docs/iam/PERMISSIONS.md 保持一致。
-- 每条权限点独占一行，行首为 "('"，列顺序与 000003 的 INSERT 列表一致。

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('iam:sod:read', '查看互斥组', 'both', 0, 240, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:sod:write', '管理互斥组', 'both', 0, 250, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
AS new
ON DUPLICATE KEY UPDATE
    name         = new.name,
    channel      = new.channel,
    data_bearing = new.data_bearing,
    sort         = new.sort,
    status       = new.status,
    updated_at   = UTC_TIMESTAMP(3);
