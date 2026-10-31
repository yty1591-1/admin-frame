-- 身份域账号权限拆分（IDN-GAP-P1-14 / SEC-02）。
--
-- 背景：
--   原只有 identity:account:read / write；重置密码、重置 MFA、删除账号、撤销会话
--   全部共用 write，任一持有 write 的管理员即可完成账号接管链。
-- 变更：
--   新增 identity:account:credential / delete / session 三个权限点，并先授权给
--   所有已持有 identity:account:write 的角色，保证路由切换后行为不变（先授权后切路由）。
-- 兼容：
--   本迁移只新增授权，不回收既有 write；回滚删除新增权限与授权即可。
--   新增权限点只能追加，禁止改写既有 code 语义。

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('identity:account:credential', '重置身份凭据', 'admin', 0, 265, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('identity:account:delete', '删除身份账号', 'admin', 0, 270, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('identity:account:session', '撤销身份会话', 'admin', 0, 275, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
AS new
ON DUPLICATE KEY UPDATE
    name         = new.name,
    channel      = new.channel,
    data_bearing = new.data_bearing,
    sort         = new.sort,
    status       = new.status,
    updated_at   = UTC_TIMESTAMP(3);

-- 先授权：把新权限授予所有已持有 identity:account:write 的角色。
INSERT INTO iam_role_permission (role_id, permission_code, granted_by, created_at)
SELECT rp.role_id, new_perm.code, 'migration-000012', UTC_TIMESTAMP(3)
FROM iam_role_permission rp
JOIN iam_permission old_perm ON old_perm.code = 'identity:account:write'
JOIN iam_permission new_perm
    ON new_perm.code IN (
        'identity:account:credential',
        'identity:account:delete',
        'identity:account:session'
    )
WHERE rp.permission_code = old_perm.code
  AND new_perm.status = 'active'
ON DUPLICATE KEY UPDATE granted_by = 'migration-000012';
