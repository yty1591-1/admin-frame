-- 身份域（identity）模块基线：账号、凭据、会话、设备会话、MFA、登录尝试与外部身份映射。
-- 本文件由增量迁移按领域合并而成（原 000001 / 000010 / 000011 / 000012 / 000013）：
-- 强制改密标记、设备会话表、MFA 防重放列与摘要列宽已直接体现在基线结构中。
-- 仅由 cmd/migrate 或部署 Job 执行；API / worker 启动不做迁移（DATA_STANDARDS）。
-- 设计要点：
--   1. 主键统一雪花 ID（pkg/id），禁止自增；
--   2. 时间列 DATETIME(3) 且由应用写入 UTC；
--   3. 字符集 / 排序规则与现有表一致（utf8mb4 / utf8mb4_0900_ai_ci）；
--   4. 外键仅用于阻止悬空凭据 / 会话（RESTRICT），业务删除仍以软删为主；
--   5. 登录尝试不建外键：账号不存在时也要能记录，避免用外键错误泄露账号是否存在；
--   6. 设备（family）与令牌代（session）分离：family 承载列表 / 撤销 / 设备上限，
--      session 承载每一代刷新令牌与重放检测；
--   7. MFA 记录已消费的 TOTP 时间步与乐观锁版本，防止重放与并发覆盖；
--   8. 摘要统一为 `<key_id>:<hmac>` 格式，摘要列宽 128。

