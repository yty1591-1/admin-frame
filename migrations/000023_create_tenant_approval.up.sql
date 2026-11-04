-- ENT-09 审核流：入驻审批事件表（append-only）。
--
-- 为什么是事件表而不是单行状态：
--   申请会被驳回后重新提交，单行状态表只能保留最后一次结果，
--   「上次为什么被驳回」被覆盖，审核留痕不成立。
--   当前审批态由事件序列派生（submit → submitted，approve → approved，
--   reject → rejected），代码见 internal/tenant/domain/approval.go。
--
-- 只追加、不更新、不删除；租户终止后按 D-05 处置时随边界级联删除，
-- 审批事实仍由审计事件（tenant.approval.*）保留 365 天。
CREATE TABLE IF NOT EXISTS tenant_approval (
    id          BIGINT       NOT NULL AUTO_INCREMENT,
    tenant_id   VARCHAR(64)  NOT NULL                  COMMENT '租户ID（隔离边界）',
    action      VARCHAR(16)  NOT NULL                  COMMENT 'submit | approve | reject',
    actor_id    VARCHAR(64)  NOT NULL                  COMMENT '操作者主体ID；提交人与审批人不得同一人',
    note        VARCHAR(255) NOT NULL DEFAULT ''       COMMENT '申请说明 / 审批意见；驳回时必填',
    created_at  DATETIME(3)  NOT NULL                  COMMENT '事件时间（UTC）',
    PRIMARY KEY (id),
    KEY idx_tenant_approval_tenant (tenant_id, id)     COMMENT '按租户取最近事件（派生当前态）',
    CONSTRAINT fk_tenant_approval_tenant FOREIGN KEY (tenant_id)
        REFERENCES tenant (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '租户入驻审批事件（append-only，ENT-09 审核流）';
