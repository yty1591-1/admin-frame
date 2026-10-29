-- 回滚设备会话拆分（IDN-P1-01）：先删索引再删表，避免依赖残留。
ALTER TABLE identity_session
    DROP KEY idx_identity_session_account_last_used,
    DROP KEY idx_identity_session_family_status;

DROP TABLE IF EXISTS identity_session_family;
