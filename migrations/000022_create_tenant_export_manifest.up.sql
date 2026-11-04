-- ENT-15：租户导出 manifest 表（D-05 三段式处置的证据锚点）。
--
-- 保留期结束进入导出窗口后，导出包生成并校验通过时在此登记一行；
-- 硬删除任务（tenant purge）只删除「存在已校验 manifest」的租户——
-- 无导出证据的租户一律跳过并告警，绝不裸删（tenant-data-lifecycle.md §到期流程）。
-- manifest 自身保留期 ≥ 365d（处置报告的证据链），不随租户硬删除清理。

CREATE TABLE IF NOT EXISTS tenant_export_manifest (
    id               BIGINT        NOT NULL AUTO_INCREMENT,
    tenant_id        VARCHAR(64)   NOT NULL                  COMMENT '租户ID（隔离边界）',
    manifest_sha256  CHAR(64)      NOT NULL                  COMMENT '导出包清单文件 SHA-256（校验通过后登记）',
    file_count       INT           NOT NULL DEFAULT 0        COMMENT '导出包含文件数',
    total_rows       BIGINT        NOT NULL DEFAULT 0        COMMENT '导出包含总行数',
    acknowledged_at  DATETIME(3)   NULL                      COMMENT '客户签收时间（UTC）；NULL=未签收（导出窗口逾期视为放弃）',
    created_by       VARCHAR(64)   NOT NULL                  COMMENT '登记操作者（A 端主体或 worker 系统名）',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '登记时间（UTC）',
    PRIMARY KEY (id),
    KEY idx_tenant_export_manifest_tenant (tenant_id, created_at) COMMENT '按租户找最新导出证据',
    KEY idx_tenant_export_manifest_created (created_at) COMMENT 'manifest 自身保留期治理'
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '租户数据导出 manifest（D-05 硬删除前置证据）';
