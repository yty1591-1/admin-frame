-- 回滚 RBAC1 角色层级：移除 parent_id 及其自引用外键。
-- 回滚后既有子角色全部退化为根角色，继承来的权限不再展开；
-- 只允许在确认业务已接受该语义变化后执行。

ALTER TABLE iam_role
    DROP FOREIGN KEY fk_iam_role_parent,
    DROP KEY idx_iam_role_parent,
    DROP COLUMN parent_id;
