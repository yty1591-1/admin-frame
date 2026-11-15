-- 000034 (down): 移除配置中心密钥管理菜单。

DELETE FROM iam_menu
WHERE channel = 'admin' AND code = 'system.config.secrets';
