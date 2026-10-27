-- 按 (channel, code) 逆序删除菜单种子：先子后父，避免自引用外键阻塞。
DELETE FROM iam_menu WHERE channel = 'biz' AND code IN ('member.list', 'member');
DELETE FROM iam_menu WHERE channel = 'admin' AND code IN ('system.role', 'system');
