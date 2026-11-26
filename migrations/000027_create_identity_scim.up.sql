-- SCIM 2.0 目录同步（docs/identity-authn.md §21.1.8）。
--
-- 设计要点：
--   1. 只增不改：本迁移不修改既有列语义，仅把 identity_oauth_identity.provider
--      从 VARCHAR(32) 扩宽到 VARCHAR(64)，容纳 scim:<provider>（扩宽为兼容变更）；
--   2. 令牌只存 SHA-256 摘要与展示指纹，明文只在签发响应出现一次；
--   3. 每组最多 2 个 active 令牌（当前 + 待退役）由应用在事务内校验，
--      这里只建 INDEX(provider, status) 供统计，不用部分唯一索引（MySQL 8.0 不支持）；
--   4. 组软删保留 external_id / display_name 唯一性占位，避免 IdP 复用同名组造成歧义；
--   5. 成员表不建级联删除：组删除由应用在同一事务内先清成员再软删组，
--      防止误删账号或留下悬空绑定。

ALTER TABLE identity_oauth_identity
    MODIFY COLUMN provider VARCHAR(64) NOT NULL
        COMMENT '外部身份提供方标识，如 oidc-corp / saml:keycloak / scim:okta';

CREATE TABLE IF NOT EXISTS identity_scim_token (
    id           BIGINT       NOT NULL                  COMMENT '令牌主键，雪花 ID',
    provider     VARCHAR(64)  NOT NULL                  COMMENT '目录提供方标识（不含 scim: 前缀）',
    token_hash   CHAR(64)     NOT NULL                  COMMENT 'SCIM 令牌 SHA-256 十六进制摘要；禁止存原文',
    fingerprint  VARCHAR(32)  NOT NULL                  COMMENT '展示用指纹（sha256:xxxxxxxxxxxx），不可反推明文',
    status       VARCHAR(16)  NOT NULL DEFAULT 'active' COMMENT '状态：active=生效 / retiring=退役宽限 / revoked=已吊销',
    expires_at   DATETIME(3)  NULL                      COMMENT '退役宽限到期时间（UTC）；仅 retiring 有值',
    created_by   VARCHAR(64)  NOT NULL DEFAULT ''       COMMENT '签发人主体 ID',
    created_at   DATETIME(3)  NOT NULL                  COMMENT '创建时间（UTC）',
    revoked_at   DATETIME(3)  NULL                      COMMENT '吊销时间（UTC）；非空表示立即不可用',
    PRIMARY KEY (id),
    UNIQUE KEY uk_identity_scim_token_hash (token_hash)
        COMMENT '摘要全局唯一，避免同一令牌被两个 provider 持有',
    KEY idx_identity_scim_token_provider (provider, status)
        COMMENT '每 provider active 令牌计数（≤2）与列表查询',
    CONSTRAINT ck_identity_scim_token_status CHECK (status IN ('active', 'retiring', 'revoked'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='SCIM 目录同步令牌：只存摘要与指纹，支持双令牌轮换与退役宽限';

CREATE TABLE IF NOT EXISTS identity_scim_group (
    id            BIGINT       NOT NULL                 COMMENT '组主键，雪花 ID',
    provider      VARCHAR(64)  NOT NULL                 COMMENT '目录提供方标识',
    external_id   VARCHAR(255) NOT NULL                 COMMENT 'IdP 侧外部 ID，幂等键',
    display_name  VARCHAR(255) NOT NULL                 COMMENT '组展示名，角色映射键',
    mapped_role   VARCHAR(64)  NOT NULL DEFAULT ''      COMMENT 'group_role_map 解析出的 A 端角色 code；空=未映射',
    version       BIGINT       NOT NULL DEFAULT 1       COMMENT '乐观锁与 meta.version / ETag 来源',
    created_at    DATETIME(3)  NOT NULL                 COMMENT '创建时间（UTC）',
    updated_at    DATETIME(3)  NOT NULL                 COMMENT '最后更新时间（UTC）',
    deleted_at    DATETIME(3)  NULL                     COMMENT '软删时间（UTC）；NULL=未删除',
    PRIMARY KEY (id),
    UNIQUE KEY uk_identity_scim_group_external (provider, external_id)
        COMMENT '同一 provider 内 external_id 唯一；软删后仍占用，防止幂等键复用',
    UNIQUE KEY uk_identity_scim_group_name (provider, display_name)
        COMMENT '同一 provider 内展示名唯一，作为角色映射键',
    KEY idx_identity_scim_group_provider (provider, deleted_at, created_at)
        COMMENT '列表分页：按 provider + 未删除 + 创建时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='SCIM 组：只承载 IdP 组与角色映射，不替代 IAM 角色';

CREATE TABLE IF NOT EXISTS identity_scim_group_member (
    group_id   BIGINT      NOT NULL COMMENT '组主键，关联 identity_scim_group.id',
    account_id BIGINT      NOT NULL COMMENT '账号主键，关联 identity_account.id',
    created_at DATETIME(3) NOT NULL COMMENT '加入时间（UTC）',
    PRIMARY KEY (group_id, account_id),
    KEY idx_identity_scim_member_account (account_id)
        COMMENT '按账号反查所属组（User.groups 只读属性）',
    CONSTRAINT fk_identity_scim_member_group FOREIGN KEY (group_id)
        REFERENCES identity_scim_group (id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_identity_scim_member_account FOREIGN KEY (account_id)
        REFERENCES identity_account (id) ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='SCIM 组成员关系：权限投影的唯一来源，删除即回收对应角色绑定';
