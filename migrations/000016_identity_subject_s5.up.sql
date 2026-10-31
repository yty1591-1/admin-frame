-- PII 加密 S5：删除明文登录主体列（IDN-GAP-SEC-01）。
--
-- 强制前置：
--   1. 000015 已执行；
--   2. cmd/identity-maintenance backfill-subject 已把 account 与 oauth 主体
--      index/ciphertext 全部回填；
--   3. 应用已发布只读密文版本。
-- 本迁移通过 MODIFY ... NOT NULL 作为回填完整性门禁：存在 NULL 时直接失败，
-- 不允许带着未回填数据删除明文列。
-- 回滚：结构可回滚（重新加入可空明文列），但明文数据不可逆恢复；
--       需从迁移前数据库快照还原，或在回滚后的双写版本重新回填。

ALTER TABLE identity_account
    MODIFY subject_index VARCHAR(128) NOT NULL
        COMMENT 'PII 确定性索引：HMAC-SHA256(account-subject|channel|merchant|subject)；唯一键',
    MODIFY subject_ciphertext VARBINARY(512) NOT NULL
        COMMENT 'PII 密文：AES-256-GCM(subject)，AAD=identity:account:<account_id>',
    DROP COLUMN subject;

ALTER TABLE identity_oauth_identity
    MODIFY provider_subject_index VARCHAR(128) NOT NULL
        COMMENT '外部身份主体确定性索引：HMAC-SHA256(oauth-provider-subject|provider|subject)',
    MODIFY provider_subject_ciphertext VARBINARY(512) NOT NULL
        COMMENT '外部身份主体密文：AES-256-GCM(sub)，AAD=identity:oauth:<identity_id>',
    DROP COLUMN provider_subject;
