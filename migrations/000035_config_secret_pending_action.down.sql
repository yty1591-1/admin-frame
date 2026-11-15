-- 000035 (down)：回滚待审变更字段。

ALTER TABLE config_secret
    DROP COLUMN pending_payload,
    DROP COLUMN pending_action;
