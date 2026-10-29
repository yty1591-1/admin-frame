-- 摘要格式升级为 `<key_id>:<hmac>`（IDN-P1-06），长度最多 16 + 1 + 64 字节。
ALTER TABLE identity_login_attempt
    MODIFY COLUMN account_ref VARCHAR(128) NOT NULL
        COMMENT '账号主体带密钥摘要（key_id:hmac）；禁止明文手机号 / 邮箱',
    MODIFY COLUMN ip_hash VARCHAR(128) NOT NULL DEFAULT ''
        COMMENT '客户端 IP 带密钥摘要；不可获得时为空',
    MODIFY COLUMN ua_hash VARCHAR(128) NOT NULL DEFAULT ''
        COMMENT 'User-Agent 带密钥摘要；不可获得时为空';

ALTER TABLE identity_session_family
    MODIFY COLUMN ip_hash VARCHAR(128) NOT NULL DEFAULT ''
        COMMENT '客户端 IP 带密钥摘要；禁止存原文',
    MODIFY COLUMN ua_hash VARCHAR(128) NOT NULL DEFAULT ''
        COMMENT 'User-Agent 带密钥摘要；禁止存原文';
