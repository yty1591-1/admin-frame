-- 000012 (up): 套餐模型（ENT-09 T-24）——tenant_plan 目录 + tenant.plan_id + 存量回填。
--
-- 语义（ADR-0006 补充）：套餐是「开账模板」——创建租户时把套餐配额物化进
-- tenant_quota，之后套餐调整只影响新建租户。存量租户的回填同样只物化一次，
-- 不覆盖已存在的租户级覆盖（INSERT ... ON DUPLICATE KEY UPDATE 仅补缺）。

CREATE TABLE IF NOT EXISTS tenant_plan (
    id          VARCHAR(64)  NOT NULL COMMENT '套餐 ID（雪花）',
    code        VARCHAR(64)  NOT NULL COMMENT '人类可读编码，全局唯一，如 standard / enterprise',
    name        VARCHAR(128) NOT NULL COMMENT '展示名',
    is_default  TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '创建租户未指定套餐时的默认套餐；目录内应恰好一个',
    quotas      JSON         NOT NULL COMMENT '默认配额数组：[{"resource":"user","limit":50}]；资源键与 tenant_quota.resource 同约束',
    created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_tenant_plan_code (code),
    KEY idx_tenant_plan_default (is_default)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '租户套餐目录（ENT-09 T-24）：配额默认值的开账模板';

-- 标准套餐：与既有运维口径对齐的保守默认值。
INSERT INTO tenant_plan (id, code, name, is_default, quotas, created_at, updated_at)
VALUES ('plan-standard-0001', 'standard', '标准套餐', 1,
        JSON_ARRAY(JSON_OBJECT('resource', 'user', 'limit', 50),
                   JSON_OBJECT('resource', 'api.rps', 'limit', 100)),
        UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
ON DUPLICATE KEY UPDATE updated_at = updated_at; -- 幂等：已存在则不动（套餐是开账模板，种子不追改）。

-- 存量租户回填默认套餐；plan_id 允许 NULL（回填失败不阻塞迁移，领域层容忍空值）。
ALTER TABLE tenant
    ADD COLUMN plan_id VARCHAR(64) NULL COMMENT '开账套餐（tenant_plan.id）；创建时物化配额进 tenant_quota' AFTER status_reason,
    ADD CONSTRAINT fk_tenant_plan FOREIGN KEY (plan_id) REFERENCES tenant_plan (id);

UPDATE tenant
SET plan_id = (SELECT id FROM (SELECT id FROM tenant_plan WHERE is_default = 1 LIMIT 1) d)
WHERE plan_id IS NULL;

-- 存量租户物化套餐默认配额：只补缺，不覆盖既有租户级覆盖（含 000010 回填产生的记录）。
-- 用 LEFT JOIN 防重而不是 ON DUPLICATE KEY UPDATE：与 JOIN 混用时 ON 子句有解析歧义，
-- 且「只补缺」的意图在这里更直白。
INSERT INTO tenant_quota (tenant_id, resource, limit_value, updated_at)
SELECT t.id, jt.resource, jt.limit_value, UTC_TIMESTAMP(3)
FROM tenant t
JOIN tenant_plan p ON p.id = t.plan_id
JOIN JSON_TABLE(p.quotas, '$[*]' COLUMNS (
        resource    VARCHAR(64) PATH '$.resource',
        limit_value BIGINT      PATH '$.limit'
    )) jt
LEFT JOIN tenant_quota q ON q.tenant_id = t.id AND q.resource = jt.resource
WHERE q.tenant_id IS NULL;
