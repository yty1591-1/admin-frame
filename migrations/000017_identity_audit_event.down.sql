-- 回滚独立审计存储：删除审计表。
--
-- 警告：审计数据不可恢复；回滚前必须先导出并确认保留期合规要求。
-- 前置：已切换回日志 / SIEM 出口，避免应用继续写入失败。

DROP TABLE IF EXISTS identity_audit_event;
