-- 首个管理员 bootstrap：账号 + 密码凭据 + IAM 系统管理员角色 + A 端全量授权 + 绑定。
--
-- 设计约束：
--   1. 只在账号不存在时创建，已有环境（历史上用 CLI 开通过）不会被覆盖、改密或重复审计；
--   2. 初始口令是公开的演练值，因此写入 must_change_password=1，首次登录必须先改密，
--      改密后才进入 MFA 强制绑定流程（A 端 admin_required=true）；
--   3. 角色逐项授予权限、不写通配符（iam.security.allow_wildcard=false）；
--      排除 iam:breakglass:approve，保证「申请 ≠ 审批」；
--   4. 直接写库属于一次性引导，不走审批；账号创建审计与业务写入同一事务落 Outbox。
--
-- 如需更换首个管理员登录名，只改 @subject；重复执行不会重建已被修改的密码。

SET @now = UTC_TIMESTAMP(3);
SET @subject = 'admin@example.com';
SET @bootstrap_account_id = 1900000000000000002;
SET @bootstrap_credential_id = 1900000000000000003;
SET @bootstrap_role_id = 1900000000000000100;
SET @bootstrap_binding_id = 1900000000000000200;
SET @bootstrap_event_id = '1900000000000000300';
SET @existing_account := (SELECT id FROM identity_account
                          WHERE channel = 'admin' AND subject = @subject LIMIT 1);
SET @account_id := COALESCE(@existing_account, @bootstrap_account_id);

-- 1) 账号：仅首次创建；status=active，登录时无需再激活
INSERT INTO identity_account
    (id, channel, subject, status, token_version, version, merchant_id,
     password_updated_at, locked_until, must_change_password, created_at, updated_at, deleted_at)
SELECT @bootstrap_account_id, 'admin', @subject, 'active', 1, 1, '',
       @now, NULL, 1, @now, @now, NULL
WHERE @existing_account IS NULL;

-- 2) 密码凭据：仅在账号尚无密码凭据时写入（Argon2id，口令见 docs/identity-authn.md）密码：Admin#Bootstrap2026
INSERT INTO identity_credential
    (id, account_id, credential_type, credential_hash, algorithm, created_at, updated_at)
SELECT @bootstrap_credential_id, @account_id, 'password',
       '$argon2id$v=19$m=19456,t=2,p=1$bGxqpo2HjthAlN+YLfpSkA$4FQGp1ifQnkGYCoMDYwN/pmejWVtI5K3Rh/8/uip//I',
       'argon2id', @now, @now
WHERE NOT EXISTS (
    SELECT 1 FROM identity_credential c
    WHERE c.account_id = @account_id AND c.credential_type = 'password');

-- 3) 系统管理员角色：按 (channel, merchant_id, code) 收敛，已存在时复用原主键
INSERT INTO iam_role
    (id, channel, scope_type, merchant_id, code, name, description, role_type, status,
     version, created_by, updated_by, created_at, updated_at, deleted_at)
VALUES
    (@bootstrap_role_id, 'admin', 'platform', '', 'system_admin', '系统管理员',
     '引导用超级管理员：逐项授予全部 A 端权限，不写通配符',
     'system', 'active', 1, 'system', 'system', @now, @now, NULL)
ON DUPLICATE KEY UPDATE
    name = '系统管理员',
    status = 'active',
    deleted_at = NULL,
    updated_at = @now;

SET @role_id := (SELECT id FROM iam_role
                 WHERE channel = 'admin' AND merchant_id = '' AND code = 'system_admin' LIMIT 1);

-- 4) 逐项授予全部生效的 A 端权限（排除破玻璃审批）
INSERT INTO iam_role_permission (role_id, permission_code, granted_by, created_at)
SELECT @role_id, p.code, 'system', @now
FROM iam_permission p
WHERE p.status = 'active'
  AND p.channel IN ('admin', 'both')
  AND p.code <> 'iam:breakglass:approve'
ON DUPLICATE KEY UPDATE granted_by = 'system';

-- 5) 数据范围：data_bearing 权限必须配置范围，引导角色统一 all
INSERT INTO iam_role_data_scope
    (role_id, channel, resource, scope_type, scope_values, granted_by, created_at, updated_at)
SELECT DISTINCT @role_id, 'admin', p.resource, 'all', JSON_OBJECT(), 'system', @now, @now
FROM iam_permission p
WHERE p.status = 'active'
  AND p.channel IN ('admin', 'both')
  AND p.data_bearing = 1
ON DUPLICATE KEY UPDATE
    scope_type = 'all',
    scope_values = JSON_OBJECT(),
    updated_at = @now;

-- 6) 绑定：A 端员工，merchant_id 为空串；已有生效绑定则跳过
INSERT INTO iam_role_binding
    (id, channel, subject_type, subject_id, merchant_id, role_id,
     expires_at, granted_by, created_at, revoked_at, revoked_by, revoke_reason)
SELECT @bootstrap_binding_id, 'admin', 'staff', CAST(@account_id AS CHAR), '', @role_id,
       NULL, 'system', @now, NULL, '', ''
WHERE NOT EXISTS (
    SELECT 1 FROM iam_role_binding b
    WHERE b.channel = 'admin' AND b.subject_type = 'staff'
      AND b.subject_id = CAST(@account_id AS CHAR)
      AND b.merchant_id = '' AND b.role_id = @role_id AND b.revoked_at IS NULL);

-- 7) 审计：仅在本次迁移创建了账号时写入 Outbox，与账号创建同事务
INSERT INTO async_outbox
    (id, topic, event_type, version, tenant_id, aggregate_type, aggregate_id,
     idempotency_key, trace_id, payload, status, attempts, next_retry_at, last_error,
     occurred_at, created_at, updated_at)
SELECT @bootstrap_event_id, 'identity.audit', 'audit.event', 'v1', '', 'account',
       CAST(@account_id AS CHAR), '', 'bootstrap-admin',
       JSON_OBJECT(
           'event_id', @bootstrap_event_id,
           'action', 'auth.account.created',
           'actor_id', 'migration:bootstrap',
           'actor_type', 'operator',
           'channel', 'admin',
           'resource_type', 'account',
           'resource_id', CAST(@account_id AS CHAR),
           'outcome', 'success',
           'occurred_at', DATE_FORMAT(@now, '%Y-%m-%dT%H:%i:%s.%fZ'),
           'trace_id', 'bootstrap-admin',
           'service', 'migration',
           'attrs', JSON_OBJECT('subject', @subject)
       ),
       'pending', 0, NULL, '',
       @now, @now, @now
WHERE @existing_account IS NULL;
