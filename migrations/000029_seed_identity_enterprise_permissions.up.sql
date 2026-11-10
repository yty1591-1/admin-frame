-- 000029 (up): 企业身份扩展权限点种子（docs/identity-authn.md §21.1.6 / §21.2.4）。
--
-- 权限拆分原则与既有一致：
--   - read 只读列表 / 组映射，不触碰密钥；
--   - write 变更客户端生命周期（创建 / 停用 / 启用 / 软删）与 SCIM 令牌签发；
--   - credential 单独授予「轮换 secret」，避免「能建客户端」等于「能取走密钥」。
-- 只授予平台级 system_admin；B / C 端不出现这些权限点（身份域企业能力仅 A 端）。
-- 不种菜单：管理页尚未交付，避免产生点不开的死入口。

SET @now = UTC_TIMESTAMP(3);

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('identity:m2m:read', '查看 M2M 客户端', 'admin', 0, 280, 'active', @now, @now),
    ('identity:m2m:write', '管理 M2M 客户端', 'admin', 0, 281, 'active', @now, @now),
    ('identity:m2m:credential', '轮换 M2M 密钥', 'admin', 0, 282, 'active', @now, @now),
    ('identity:scim:read', '查看 SCIM 令牌', 'admin', 0, 283, 'active', @now, @now),
    ('identity:scim:write', '管理 SCIM 令牌', 'admin', 0, 284, 'active', @now, @now)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    channel = VALUES(channel),
    sort = VALUES(sort),
    status = 'active',
    updated_at = @now;

-- 授予已存在的 system_admin；全新库上该角色此时已存在（000005 建立）。
SET @role_id := (SELECT id FROM iam_role
                 WHERE channel = 'admin' AND merchant_id = '' AND code = 'system_admin' LIMIT 1);

INSERT INTO iam_role_permission (role_id, permission_code, granted_by, created_at)
SELECT @role_id, p.code, 'migration-identity-enterprise', @now
FROM iam_permission p
WHERE p.code IN (
        'identity:m2m:read', 'identity:m2m:write', 'identity:m2m:credential',
        'identity:scim:read', 'identity:scim:write'
    )
  AND @role_id IS NOT NULL
ON DUPLICATE KEY UPDATE granted_by = VALUES(granted_by);
