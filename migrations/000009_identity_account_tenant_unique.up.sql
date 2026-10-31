-- 多租户账号唯一键：B 端以 merchant_id 隔离登录主体。
--
-- 背景（IDN-GAP-P1-10）：
--   原唯一键 (channel, subject) 是端别内全局唯一，导致不同商户无法使用相同
--   登录主体（如相同员工邮箱），且登录查询不带租户上下文。
-- 变更：
--   唯一键改为 (channel, merchant_id, subject)。
--   A / C 端 merchant_id 恒为空串，语义不变；B 端按商户隔离。
-- 兼容：
--   旧数据均满足新唯一键（旧约束更严格），无需回填。
-- 回滚：
--   若存在跨商户同名主体，回滚会因唯一冲突失败，需先人工合并。

ALTER TABLE identity_account
    DROP INDEX uk_identity_account_subject,
    ADD UNIQUE KEY uk_identity_account_subject (channel, merchant_id, subject)
        COMMENT '租户内登录主体唯一；软删后仍占用，防止身份复用';
