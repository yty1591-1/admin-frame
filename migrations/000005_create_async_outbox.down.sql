-- 回滚平台异步模块：删除三张异步表。
-- 前置：确认无 Relay 运行且已归档死信。IAM 审计事件同表存放，回滚会一并删除。

DROP TABLE IF EXISTS async_consumed;
DROP TABLE IF EXISTS async_dlq;
DROP TABLE IF EXISTS async_outbox;
