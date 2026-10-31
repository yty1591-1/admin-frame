-- 回滚多租户唯一键；跨商户同名主体会导致回滚失败，需先合并账号。
ALTER TABLE identity_account
    DROP INDEX uk_identity_account_subject,
    ADD UNIQUE KEY uk_identity_account_subject (channel, subject)
        COMMENT '端内登录主体唯一；软删后仍占用，防止身份复用';
