-- 回滚清理索引；索引无业务数据，可直接删除。
ALTER TABLE identity_session
    DROP INDEX idx_identity_session_status_updated,
    DROP INDEX idx_identity_session_updated;

ALTER TABLE identity_session_family
    DROP INDEX idx_identity_session_family_status_updated,
    DROP INDEX idx_identity_session_family_updated;
