-- 管理员重置密码后的强制改密标记（IDN-P0-02）。
-- 为 0 时登录行为不变；为 1 时登录只返回 password_change_ticket，
-- 用户完成改密前不签发正式令牌。

ALTER TABLE identity_account
    ADD COLUMN must_change_password TINYINT NOT NULL DEFAULT 0
        COMMENT '强制改密标记：1=管理员重置密码后尚未改密'
        AFTER locked_until;
