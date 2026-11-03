-- ENT-09：租户成员与配额。
--
-- 设计要点：
--   1. 成员主键 (tenant_id, subject_id)：同一主体在一个租户内只有一条记录，
--      重复添加走幂等 upsert 而不是插入第二行；
--   2. 成员用 status 表达「停用 / 启用」，不做物理删除：删除会丢掉「曾在过这个租户」
--      的事实，而离职回溯、权限审计都依赖它（DATA-08 禁止无痕删除）；
--   3. 配额用 limit_value 表示上限，-1 表示不限；缺行表示「未配置」，由 ENT-14
--      在限流时按默认值兜底，不在本表预置全量资源；
--   4. 外键级联：租户行被删除时成员与配额随之清理（正常流程不删租户，仅迁移回滚会用）。
--
-- 「至少一名 owner」是跨行不变量，SQL 无法表达，由应用层在降级 / 停用时校验。
CREATE TABLE IF NOT EXISTS tenant_member (
    tenant_id   VARCHAR(64)  NOT NULL                       COMMENT '租户ID',
    subject_id  VARCHAR(64)  NOT NULL                       COMMENT '成员主体ID（A/B 端账号主体）',
    role        VARCHAR(16)  NOT NULL                       COMMENT 'owner | admin | member',
    status      VARCHAR(16)  NOT NULL                       COMMENT 'active | disabled',
    created_at  DATETIME(3)  NOT NULL,
    updated_at  DATETIME(3)  NOT NULL,
    PRIMARY KEY (tenant_id, subject_id),
    KEY idx_tenant_member_subject (subject_id)              COMMENT '按主体反查所属租户',
    KEY idx_tenant_member_role_status (tenant_id, role, status) COMMENT '统计在册 owner，校验最后一名 owner',
    CONSTRAINT fk_tenant_member_tenant FOREIGN KEY (tenant_id) REFERENCES tenant (id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '租户成员（ENT-09）';

CREATE TABLE IF NOT EXISTS tenant_quota (
    tenant_id    VARCHAR(64)  NOT NULL                      COMMENT '租户ID',
    resource     VARCHAR(64)  NOT NULL                      COMMENT '配额资源键，如 user / api.rps / storage.mb',
    limit_value  BIGINT       NOT NULL                      COMMENT '上限；-1 表示不限',
    updated_at   DATETIME(3)  NOT NULL,
    PRIMARY KEY (tenant_id, resource),
    CONSTRAINT fk_tenant_quota_tenant FOREIGN KEY (tenant_id) REFERENCES tenant (id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '租户配额（ENT-09 定义，ENT-14 执行）';
