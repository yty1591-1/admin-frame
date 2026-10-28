-- 回滚 RBAC2 约束基线：移除互斥组、前置角色与角色基数约束。
-- 回滚后既有约束关系全部丢失，绑定不再被互斥 / 前置 / 基数规则拦截；
-- 只允许在确认业务已接受该语义变化后执行。

ALTER TABLE iam_role
    DROP CHECK ck_iam_role_max_bindings,
    DROP COLUMN max_bindings;

DROP TABLE IF EXISTS iam_role_prerequisite;
DROP TABLE IF EXISTS iam_sod_group_role;
DROP TABLE IF EXISTS iam_sod_group;
