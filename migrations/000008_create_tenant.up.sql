-- ENT-09：租户主数据表。
--
-- 设计要点（ADR-0006）：
--   1. tenant.id 即隔离边界：B 端各表的 merchant_id 直接存 tenant.id，不引入映射表；
--   2. 状态机 pending → active → suspended → terminated，终止为终态，不允许复活；
--   3. version 列做乐观锁：状态迁移必须带版本，避免并发「停用 + 启用」互相覆盖；
--   4. 停用 / 终止时间与原因单独留痕，供审计与合同结算回溯。
--
-- 未建 tenant_member / tenant_quota：成员与配额分别由成员用例与 ENT-14 落地，
-- 建表但无读写路径属死表（DATA-08 禁止「预留字段 / 预留表」）。
CREATE TABLE IF NOT EXISTS tenant (
    id              VARCHAR(64)   NOT NULL                      COMMENT '租户ID；B端表的 merchant_id 取同一值',
    name            VARCHAR(128)  NOT NULL                      COMMENT '租户名称（企业 / 商户简称）',
    status          VARCHAR(16)   NOT NULL                      COMMENT 'pending | active | suspended | terminated',
    status_reason   VARCHAR(255)  NOT NULL DEFAULT ''           COMMENT '最近一次状态变更原因；审计外的运营说明',
    version         BIGINT        NOT NULL DEFAULT 1            COMMENT '乐观锁版本；每次成功迁移 +1',
    suspended_at    DATETIME(3)   NULL                          COMMENT '最近一次停用时间；恢复后保留历史',
    terminated_at   DATETIME(3)   NULL                          COMMENT '终止时间；终态，非空即不可再迁移',
    created_at      DATETIME(3)   NOT NULL,
    updated_at      DATETIME(3)   NOT NULL,
    PRIMARY KEY (id),
    KEY idx_tenant_status_created (status, created_at)          COMMENT '按状态列租户（运营待办）',
    KEY idx_tenant_name (name)                                  COMMENT '按名称检索'
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '租户主数据（隔离边界，ENT-09）';
