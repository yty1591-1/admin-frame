-- 回滚 orders 表。
-- MySQL 不支持 DROP INDEX IF EXISTS（原写法只在 MariaDB 可用，MySQL 会语法错误）；
-- 索引随表一起删除，因此无需单独 DROP INDEX。
-- 若将来需要只删索引而保留表，必须使用 ALTER TABLE orders DROP INDEX <index_name>。

DROP TABLE IF EXISTS orders;
