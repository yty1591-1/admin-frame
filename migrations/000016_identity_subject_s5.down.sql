-- 回滚 S5：重新加入可空明文列，保护列恢复可空。
--
-- 重要：被删除的明文数据无法由本脚本恢复；如需完整回滚，
-- 必须先还原迁移前数据库快照，或在回滚后的双写版本重新回填。

ALTER TABLE identity_account
    ADD COLUMN subject VARCHAR(255) NULL
        COMMENT '回滚占位列；数据需从迁移前快照恢复',
    MODIFY subject_index VARCHAR(128) NULL
        COMMENT 'PII 确定性索引；回滚窗口允许 NULL',
    MODIFY subject_ciphertext VARBINARY(512) NULL
        COMMENT 'PII 密文；回滚窗口允许 NULL';

ALTER TABLE identity_oauth_identity
    ADD COLUMN provider_subject VARCHAR(255) NULL
        COMMENT '回滚占位列；数据需从迁移前快照恢复',
    MODIFY provider_subject_index VARCHAR(128) NULL
        COMMENT '外部身份主体确定性索引；回滚窗口允许 NULL',
    MODIFY provider_subject_ciphertext VARBINARY(512) NULL
        COMMENT '外部身份主体密文；回滚窗口允许 NULL';
