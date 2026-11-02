-- 回滚 ENT-04 T1：audit_event 还原为 identity_audit_event。
--
-- 先删除新增索引再加回原名，避免索引名与表名语义不一致。

ALTER TABLE audit_event
    DROP KEY idx_audit_channel,
    DROP KEY idx_audit_action;

RENAME TABLE audit_event TO identity_audit_event;
