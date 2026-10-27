-- RBAC1 角色继承：iam_role 增加 parent_id，形成「同端别 + 同商户」内的单亲角色树。
-- 设计要点：
--   1. parent_id 为 NULL 表示根角色（无继承）；父角色必须与子角色同 channel / merchant_id，
--      由复合自引用外键在库层拒绝跨端 / 跨商户继承；
--   2. 继承语义为「向上继承」：子角色自动获得全部祖先角色的权限点与数据范围，
--      鉴权快照按绑定角色向上递归展开（见 internal/iam/infrastructure/persistence/mysql_snapshot.go）；
--   3. 破玻璃角色不参与继承：既不能有父角色，也不能作为父角色，
--      避免未走审批的普通角色通过继承获得 "*"；
--   4. 直接 / 间接环由应用层在写入 parent_id 时检测，并限制最大深度
--      domain.MaxRoleHierarchyDepth；库层只能拦住单行自引用之外的间接环；
--   5. 角色软删不级联子角色：删除前必须先把子角色改挂到其它父角色或提升为根角色，
--      否则子树会静默丢失继承来的权限。

ALTER TABLE iam_role
    ADD COLUMN parent_id BIGINT NULL DEFAULT NULL
        COMMENT '父角色ID；NULL=根角色（无继承）；父角色须同端别、同商户' AFTER merchant_id,
    ADD KEY idx_iam_role_parent (parent_id, channel, merchant_id)
        COMMENT '按父角色查子树，同时作为复合外键的索引',
    ADD CONSTRAINT fk_iam_role_parent FOREIGN KEY (parent_id, channel, merchant_id)
        REFERENCES iam_role (id, channel, merchant_id)
        ON DELETE RESTRICT ON UPDATE RESTRICT;
