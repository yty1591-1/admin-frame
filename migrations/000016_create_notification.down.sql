-- 000016 (down): 回滚通知中心五张表（ENT-10）。
--
-- 顺序无所谓（互相无外键）；投递明细先删只是排版习惯——
-- 它是唯一可能被外部系统（回执回调）并发写入的表。

DROP TABLE IF EXISTS notification_delivery;
DROP TABLE IF EXISTS notification_blacklist;
DROP TABLE IF EXISTS notification_preference;
DROP TABLE IF EXISTS notification_template;
DROP TABLE IF EXISTS notification_request;
