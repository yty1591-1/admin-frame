-- 菜单进入运营 CRUD 后需要乐观锁：更新与停用必须携带 version，
-- 避免两个运营并发编辑同一条菜单时后写覆盖先写。
-- version 由应用层在事务内校验并自增，不依赖数据库触发器。

ALTER TABLE iam_menu
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1 COMMENT '乐观锁版本；每次写入自增' AFTER status;
