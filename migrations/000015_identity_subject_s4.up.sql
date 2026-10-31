-- PII 加密 S4：唯一键切换到确定性索引；外部身份新增主体保护列（IDN-GAP-SEC-01）。
--
-- 前置：应用已发布双写版本，并完成 cmd/identity-maintenance backfill-subject
--       （account subject_index/ciphertext 已回填；本迁移后还需回填 oauth 主体）。
-- 说明：
--   - 账号唯一键由 (channel, merchant_id, subject) 切换为
--     (channel, merchant_id, subject_index)，不再依赖明文；
--   - 外部身份新增 provider_subject_index / provider_subject_ciphertext 可空列，
--     唯一键切换为 (provider, provider_subject_index)；
--   - 新列可空以兼容回填窗口，唯一键允许并存的 NULL 行；
--   - 明文列在本迁移中保留，由 000016（S5）删除。
-- 回滚：恢复明文唯一键并删除外部身份保护列；需先回滚 000016。

ALTER TABLE identity_account
    DROP INDEX idx_identity_account_subject_index,
    DROP INDEX uk_identity_account_subject,
    ADD UNIQUE KEY uk_identity_account_subject_index (channel, merchant_id, subject_index)
        COMMENT '租户内登录主体确定性索引唯一；软删后仍占用，防止身份复用';

ALTER TABLE identity_oauth_identity
    ADD COLUMN provider_subject_index VARCHAR(128) NULL
        COMMENT '外部身份主体确定性索引：HMAC-SHA256(oauth-provider-subject|provider|subject)',
    ADD COLUMN provider_subject_ciphertext VARBINARY(512) NULL
        COMMENT '外部身份主体密文：AES-256-GCM(sub)，AAD=identity:oauth:<identity_id>',
    ADD INDEX idx_identity_oauth_provider_subject_index (provider, provider_subject_index)
        COMMENT '按提供方 + 主体索引查找映射',
    DROP INDEX uk_identity_oauth_subject,
    ADD UNIQUE KEY uk_identity_oauth_subject_index (provider, provider_subject_index)
        COMMENT '同一提供方主体只能映射一个账号（按确定性索引）';
