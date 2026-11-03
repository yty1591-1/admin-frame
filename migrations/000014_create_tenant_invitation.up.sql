-- 000014 (up): 邀请流（ENT-09 T-14）——tenant_invitation 表。
--
-- 设计要点：
--   1. token 只存 SHA-256 摘要（token_hash）：token 是高熵随机数（32 字节
--      crypto/rand），无需慢哈希；摘要化保证「库被拖走也伪造不了有效邀请」。
--      明文 token 仅在创建 / 重发响应里返回一次，由运营线下转交——
--      邮件投递依赖 ENT-10，就绪前「只落库不发送」是明确的阶段语义。
--   2. 同一 (tenant_id, email) 只允许一个 pending 邀请：用生成列 pending_key
--      （status='pending' 时取值、否则 NULL）做唯一索引——MySQL 唯一索引对
--      NULL 不去重，历史邀请（accepted / revoked / expired）不占约束，
--      重复邀请引导走 resend（作废旧 token、顺延有效期）而不是插第二行。
--      生成列必须是 VIRTUAL 而不是 STORED：8.0.46 实测带 STORED 生成列的表
--      再 ALTER ADD FOREIGN KEY 会报 1215（与索引复用顺序无关），VIRTUAL
--      无此限制且同样支持唯一索引。
--   3. 状态迁移只有三条出边（accepted / revoked / expired），全部从 pending
--      出发：仓储以 `WHERE status = 'pending'` 做 CAS，不需要版本列。
--   4. accepted_by 与 email 解耦：email 只是投递目标（T-14 阶段为记录字段），
--      实际接受的主体在受领时显式提供——账号系统（identity）的 subject_id
--      才是成员关系的键。

CREATE TABLE IF NOT EXISTS tenant_invitation (
    id           VARCHAR(64)  NOT NULL COMMENT '邀请 ID（雪花）',
    tenant_id    VARCHAR(64)  NOT NULL COMMENT '目标租户',
    email        VARCHAR(128) NOT NULL COMMENT '被邀请人邮箱（投递目标；T-14 阶段仅记录）',
    role         VARCHAR(16)  NOT NULL COMMENT '受邀角色：owner | admin | member',
    token_hash   CHAR(64)     NOT NULL COMMENT 'SHA-256(token) 十六进制摘要；明文不落库',
    status       VARCHAR(16)  NOT NULL COMMENT 'pending | accepted | revoked | expired',
    invited_by   VARCHAR(64)  NOT NULL COMMENT '发起邀请的操作者主体',
    accepted_by  VARCHAR(64)  NULL COMMENT '实际接受的主体（与 email 解耦）',
    expires_at   DATETIME(3)  NOT NULL COMMENT '过期时间；接受时惰性校验',
    accepted_at  DATETIME(3)  NULL COMMENT '接受时间',
    created_at   DATETIME(3)  NOT NULL,
    updated_at   DATETIME(3)  NOT NULL,
    pending_key  VARCHAR(193) GENERATED ALWAYS AS (
                     CASE WHEN status = 'pending' THEN CONCAT(tenant_id, ':', email) END
                 ) VIRTUAL COMMENT 'pending 去重键：同租户同邮箱仅一个待处理邀请',
    PRIMARY KEY (id),
    UNIQUE KEY uk_invitation_token (token_hash),
    UNIQUE KEY uk_invitation_pending (pending_key),
    KEY idx_invitation_tenant (tenant_id, status, created_at),
    CONSTRAINT fk_invitation_tenant FOREIGN KEY (tenant_id) REFERENCES tenant (id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '租户成员邀请（ENT-09 T-14）：token 摘要化、pending 唯一、全流程审计';
