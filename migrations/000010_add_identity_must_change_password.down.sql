-- 回滚强制改密标记列。
ALTER TABLE identity_account
    DROP COLUMN must_change_password;
