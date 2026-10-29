-- 回滚 IAM 基线结构（含菜单、RBAC1 继承与 RBAC2 约束）。
-- 顺序必须先子后父：外键为 RESTRICT，父表仍有子表引用时 DROP 会失败。
-- 该迁移会丢失全部 IAM 授权与菜单数据，只允许在非生产环境或已确认清理的前提下执行。

DROP TABLE IF EXISTS iam_role_prerequisite;
DROP TABLE IF EXISTS iam_sod_group_role;
DROP TABLE IF EXISTS iam_sod_group;
DROP TABLE IF EXISTS iam_menu;
DROP TABLE IF EXISTS iam_change_request;
DROP TABLE IF EXISTS iam_role_binding;
DROP TABLE IF EXISTS iam_role_data_scope;
DROP TABLE IF EXISTS iam_role_permission;
DROP TABLE IF EXISTS iam_role_template_permission;
DROP TABLE IF EXISTS iam_role_template_data_scope;
DROP TABLE IF EXISTS iam_role_template;
DROP TABLE IF EXISTS iam_role;
DROP TABLE IF EXISTS iam_permission;
