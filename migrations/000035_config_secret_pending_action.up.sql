-- 000035 (up)：密钥待审变更字段（M2 收口 / AC-CC-15）。
--
-- pending_action 非空时密钥保持 active，解析不受影响；审批通过后再生效。

ALTER TABLE config_secret
    ADD COLUMN pending_action  VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '待审动作：rotate/revoke/grant/空' AFTER submitted_by,
    ADD COLUMN pending_payload VARCHAR(512) NOT NULL DEFAULT '' COMMENT '待审附属数据（如 grant service_identity）' AFTER pending_action;
