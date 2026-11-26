-- 000033 (down): 回滚配置中心 M2 业务密钥表族。

DROP TABLE IF EXISTS config_secret_review;
DROP TABLE IF EXISTS config_secret_grant;
DROP TABLE IF EXISTS config_secret_version;
DROP TABLE IF EXISTS config_secret;
