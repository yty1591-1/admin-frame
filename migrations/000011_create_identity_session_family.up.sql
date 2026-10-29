-- 设备会话（family）与令牌代（session）拆分（IDN-P1-01）。
-- 设计要点：
--   1. identity_session_family 保存设备级状态：列表、撤销与单账号设备上限都以 family 为单位；
--   2. identity_session 继续保存每一代刷新令牌，用于重放检测与审计；
--   3. family_id 取首次登录的会话 ID，与既有数据保持一致；
--   4. 迁移按 family_id 回填，并把设备当前状态取为「任一 active 会话存在即 active」；
--   5. 新增索引支撑按账号 + 最近使用列设备、按 family 查令牌代与保留期清理。

CREATE TABLE IF NOT EXISTS identity_session_family (
    family_id           BIGINT       NOT NULL                 COMMENT '设备会话主键，取首次登录的会话 ID',
    account_id          BIGINT       NOT NULL                 COMMENT '账号ID，关联 identity_account.id',
    status              VARCHAR(16)  NOT NULL DEFAULT 'active' COMMENT '状态：active / revoked / expired',
    version             BIGINT       NOT NULL DEFAULT 1       COMMENT '乐观锁版本；刷新与撤销时递增',
    created_at          DATETIME(3)  NOT NULL                 COMMENT '创建时间（UTC）',
    last_used_at        DATETIME(3)  NOT NULL                 COMMENT '最近一次刷新时间（UTC）',
    expires_at          DATETIME(3)  NOT NULL                 COMMENT '当前令牌代过期时间（UTC）',
    absolute_expires_at DATETIME(3)  NOT NULL                 COMMENT '设备绝对到期时间（UTC）；刷新不得延长',
    ip_hash             VARCHAR(64)  NOT NULL DEFAULT ''      COMMENT '客户端 IP 摘要；禁止存原文',
    ua_hash             VARCHAR(64)  NOT NULL DEFAULT ''      COMMENT 'User-Agent 摘要；禁止存原文',
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

-- 回填既有会话：每个 family_id 一条设备记录，状态取「任一 active 即 active」，否则 revoked / expired。
INSERT INTO identity_session_family
    (family_id, account_id, status, version, created_at, last_used_at, expires_at,
     absolute_expires_at, ip_hash, ua_hash, device_label, revoked_at, revoked_reason, updated_at)
SELECT
    family_id,
    MAX(account_id),
    CASE
        WHEN SUM(status = 'active') > 0 THEN 'active'
        WHEN SUM(status = 'revoked') > 0 THEN 'revoked'
        ELSE 'expired'
    END,
    1,
    MIN(created_at),
    MAX(last_used_at),
    MAX(expires_at),
    MAX(expires_at),
    '',
    '',
    '',
    MAX(revoked_at),
    MAX(revoked_reason),
    MAX(updated_at)
FROM identity_session
GROUP BY family_id
ON DUPLICATE KEY UPDATE family_id = family_id;

-- 令牌代查询与清理：按 family + 状态、按账号 + 最近使用。
ALTER TABLE identity_session
    ADD KEY idx_identity_session_family_status (family_id, status),
    ADD KEY idx_identity_session_account_last_used (account_id, last_used_at);
