-- 000033 (up): 配置中心 M2 业务密钥表族（config-center-plan §附录 A / ADR-0014）。
--
-- 设计要点：
--   1. 明文永不入库；仅 ciphertext + nonce + wrapped_dek + kek_key_id + value_hash。
--   2. 版本只追加；active / grace 由 status 与 config_secret.active_version 表达。
--   3. grant 按 env+namespace+service_identity+secret_id 四元组，禁止通配符。
--   4. 主键雪花 ID；时间 DATETIME(3) UTC。

CREATE TABLE IF NOT EXISTS config_secret (
    id                   BIGINT       NOT NULL COMMENT '主键：雪花 ID',
    env                  VARCHAR(16)  NOT NULL COMMENT '环境枚举：local/dev/test/staging/prod',
    namespace            VARCHAR(64)  NOT NULL COMMENT '命名空间',
    name                 VARCHAR(128) NOT NULL COMMENT '小写点分密钥名；不含明文语义',
    description          VARCHAR(512) NOT NULL DEFAULT '' COMMENT '用途说明',
    owner                VARCHAR(64)  NOT NULL COMMENT '负责人或团队',
    status               VARCHAR(16)  NOT NULL DEFAULT 'draft' COMMENT 'draft/pending_review/active/rejected/revoked/destroyed',
    active_version       BIGINT       NULL COMMENT '当前 active 版本号；NULL=尚无激活版本',
    version              BIGINT       NOT NULL DEFAULT 1 COMMENT '元数据乐观锁',
    rotate_interval_days INT          NOT NULL DEFAULT 90 COMMENT '建议轮换间隔（天）',
    dual_version         TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '1=解析同时返回 active 与 grace',
    submitted_by         VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '最近一次送审主体；审批人不得相同',
    expires_at           DATETIME(3)  NULL COMMENT '到期时间（UTC）；NULL=长期',
    created_by           VARCHAR(64)  NOT NULL COMMENT '创建主体',
    updated_by           VARCHAR(64)  NOT NULL COMMENT '最近更新主体',
    created_at           DATETIME(3)  NOT NULL COMMENT '创建时间（UTC）',
    updated_at           DATETIME(3)  NOT NULL COMMENT '更新时间（UTC）',
    PRIMARY KEY (id),
    UNIQUE KEY uk_config_secret (env, namespace, name),
    KEY idx_config_secret_status (env, namespace, status),
    KEY idx_config_secret_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='业务密钥元数据（M2）；密文在 config_secret_version';

CREATE TABLE IF NOT EXISTS config_secret_version (
    id           BIGINT        NOT NULL COMMENT '主键：雪花 ID',
    secret_id    BIGINT        NOT NULL COMMENT '所属密钥 ID',
    version      BIGINT        NOT NULL COMMENT '版本号：同 secret 内单调递增',
    ciphertext   MEDIUMBLOB    NOT NULL COMMENT 'AES-256-GCM 密文',
    nonce        VARBINARY(12) NOT NULL COMMENT 'GCM nonce（12 字节）',
    wrapped_dek  VARBINARY(256) NOT NULL COMMENT 'KEK wrap 后的 DEK',
    kek_key_id   VARCHAR(64)   NOT NULL COMMENT 'KEK 版本标识；不返回业务',
    algorithm    VARCHAR(32)   NOT NULL DEFAULT 'aes-256-gcm' COMMENT '信封算法',
    value_hash   CHAR(16)      NOT NULL COMMENT 'SHA-256(plaintext) 前 8 字节 hex 指纹',
    status       VARCHAR(16)   NOT NULL COMMENT 'active/grace/revoked/destroyed',
    created_by   VARCHAR(64)   NOT NULL COMMENT '写入主体',
    created_at   DATETIME(3)   NOT NULL COMMENT '写入时间（UTC）',
    revoked_at   DATETIME(3)   NULL COMMENT '吊销时间',
    destroyed_at DATETIME(3)   NULL COMMENT '物理销毁时间',
    PRIMARY KEY (id),
    UNIQUE KEY uk_config_secret_version (secret_id, version),
    KEY idx_config_secret_version_status (secret_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='业务密钥版本（只追加）；列表查询不得 SELECT 密文列';

CREATE TABLE IF NOT EXISTS config_secret_grant (
    id               BIGINT       NOT NULL COMMENT '主键：雪花 ID',
    env              VARCHAR(16)  NOT NULL COMMENT '环境',
    namespace        VARCHAR(64)  NOT NULL COMMENT '命名空间',
    service_identity VARCHAR(256) NOT NULL COMMENT '证书 SAN / SPIFFE ID；禁止通配符',
    secret_id        BIGINT       NOT NULL COMMENT '授权密钥 ID',
    permission       VARCHAR(16)  NOT NULL DEFAULT 'resolve' COMMENT '仅 resolve',
    expires_at       DATETIME(3)  NULL COMMENT '授权到期；NULL=长期',
    created_by       VARCHAR(64)  NOT NULL COMMENT '创建主体',
    created_at       DATETIME(3)  NOT NULL COMMENT '创建时间',
    revoked_at       DATETIME(3)  NULL COMMENT '收回时间',
    PRIMARY KEY (id),
    UNIQUE KEY uk_config_secret_grant (env, namespace, service_identity, secret_id),
    KEY idx_config_secret_grant_secret (secret_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='密钥解析服务授权；默认拒绝无 grant';

CREATE TABLE IF NOT EXISTS config_secret_review (
    id         BIGINT       NOT NULL COMMENT '主键：雪花 ID',
    secret_id  BIGINT       NOT NULL COMMENT '所属密钥 ID',
    action     VARCHAR(16)  NOT NULL COMMENT 'create/rotate/revoke/grant/activate',
    reviewer   VARCHAR(64)  NOT NULL COMMENT '审批人',
    decision   VARCHAR(16)  NOT NULL COMMENT 'approved/rejected',
    comment    VARCHAR(512) NOT NULL DEFAULT '' COMMENT '审批意见',
    created_at DATETIME(3)  NOT NULL COMMENT '审批时间',
    PRIMARY KEY (id),
    KEY idx_config_secret_review (secret_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='密钥变更审批记录';
