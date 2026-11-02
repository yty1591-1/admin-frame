-- 身份域种子：IAM 系统管理员角色、A 端全量授权与数据范围基线；
-- 文件末尾附带身份域存量数据订正（原 000014 无效脱敏 IP 清理、原 000020 商户 ID 小写化）。
--
-- 首个管理员账号与密码凭据不在迁移中写入：登录主体必须由应用密钥加密，SQL 无法完成；
-- A 端 admin-api 启动时由 identityruntime 调用 bootstrap.EnsureAdmin 在单事务内幂等创建
-- 账号、密码凭据、角色绑定与审计事件（见 internal/identity/bootstrap）。
-- 因此迁移不含明文账号数据，一次执行即可完成。
--
-- 设计约束：
--   1. 角色按 (channel, merchant_id, code) 收敛，已存在时复用原主键；
--   2. 角色逐项授予权限、不写通配符（iam.security.allow_wildcard=false）；
--      排除 iam:breakglass:approve，保证「申请 ≠ 审批」；
--   3. data_bearing 权限统一配置 all 数据范围，与权限点定义保持同步；
--   4. 存量订正在全新库上是空操作，重复执行幂等。

SET @now = UTC_TIMESTAMP(3);
SET @bootstrap_role_id = 1900000000000000100;

-- 1) 系统管理员角色：按 (channel, merchant_id, code) 收敛，已存在时复用原主键
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

-- 2) 逐项授予全部生效的 A 端权限（排除破玻璃审批）
INSERT INTO iam_role_permission (role_id, permission_code, granted_by, created_at)
SELECT @role_id, p.code, 'system', @now
FROM iam_permission p
WHERE p.status = 'active'
  AND p.channel IN ('admin', 'both')
  AND p.code <> 'iam:breakglass:approve'
ON DUPLICATE KEY UPDATE granted_by = 'system';

-- 3) 数据范围：data_bearing 权限必须配置范围，引导角色统一 all
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

-- 4) 存量订正：清空历史设备会话中由回环 / 未指定地址派生的无效脱敏 IP（原 000014）。
--    早期 MaskIP 未过滤 127.0.0.1 / ::1，落库得到 127.0.x.x、0.0.x.x 或 0:0:0:0:x:x:x:x；
--    真实公网 / 内网 IP 的脱敏结果保持不变，原始值不可还原且无需还原。
UPDATE identity_session_family
SET ip_masked = ''
WHERE ip_masked IN ('0.0.x.x', '127.0.x.x')
   OR ip_masked LIKE '0:0:0:0:%';

-- 5) 存量订正：商户 ID 统一小写（原 000020）。
--    库排序规则大小写不敏感，`M-1` 与 `m-1` 会被唯一键判为重复，但应用按不同租户处理；
--    规范化后与唯一键口径对齐。若存量同时存在同一主体的两种大小写，UPDATE 会因唯一键
--    冲突失败，须先按原迁移注释中的预检语句人工合并。原始大小写不可恢复。
UPDATE identity_account
   SET merchant_id = LOWER(merchant_id)
 WHERE merchant_id <> LOWER(merchant_id);
