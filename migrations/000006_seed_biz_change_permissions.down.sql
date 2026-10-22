-- 回滚 000006：删除 B 端变更审批权限点。
--
-- 删除前必须先撤销引用这些权限点的角色授权（iam_role_permission）与绑定，
-- 否则外键会阻止删除；这是有意的保护：留下指向已删除权限点的角色定义
-- 会让「谁还能提申请」变成不可审计的黑洞。

DELETE FROM iam_permission
WHERE code IN ('iam:change:request', 'iam:change:review');
