-- 回滚菜单乐观锁列。已产生的 version 仅用于并发控制，删除不影响业务数据。

ALTER TABLE iam_menu DROP COLUMN version;
