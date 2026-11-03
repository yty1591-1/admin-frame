-- ENT-09 T-18：存量租户的 IAM 模板角色回填（ADR-0007）。
--
-- 背景：租户成员角色（owner/admin/member）此前的 IAM 联动缺失（P1-8）。
-- 按 ADR-0007，每个租户物化三个模板角色（与配额物化同模式），
-- 成员变更时同步绑定到对应模板角色。
--
-- 本迁移只覆盖**存量**租户；新租户由 app 层 Create 事务内 EnsureTemplates 物化。
--
-- 规则：
--   1. 每个租户三个模板角色：(biz, merchant, tenant_id, code IN tenant_owner / tenant_admin / tenant_member)；
--      id 用 UUID_SHORT() 生成，幂等由 uk_iam_role_scope_code (channel, merchant_id, code) 兜底；
--   2. 模板权限按 ADR-0007 最小集授予（iam_role_permission，幂等靠主键）；
--   3. 为每个在册成员回填绑定（幂等靠 uk_iam_binding），id 用 UUID_SHORT()；
--   4. 已停用成员不回填绑定（复活时由应用层 SyncRole 重绑）；
--   5. 不删除任何既有行——回滚只撤销本迁移新增的角色 / 授权 / 绑定。
--
-- 说明：模板角色 id 无法用确定性算术派生（tenant.id 来自 merchant_id 回填时
-- 可能是非数字字符串），因此用 UUID_SHORT()；重跑安全（uk 拦截已存在行）。

SET @now = UTC_TIMESTAMP(3);

-- 1) 模板角色：每租户三行（幂等）。
INSERT INTO iam_role
    (id, channel, scope_type, merchant_id, code, name, description, role_type, status,
        version, created_by, updated_by, created_at, updated_at, deleted_at)
-- 注意：JOIN 不带 ON 子句直接接 ON DUPLICATE KEY 会产生解析歧义（Error 1064，
-- 同 000011 的教训），因此用逗号连接（CROSS JOIN）。
SELECT UUID_SHORT(), 'biz', 'merchant', t.id, r.code, r.name, r.description, 'system', 'active',
       1, 'migration-tenant-iam', 'migration-tenant-iam', @now, @now, NULL
FROM tenant t,
     (
    SELECT 'tenant_owner'  AS code, '租户所有者' AS name, '系统内置：owner 成员的默认角色（ADR-0007）' AS description
    UNION ALL SELECT 'tenant_admin', '租户管理员', '系统内置：admin 成员的默认角色（ADR-0007）'
    UNION ALL SELECT 'tenant_member', '租户成员', '系统内置：member 成员的默认角色（ADR-0007）'
) r
ON DUPLICATE KEY UPDATE updated_at = @now;

-- 2) 模板权限（最小集，ADR-0007；biz 权限点须已存在于 iam_permission）。
--    owner：变更发起 + 商户内审批 + 只读；admin：变更发起 + 只读；member：基础只读。
INSERT INTO iam_role_permission (role_id, permission_code, granted_by, created_at)
SELECT ro.id, g.grants, 'migration-tenant-iam', @now
FROM iam_role ro
JOIN (
    SELECT 'tenant_owner'  AS code, 'iam:change:request' AS grants
    UNION ALL SELECT 'tenant_owner',  'iam:change:review'
    UNION ALL SELECT 'tenant_owner',  'iam:role:read'
    UNION ALL SELECT 'tenant_owner',  'iam:binding:read'
    UNION ALL SELECT 'tenant_owner',  'iam:sod:read'
    UNION ALL SELECT 'tenant_admin',  'iam:change:request'
    UNION ALL SELECT 'tenant_admin',  'iam:role:read'
    UNION ALL SELECT 'tenant_admin',  'iam:binding:read'
    UNION ALL SELECT 'tenant_admin',  'iam:sod:read'
    UNION ALL SELECT 'tenant_member', 'iam:role:read'
) g ON g.code = ro.code
JOIN iam_permission p ON p.code = g.grants AND p.channel IN ('biz', 'both') AND p.status = 'active'
WHERE ro.channel = 'biz' AND ro.merchant_id <> '' AND ro.deleted_at IS NULL
ON DUPLICATE KEY UPDATE granted_by = 'migration-tenant-iam';

-- 3) 在册成员回填绑定（停用成员跳过；幂等靠 uk_iam_binding）。
INSERT INTO iam_role_binding
    (id, channel, subject_type, subject_id, merchant_id, role_id, expires_at,
        granted_by, created_at, revoked_at, revoked_by, revoke_reason)
SELECT UUID_SHORT(), 'biz', 'merchant_staff', m.subject_id, m.tenant_id, ro.id, NULL,
       'migration-tenant-iam', @now, NULL, '', ''
FROM tenant_member m
JOIN tenant t ON t.id = m.tenant_id
JOIN iam_role ro
  ON ro.channel = 'biz' AND ro.merchant_id = m.tenant_id
 AND ro.code = CONCAT('tenant_', m.role) AND ro.deleted_at IS NULL
WHERE m.status = 'active'
ON DUPLICATE KEY UPDATE granted_by = 'migration-tenant-iam';
