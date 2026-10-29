-- 回滚摘要列宽（IDN-P1-06）；若已写入带 key_id 的摘要，需先回填为旧格式。
ALTER TABLE identity_login_attempt
    MODIFY COLUMN account_ref CHAR(64) NOT NULL
        COMMENT '账号主体不可逆摘要（含端别前缀）；禁止明文手机号 / 邮箱',
    MODIFY COLUMN ip_hash VARCHAR(64) NOT NULL DEFAULT ''
        COMMENT '客户端 IP 摘要；不可获得时为空',
    MODIFY COLUMN ua_hash VARCHAR(64) NOT NULL DEFAULT ''
        COMMENT 'User-Agent 摘要；不可获得时为空';

ALTER TABLE identity_session_family
    MODIFY COLUMN ip_hash VARCHAR(64) NOT NULL DEFAULT ''
        COMMENT '客户端 IP 摘要；禁止存原文',
    MODIFY COLUMN ua_hash VARCHAR(64) NOT NULL DEFAULT ''
        COMMENT 'User-Agent 摘要；禁止存原文';
