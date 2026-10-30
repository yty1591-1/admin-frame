-- 移除个人中心菜单 / 路由登记。
-- 子节点先行删除，避免自引用外键 RESTRICT；本菜单无子节点，直接按 code 删除。

DELETE FROM iam_menu
WHERE channel = 'admin' AND code = 'system.profile';
