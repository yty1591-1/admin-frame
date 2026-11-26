-- M2M 机器身份（OAuth2 client_credentials）基线表（docs/identity-authn.md §21.2.7）。
--
-- 设计要点：
--   1. 客户端与密钥分表：轮换只新增 secret 行，不覆盖历史指纹，迭代可审计；
--   2. token_version 表达撤销语义：停用 / 启用 / 轮换 / 缩小 scope 均递增，
--      校验侧比较令牌 ver 与快照（authn:client_ver:<client_id>），旧令牌 ≤60s 失效；
--   3. allowed_channels / allowed_scopes 用 JSON 数组保存，scope 精确匹配、禁止通配符；
--   4. client_id 软删后仍占用唯一键，禁止复用（避免历史审计指向新主体）；
--   5. 只做加法：不修改既有身份表；两张表保留 ≥180 天，与审计留存对齐。
CREATE TABLE IF NOT EXISTS identity_m2m_client (
    id                BIGINT        NOT NULL                  COMMENT '主键，雪花 ID（pkg/id 生成），禁止自增',
    client_id         VARCHAR(64)   NOT NULL                  COMMENT '对外客户端标识：svc_<16 字节 base64url>；软删后不可复用',
    name              VARCHAR(128)  NOT NULL                  COMMENT '展示名，仅用于列表与审计',
    status            VARCHAR(16)   NOT NULL DEFAULT 'active' COMMENT '生命周期：active=可签发 / disabled=已停用 / deleted=已软删',
    allowed_channels  JSON          NOT NULL                  COMMENT '允许取令牌的端别数组，元素取值 admin/biz/c，非空',
    allowed_scopes    JSON          NOT NULL                  COMMENT '权限码白名单数组；精确匹配，禁止通配符 *',
    token_version     BIGINT        NOT NULL DEFAULT 1        COMMENT '撤销版本；签发写入令牌 ver，递增后旧令牌失效',
    owner             VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT '归属团队 / 系统，便于轮换责任到人',
    created_by        VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT '创建人主体 ID（A 端账号）',
    rotation_due_at   DATETIME(3)   NULL                      COMMENT '最近一次 secret 轮换时间（UTC）；超 max_secret_age 仅告警',
    created_at        DATETIME(3)   NOT NULL                  COMMENT '创建时间（UTC，毫秒精度）',
    updated_at        DATETIME(3)   NOT NULL                  COMMENT '更新时间（UTC，毫秒精度）',
    deleted_at        DATETIME(3)   NULL                      COMMENT '软删时间（UTC）；非空表示不可用',
    version           BIGINT        NOT NULL DEFAULT 1        COMMENT '乐观锁版本；更新须 WHERE version=?，冲突返回 40900',
    PRIMARY KEY (id),
    UNIQUE KEY uk_identity_m2m_client_client_id (client_id)
        COMMENT 'client_id 全局唯一且软删后不可复用',
    KEY idx_identity_m2m_client_status (status, created_at)
        COMMENT '列表：按状态 + 创建时间扫描',
    CONSTRAINT ck_identity_m2m_client_status CHECK (status IN ('active', 'disabled', 'deleted')),
    CONSTRAINT ck_identity_m2m_client_version CHECK (version >= 1),
    CONSTRAINT ck_identity_m2m_client_token_version CHECK (token_version >= 1),
    CONSTRAINT ck_identity_m2m_client_channels CHECK (JSON_TYPE(allowed_channels) = 'ARRAY'),
    CONSTRAINT ck_identity_m2m_client_scopes CHECK (JSON_TYPE(allowed_scopes) = 'ARRAY')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='M2M 机器客户端：服务身份、端别与 scope 白名单';

CREATE TABLE IF NOT EXISTS identity_m2m_secret (
    id           BIGINT       NOT NULL                  COMMENT '主键，雪花 ID',
    client_id    VARCHAR(64)  NOT NULL                  COMMENT '所属客户端对外标识；客户端只软删，故不建外键（软删后仍需保留指纹）',
    secret_hash  CHAR(64)     NOT NULL                  COMMENT 'client_secret 的 SHA-256 十六进制摘要；禁止存明文',
    fingerprint  VARCHAR(32)  NOT NULL                  COMMENT '展示用指纹（sha256:xxxx），不可反推明文',
    status       VARCHAR(16)  NOT NULL DEFAULT 'current' COMMENT 'current=生效 / retiring=轮换宽限 / revoked=已吊销',
    created_at   DATETIME(3)  NOT NULL                  COMMENT '创建时间（UTC）',
    expires_at   DATETIME(3)  NULL                      COMMENT 'retiring 宽限到期时间（UTC）；NULL=无宽限上限',
    revoked_at   DATETIME(3)  NULL                      COMMENT '吊销时间（UTC）；非空表示立即不可用',
    PRIMARY KEY (id),
    UNIQUE KEY uk_identity_m2m_secret_hash (secret_hash)
        COMMENT '摘要全局唯一，避免同一 secret 被两个客户端持有',
    KEY idx_identity_m2m_secret_client (client_id, status)
        COMMENT '轮换：按客户端 + 状态统计有效密钥数（current + retiring ≤ 2）',
    CONSTRAINT ck_identity_m2m_secret_status CHECK (status IN ('current', 'retiring', 'revoked'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='M2M 客户端密钥：只存摘要与指纹，支持 current + retiring 双密钥轮换';
