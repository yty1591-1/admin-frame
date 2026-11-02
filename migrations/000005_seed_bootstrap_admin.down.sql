-- 回滚身份域种子：删除 system_admin 角色及其授权、数据范围、绑定。
--
-- 账号与凭据由 A 端启动引导创建，不随本迁移回滚：
-- 回滚后管理员账号仍存在但不再拥有任何权限；如需回收账号，应走账号管理流程，
-- 避免在此误删已有账号或依赖固定主键。
-- 注意：绑定按角色收敛删除，启动引导为系统管理员创建的绑定也会一并移除。
-- 文件末尾的两段存量订正（无效脱敏 IP 清理、商户 ID 小写化）不可逆：原始值未保留，
-- 应用层已按新口径读写，因此 down 不做数据恢复（详见原 000014 / 000020 的回滚说明）。

SET @role_id := (SELECT id FROM iam_role
                 WHERE channel = 'admin' AND merchant_id = '' AND code = 'system_admin' LIMIT 1);

DELETE FROM iam_role_binding WHERE role_id = @role_id;
DELETE FROM iam_role_permission WHERE role_id = @role_id;
DELETE FROM iam_role_data_scope WHERE role_id = @role_id;

DELETE FROM iam_role
WHERE id = @role_id AND channel = 'admin' AND merchant_id = '' AND code = 'system_admin';
