-- 菜单管理权限点（A 端专属）。
-- 菜单 CRUD 只影响前端入口，不构成接口鉴权边界；B 端菜单仍由平台种子维护。
-- 追加式种子：不得改写既有权限码语义；本文件与 catalog / perm.go / PERMISSIONS.md 保持一致。
-- 每条权限点独占一行，行首为 "('"，供 internal/iam/catalog 的一致性测试解析。

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('iam:menu:read', '查看菜单资源', 'admin', 0, 220, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:menu:write', '管理菜单资源', 'admin', 0, 230, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
AS new
ON DUPLICATE KEY UPDATE
    name         = new.name,
    channel      = new.channel,
    data_bearing = new.data_bearing,
    sort         = new.sort,
    status       = new.status,
    updated_at   = UTC_TIMESTAMP(3);