CREATE TABLE IF NOT EXISTS identity_account (
    id                  BIGINT       NOT NULL                 COMMENT '账号主键，雪花 ID',
    channel             VARCHAR(16)  NOT NULL                 COMMENT '端别：admin=A端 / biz=B端 / c=C端',
    subject             VARCHAR(255) NOT NULL                 COMMENT '规范化登录主体（trim + 小写）；同端别唯一，软删后不可复用',
    status              VARCHAR(16)  NOT NULL DEFAULT 'pending' COMMENT '状态：pending=待激活 / active=可用 / locked=锁定 / disabled=停用',
    token_version       BIGINT       NOT NULL DEFAULT 1       COMMENT '账号级撤销版本，只增不减；签发令牌写入 ver',
    version             BIGINT       NOT NULL DEFAULT 1       COMMENT '乐观锁版本；更新须 WHERE version=?，冲突返回 40900',
    merchant_id         VARCHAR(64)  NOT NULL DEFAULT ''      COMMENT 'B端商户ID；admin / c 端为空串，禁止用空串代表全部商户',
    password_updated_at DATETIME(3)  NULL                     COMMENT '最近一次改密时间（UTC）；NULL=从未设置密码',
    locked_until        DATETIME(3)  NULL                     COMMENT '锁定到期时间（UTC）；仅 status=locked 时有值',
    must_change_password TINYINT     NOT NULL DEFAULT 0       COMMENT '强制改密标记：1=管理员重置密码后尚未改密',
    created_at          DATETIME(3)  NOT NULL                 COMMENT '创建时间（UTC）',
    updated_at          DATETIME(3)  NOT NULL                 COMMENT '最后更新时间（UTC）',
    deleted_at          DATETIME(3)  NULL                     COMMENT '软删时间（UTC）；NULL=未删除',
    PRIMARY KEY (id),
    UNIQUE KEY uk_identity_account_subject (channel, subject)
        COMMENT '端内登录主体唯一；软删后仍占用，防止身份复用',
    KEY idx_identity_account_channel_status (channel, status)
        COMMENT '按端别 + 状态列账号（停用 / 锁定盘点）'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='身份域账号：状态机与账号级撤销版本；不承载角色与权限';

CREATE TABLE IF NOT EXISTS identity_credential (
    id                BIGINT       NOT NULL                 COMMENT '凭据主键，雪花 ID',
    account_id        BIGINT       NOT NULL                 COMMENT '账号ID，关联 identity_account.id',
    credential_type   VARCHAR(32)  NOT NULL                 COMMENT '凭据类型：password=密码 / phone=手机号绑定',
    credential_hash   VARCHAR(255) NOT NULL                 COMMENT '编码后哈希或不可逆摘要；禁止存明文',
    algorithm         VARCHAR(32)  NOT NULL                 COMMENT '算法标识：argon2id / bcrypt / hmac-sha256',
    created_at        DATETIME(3)  NOT NULL                 COMMENT '创建时间（UTC）',
    updated_at        DATETIME(3)  NOT NULL                 COMMENT '最后更新时间（UTC）',
    PRIMARY KEY (id),
    UNIQUE KEY uk_identity_credential_type (account_id, credential_type)
        COMMENT '同一账号同一类型仅一条有效凭据',
    CONSTRAINT fk_identity_credential_account FOREIGN KEY (account_id) REFERENCES identity_account (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='账号凭据：只存哈希，支持登录成功后透明重哈希';

CREATE TABLE IF NOT EXISTS identity_session (
    id                 BIGINT       NOT NULL                 COMMENT '会话主键，雪花 ID',
    account_id         BIGINT       NOT NULL                 COMMENT '账号ID，关联 identity_account.id',
    family_id          BIGINT       NOT NULL                 COMMENT '会话链家庭ID；重放时按此整链撤销，取首次登录的会话ID',
    refresh_token_hash CHAR(64)     NOT NULL                 COMMENT '刷新令牌 SHA-256 十六进制摘要，全局唯一；禁止存原文',
    status             VARCHAR(16)  NOT NULL DEFAULT 'active' COMMENT '状态：active / rotated / revoked / expired',
    token_version      BIGINT       NOT NULL                 COMMENT '签发时的账号撤销版本，用于排查跨版本会话',
    version            BIGINT       NOT NULL DEFAULT 1       COMMENT '乐观锁版本；并发刷新只有一个请求能改为 rotated',
    expires_at         DATETIME(3)  NOT NULL                 COMMENT '绝对过期时间（UTC）',
    idle_expires_at    DATETIME(3)  NULL                     COMMENT '空闲上限（UTC）；NULL=不限制',
    last_used_at       DATETIME(3)  NOT NULL                 COMMENT '最近使用时间（UTC）',
    revoked_at         DATETIME(3)  NULL                     COMMENT '撤销时间（UTC）；NULL=未撤销',
    revoked_reason     VARCHAR(32)  NOT NULL DEFAULT ''      COMMENT '撤销原因：logout / logout_all / replay / admin / account_disabled / password_changed',
    created_at         DATETIME(3)  NOT NULL                 COMMENT '创建时间（UTC）',
    updated_at         DATETIME(3)  NOT NULL                 COMMENT '最后更新时间（UTC）',
    PRIMARY KEY (id),
    UNIQUE KEY uk_identity_session_refresh (refresh_token_hash)
        COMMENT '刷新令牌摘要全局唯一，一次性使用',
    KEY idx_identity_session_account_status (account_id, status)
        COMMENT '会话管理：按账号列出有效会话',
    KEY idx_identity_session_family (family_id)
        COMMENT '重放检测：整链撤销',
    KEY idx_identity_session_family_status (family_id, status)
        COMMENT '按设备查令牌代与清理扫描',
    KEY idx_identity_session_account_last_used (account_id, last_used_at)
        COMMENT '会话管理：按账号 + 最近使用排序',
    KEY idx_identity_session_expires (expires_at)
        COMMENT '过期清理任务扫描',
    CONSTRAINT fk_identity_session_account FOREIGN KEY (account_id) REFERENCES identity_account (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='刷新会话链：一次性轮换；rotated 再次出现视为重放';

CREATE TABLE IF NOT EXISTS identity_session_family (
    family_id           BIGINT       NOT NULL                 COMMENT '设备会话主键，取首次登录的会话 ID',
    account_id          BIGINT       NOT NULL                 COMMENT '账号ID，关联 identity_account.id',
    status              VARCHAR(16)  NOT NULL DEFAULT 'active' COMMENT '状态：active / revoked / expired',
    version             BIGINT       NOT NULL DEFAULT 1       COMMENT '乐观锁版本；刷新与撤销时递增',
    created_at          DATETIME(3)  NOT NULL                 COMMENT '创建时间（UTC）',
    last_used_at        DATETIME(3)  NOT NULL                 COMMENT '最近一次刷新时间（UTC）',
    expires_at          DATETIME(3)  NOT NULL                 COMMENT '当前令牌代过期时间（UTC）',
    absolute_expires_at DATETIME(3)  NOT NULL                 COMMENT '设备绝对到期时间（UTC）；刷新不得延长',
    ip_hash             VARCHAR(128) NOT NULL DEFAULT ''      COMMENT '客户端 IP 带密钥摘要；禁止存原文',
    ua_hash             VARCHAR(128) NOT NULL DEFAULT ''      COMMENT 'User-Agent 带密钥摘要；禁止存原文',
    device_label        VARCHAR(64)  NOT NULL DEFAULT ''      COMMENT '可选设备标签（浏览器 / 终端名）',
    revoked_at          DATETIME(3)  NULL                     COMMENT '撤销时间（UTC）；NULL=未撤销',
    revoked_reason      VARCHAR(32)  NOT NULL DEFAULT ''      COMMENT '撤销原因：logout / replay / admin / evicted 等',
    updated_at          DATETIME(3)  NOT NULL                 COMMENT '最后更新时间（UTC）',
    PRIMARY KEY (family_id),
    KEY idx_identity_session_family_account (account_id, status, last_used_at)
        COMMENT '会话管理：按账号列出设备并按最近使用排序',
    KEY idx_identity_session_family_expires (expires_at)
        COMMENT '过期清理任务扫描',
    CONSTRAINT fk_identity_session_family_account FOREIGN KEY (account_id) REFERENCES identity_account (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='设备会话：一次登录一台设备；刷新只轮换 identity_session 令牌代';

CREATE TABLE IF NOT EXISTS identity_mfa (
    account_id           BIGINT       NOT NULL              COMMENT '账号ID，主键即账号，关联 identity_account.id',
    secret_ciphertext    VARBINARY(256) NOT NULL             COMMENT 'TOTP 密钥 AES-GCM 密文；禁止明文',
    confirmed_at         DATETIME(3)  NULL                  COMMENT '首次动态码校验通过时间（UTC）；NULL=未确认',
    last_used_step       BIGINT       NOT NULL DEFAULT 0    COMMENT '最近一次成功消费的 TOTP 时间步；同一时间步不得重放',
    version              BIGINT       NOT NULL DEFAULT 1    COMMENT '乐观锁版本；确认、消费时间步与密钥重加密时递增',
    recovery_code_hashes JSON         NOT NULL              COMMENT '恢复码哈希数组；一次性使用，禁止明文',
    created_at           DATETIME(3)  NOT NULL              COMMENT '创建时间（UTC）',
    updated_at           DATETIME(3)  NOT NULL              COMMENT '最后更新时间（UTC）',
    PRIMARY KEY (account_id),
    CONSTRAINT fk_identity_mfa_account FOREIGN KEY (account_id) REFERENCES identity_account (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='A端 TOTP 绑定：未确认不得用于登录；密钥加密存储';

CREATE TABLE IF NOT EXISTS identity_login_attempt (
    id          BIGINT       NOT NULL                 COMMENT '登录尝试主键，雪花 ID',
    channel     VARCHAR(16)  NOT NULL                 COMMENT '端别：admin / biz / c',
    account_ref VARCHAR(128) NOT NULL                 COMMENT '账号主体带密钥摘要（key_id:hmac）；禁止明文手机号 / 邮箱',
    outcome     VARCHAR(16)  NOT NULL                 COMMENT '结果：success / failure / denied',
    reason      VARCHAR(64)  NOT NULL DEFAULT ''      COMMENT '原因：bad_credential / locked / disabled / mfa_pending / mfa_failed 等',
    ip_hash     VARCHAR(128) NOT NULL DEFAULT ''      COMMENT '客户端 IP 带密钥摘要；不可获得时为空',
    ua_hash     VARCHAR(128) NOT NULL DEFAULT ''      COMMENT 'User-Agent 带密钥摘要；不可获得时为空',
    occurred_at DATETIME(3)  NOT NULL                 COMMENT '发生时间（UTC）',
    PRIMARY KEY (id),
    KEY idx_identity_login_attempt_ref (channel, account_ref, occurred_at)
        COMMENT '失败计数：窗口内按账号摘要统计',
    KEY idx_identity_login_attempt_time (occurred_at)
        COMMENT '保留期清理任务扫描（默认 180 天）'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='登录尝试：只追加；失败阈值与审计关联；不含明文 PII';

CREATE TABLE IF NOT EXISTS identity_oauth_identity (
    id               BIGINT       NOT NULL              COMMENT '外部身份映射主键，雪花 ID',
    account_id       BIGINT       NOT NULL              COMMENT '账号ID，关联 identity_account.id',
    provider         VARCHAR(32)  NOT NULL              COMMENT '外部身份提供方标识，如 oidc-corp',
    provider_subject VARCHAR(255) NOT NULL              COMMENT '提供方侧主体标识（sub）',
    created_at       DATETIME(3)  NOT NULL              COMMENT '创建时间（UTC）',
    updated_at       DATETIME(3)  NOT NULL              COMMENT '最后更新时间（UTC）',
    PRIMARY KEY (id),
    UNIQUE KEY uk_identity_oauth_subject (provider, provider_subject)
        COMMENT '同一提供方主体只能映射一个账号',
    KEY idx_identity_oauth_account (account_id)
        COMMENT '按账号反查外部身份',
    CONSTRAINT fk_identity_oauth_account FOREIGN KEY (account_id) REFERENCES identity_account (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='外部身份映射：OIDC / OAuth 账号关联；阶段 4 启用';
