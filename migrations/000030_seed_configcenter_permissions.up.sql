-- 000030 (up): 配置中心域权限点种子（ADR-0013 / ADR-0014）。
--
-- 配置中心写、发布、审批三权分离；密钥权限独立于普通 config:read/write，
-- 普通配置读取不得查看密钥元数据，更不得接触明文。
-- 只授予平台级 system_admin：配置中心是平台治理能力，B/C 端不提供端点。
-- 生产环境应按职责分离创建独立角色；审批强制「提交人 != 审批人」。
-- 不种菜单：管控页面随 M1/M2 交付时再种。

SET @now = UTC_TIMESTAMP(3);

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('config:publish', '发布配置', 'admin', 0, 375, 'active', @now, @now),
    ('config:approve', '审批配置发布', 'admin', 0, 385, 'active', @now, @now),
    ('config:secret:read', '查看密钥元数据', 'admin', 0, 395, 'active', @now, @now),
    ('config:secret:write', '管理业务密钥', 'admin', 0, 405, 'active', @now, @now),
    ('config:secret:approve', '审批密钥操作', 'admin', 0, 415, 'active', @now, @now)
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
SELECT @role_id, p.code, 'migration-configcenter-domain', @now
FROM iam_permission p
WHERE p.code IN ('config:publish', 'config:approve', 'config:secret:read',
                 'config:secret:write', 'config:secret:approve')
  AND @role_id IS NOT NULL
ON DUPLICATE KEY UPDATE granted_by = VALUES(granted_by);
