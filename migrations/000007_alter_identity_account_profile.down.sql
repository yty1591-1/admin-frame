-- 回滚账号展示昵称列；昵称数据不可恢复，回滚前须确认可接受。
ALTER TABLE identity_account
    DROP COLUMN display_name;
