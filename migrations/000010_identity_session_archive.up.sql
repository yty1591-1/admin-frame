-- 身份域会话归档：在线表只保留保留期内的数据，历史会话转入归档表。
--
-- 背景（IDN-GAP-P1-04）：
--   每次刷新都会插入新的 identity_session 令牌代；清理任务只删不归档，
--   在线表随刷新频率持续增长，且历史会话无法追溯。
-- 设计：
--   1. 归档表与在线表结构一致，额外 archived_at 记录归档时间（UTC）；
--   2. 归档表不建外键：账号删除采用软删，归档数据需长期保留；
--   3. 归档与在线删除在清理任务的同一事务内完成，避免半状态；
--   4. 索引按 archived_at 支撑归档保留期清理，按 account_id 支撑历史查询。
-- 回滚：
--   停止归档任务即回到「只删不归档」；归档表可保留，不影响在线查询。

CREATE TABLE IF NOT EXISTS identity_session_archive (
    id                 BIGINT       NOT NULL                 COMMENT '会话主键，沿用在线表雪花 ID',
    account_id         BIGINT       NOT NULL                 COMMENT '账号ID',
    family_id          BIGINT       NOT NULL                 COMMENT '会话链家庭ID',
    refresh_token_hash CHAR(64)     NOT NULL                 COMMENT '刷新令牌 SHA-256 摘要；归档后仍禁止存原文',
    status             VARCHAR(16)  NOT NULL                 COMMENT '归档时状态：rotated / revoked / expired',
    token_version      BIGINT       NOT NULL                 COMMENT '签发时的账号撤销版本',
    version            BIGINT       NOT NULL                 COMMENT '归档时的乐观锁版本',
    expires_at         DATETIME(3)  NOT NULL                 COMMENT '绝对过期时间（UTC）',
    idle_expires_at    DATETIME(3)  NULL                     COMMENT '空闲上限（UTC）',
    last_used_at       DATETIME(3)  NOT NULL                 COMMENT '最近使用时间（UTC）',
    revoked_at         DATETIME(3)  NULL                     COMMENT '撤销时间（UTC）',
    revoked_reason     VARCHAR(32)  NOT NULL DEFAULT ''      COMMENT '撤销原因',
    created_at         DATETIME(3)  NOT NULL                 COMMENT '创建时间（UTC）',
    updated_at         DATETIME(3)  NOT NULL                 COMMENT '原表最后更新时间（UTC）',
    archived_at        DATETIME(3)  NOT NULL                 COMMENT '归档时间（UTC）',
    PRIMARY KEY (id),
    KEY idx_identity_session_archive_archived (archived_at)
        COMMENT '归档保留期清理扫描',
    KEY idx_identity_session_archive_account (account_id, created_at)
        COMMENT '按账号追溯历史会话'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='历史会话归档：只由清理任务写入，不参与在线鉴权';

CREATE TABLE IF NOT EXISTS identity_session_family_archive (
    family_id           BIGINT       NOT NULL                 COMMENT '设备会话主键',
    account_id          BIGINT       NOT NULL                 COMMENT '账号ID',
    status              VARCHAR(16)  NOT NULL                 COMMENT '归档时状态：revoked / expired',
    version             BIGINT       NOT NULL                 COMMENT '归档时的乐观锁版本',
    created_at          DATETIME(3)  NOT NULL                 COMMENT '创建时间（UTC）',
    last_used_at        DATETIME(3)  NOT NULL                 COMMENT '最近一次刷新时间（UTC）',
    expires_at          DATETIME(3)  NOT NULL                 COMMENT '当前令牌代过期时间（UTC）',
    absolute_expires_at DATETIME(3)  NOT NULL                 COMMENT '设备绝对到期时间（UTC）',
    ip_hash             VARCHAR(128) NOT NULL DEFAULT ''      COMMENT '客户端 IP 摘要',
    ua_hash             VARCHAR(128) NOT NULL DEFAULT ''      COMMENT 'User-Agent 摘要',
    device_label        VARCHAR(64)  NOT NULL DEFAULT ''      COMMENT '设备标签',
    revoked_at          DATETIME(3)  NULL                     COMMENT '撤销时间（UTC）',
    revoked_reason      VARCHAR(32)  NOT NULL DEFAULT ''      COMMENT '撤销原因',
    updated_at          DATETIME(3)  NOT NULL                 COMMENT '原表最后更新时间（UTC）',
    archived_at         DATETIME(3)  NOT NULL                 COMMENT '归档时间（UTC）',
    PRIMARY KEY (family_id),
    KEY idx_identity_session_family_archive_archived (archived_at)
        COMMENT '归档保留期清理扫描',
    KEY idx_identity_session_family_archive_account (account_id, last_used_at)
        COMMENT '按账号追溯历史设备'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='历史设备会话归档：只由清理任务写入，不参与在线鉴权';
