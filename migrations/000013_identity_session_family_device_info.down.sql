-- 回滚设备会话画像列；摘要列（ip_hash / ua_hash）不受影响。

ALTER TABLE identity_session_family
    DROP COLUMN ip_masked,
    DROP COLUMN browser_name,
    DROP COLUMN os_name,
    DROP COLUMN device_type;
