-- 身份域清理性能基线：为会话/设备的保留期删除增加 updated_at 索引。
--
-- 背景（IDN-GAP-P0-04）：
--   清理任务按 `status IN (...) AND updated_at < ? ORDER BY updated_at LIMIT ?` 删除，
--   原基线只有 expires_at 索引，删除语句会随表增长退化为全表扫描。
-- 设计：
--   1. 单列 updated_at 索引支撑跨状态排序与范围扫描；
--   2. (status, updated_at) 复合索引支撑按状态过滤后的范围扫描；
--   3. 不改变任何业务语义，仅新增索引，可在线回滚。

ALTER TABLE identity_session
    ADD INDEX idx_identity_session_updated (updated_at),
    ADD INDEX idx_identity_session_status_updated (status, updated_at);

ALTER TABLE identity_session_family
    ADD INDEX idx_identity_session_family_updated (updated_at),
    ADD INDEX idx_identity_session_family_status_updated (status, updated_at);
