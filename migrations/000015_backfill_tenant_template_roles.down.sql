-- 回滚 000015：只撤销本迁移创建的角色 / 授权 / 绑定。
--
-- 识别标记：created_by = 'migration-tenant-iam'（角色与授权），
-- 绑定按「role_id 属于这些模板角色且 granted_by 相同」清理。
-- 运营后续对模板角色的调整（改权限 / 新增 custom 绑定）不在本回滚范围——
-- 回滚前应确认这些行尚未被人工修改。

-- 绑定（引用模板角色的行）。
DELETE FROM iam_role_binding
WHERE granted_by = 'migration-tenant-iam'
  AND role_id IN (
    SELECT id FROM (
      SELECT id FROM iam_role
      WHERE channel = 'biz' AND created_by = 'migration-tenant-iam'
    ) AS tpl
  );

-- 授权。
DELETE FROM iam_role_permission
WHERE granted_by = 'migration-tenant-iam'
  AND role_id IN (
    SELECT id FROM (
      SELECT id FROM iam_role
      WHERE channel = 'biz' AND created_by = 'migration-tenant-iam'
    ) AS tpl
  );

-- 模板角色。
DELETE FROM iam_role
WHERE channel = 'biz' AND created_by = 'migration-tenant-iam';
