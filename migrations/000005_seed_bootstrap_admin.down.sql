-- 回滚首个管理员 bootstrap。
--
-- 仅移除迁移自身创建的数据：
--   * 角色 / 授权 / 数据范围 / 绑定 按 system_admin 角色收敛删除；
--   * 账号与凭据只在主键仍是迁移写入的保留 ID 时删除，避免误删已有管理员；
--   * 审计事件按固定 event_id 删除。
-- 若账号已产生会话，删除会因外键 RESTRICT 失败，属预期保护。

SET @subject = 'admin@example.com';
SET @bootstrap_account_id = 1900000000000000002;
SET @bootstrap_event_id = '1900000000000000300';
SET @role_id := (SELECT id FROM iam_role
                 WHERE channel = 'admin' AND merchant_id = '' AND code = 'system_admin' LIMIT 1);

-- 绑定可能指向「账号已存在时复用」的原账号，因此按角色收敛删除，而非固定账号 ID。
DELETE FROM iam_role_binding WHERE role_id = @role_id;

DELETE FROM iam_role_permission WHERE role_id = @role_id;
DELETE FROM iam_role_data_scope WHERE role_id = @role_id;

DELETE FROM iam_role
WHERE id = @role_id AND channel = 'admin' AND merchant_id = '' AND code = 'system_admin';

DELETE FROM async_outbox WHERE id = @bootstrap_event_id;

DELETE FROM identity_credential WHERE account_id = @bootstrap_account_id;
DELETE FROM identity_account
WHERE id = @bootstrap_account_id AND channel = 'admin' AND subject = @subject;
