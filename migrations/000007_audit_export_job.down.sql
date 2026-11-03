-- 回滚 ENT-04 T5：删除审计异步导出任务表。
--
-- 对象存储中的历史导出文件不由本脚本删除：迁移无法撤销件，
-- 需要时由运维按 audit/exports/ 前缀手工清理。

DROP TABLE IF EXISTS audit_export_job;
