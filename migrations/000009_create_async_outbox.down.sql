-- 000009_create_async_outbox.down.sql
-- 回滚 ENT-03 持久化底座：删除三张异步表。
-- 前置：确认无 Relay 运行且已归档死信；本迁移不恢复 iam_outbox（见 000010 down）。

DROP TABLE IF EXISTS async_consumed;
DROP TABLE IF EXISTS async_dlq;
DROP TABLE IF EXISTS async_outbox;
