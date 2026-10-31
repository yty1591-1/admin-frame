-- 回滚 S4：恢复明文唯一键，删除外部身份保护列。
--
-- 前置：已回滚 000016（重新加入可空明文列）；否则明文唯一键无法建立。
-- 注意：明文列数据不随本迁移恢复，需从迁移前快照还原后重新回填。

ALTER TABLE identity_oauth_identity
    DROP INDEX uk_identity_oauth_subject_index,
    DROP INDEX idx_identity_oauth_provider_subject_index,
    DROP COLUMN provider_subject_ciphertext,
    DROP COLUMN provider_subject_index,
    ADD UNIQUE KEY uk_identity_oauth_subject (provider, provider_subject)
        COMMENT '同一提供方主体只能映射一个账号';

ALTER TABLE identity_account
    DROP INDEX uk_identity_account_subject_index,
    ADD UNIQUE KEY uk_identity_account_subject (channel, merchant_id, subject)
        COMMENT '租户内登录主体唯一；软删后仍占用，防止身份复用',
    ADD INDEX idx_identity_account_subject_index (channel, merchant_id, subject_index)
        COMMENT '按端别 + 商户 + PII 索引查找账号，避免明文扫描';
