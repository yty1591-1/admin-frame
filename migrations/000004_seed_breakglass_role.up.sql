-- 破玻璃角色种子（docs/rbac-iam.md §8.5）。
-- 该角色不持有任何权限点：只有主体在审批有效期内的绑定存在时，快照才动态展开 "*"。
-- 幂等：重复执行不覆盖既有状态（避免把运维手工停用的角色重新激活）。
-- 固定主键放在保留段（19xxxxxxxxxxxxxxxxxx），避免与雪花 ID 冲突。

INSERT INTO iam_role
    (id, channel, scope_type, merchant_id, code, name, description, role_type, status,
        version, created_by, updated_by, created_at, updated_at, deleted_at)
VALUES
    (1900000000000000001, 'admin', 'platform', '', 'breakglass_admin', '破玻璃超级权限',
        '系统内置：仅在有效破玻璃审批绑定期间由快照展开 *，不得直接授予',
        'breakglass', 'active', 1, 'system', 'system', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3), NULL)
ON DUPLICATE KEY UPDATE id = id;
