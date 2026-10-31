-- PII 加密 S1：为账号登录主体新增确定性索引与密文列（IDN-GAP-P1-06）。
--
-- 背景：
--   identity_account.subject 为明文 PII，唯一键直接依赖明文，库内可直接读取。
-- 阶段（本迁移只做 S1，不删除明文、不切换唯一键）：
--   S1 新增可空列与非唯一索引；应用双写（明文 + index + ciphertext）。
--   S2 读路径优先按 index 查找，未命中回退明文。
--   S3 回填存量数据（cmd/migrate -action backfill-subject）。
--   S4/S5（唯一键切换到 subject_index、删除明文列）需单独发布，禁止与本迁移合并上线。
-- 设计：
--   subject_index = HMAC-SHA256(identity.hash_key, "account-subject|channel|merchant|subject")
--   subject_ciphertext = AES-256-GCM(subject)，AAD = "identity:account:<account_id>"
--   两者可空，保证滚动升级与回滚期间旧行不受影响。
-- 回滚：
--   删除列与索引即可回到纯明文路径；回滚前请确认应用已停止双写。

ALTER TABLE identity_account
    ADD COLUMN subject_index VARCHAR(128) NULL
        COMMENT 'PII 确定性索引：HMAC-SHA256(account-subject|channel|merchant|subject)；S4 起作为唯一键',
    ADD COLUMN subject_ciphertext VARBINARY(512) NULL
        COMMENT 'PII 密文：AES-256-GCM(subject)，AAD=identity:account:<account_id>',
    ADD INDEX idx_identity_account_subject_index (channel, merchant_id, subject_index)
        COMMENT '按端别 + 商户 + PII 索引查找账号，避免明文扫描';
