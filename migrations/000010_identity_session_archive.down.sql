-- 回滚 000010：删除归档表。
--
-- 注意：归档数据将随表删除而丢失；如需保留历史，请先导出归档表。
-- 在线表与业务行为不受影响（清理任务在归档表缺失时会因 SQL 错误告警）。

DROP TABLE IF EXISTS identity_session_family_archive;
DROP TABLE IF EXISTS identity_session_archive;
