-- 回滚 000011 S1：删除 PII 索引与密文列。
--
-- 前置条件：应用已停止双写（回滚配置并重启），否则再次上线会因列缺失写入失败。
-- 明文 subject 未被修改，回滚后登录与列表行为立即恢复。

ALTER TABLE identity_account
    DROP INDEX idx_identity_account_subject_index,
    DROP COLUMN subject_ciphertext,
    DROP COLUMN subject_index;
