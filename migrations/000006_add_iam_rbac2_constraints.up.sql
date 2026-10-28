-- RBAC2 约束基线：静态职责分离（SSD）、前置角色与角色基数约束。
-- 设计要点：
--   1. iam_sod_group 表达「同租户内互斥角色集」，max_active_roles 为同一主体在组内
--      至多可同时持有的角色数，默认 1 即经典两两互斥；
--   2. 组与角色、角色与前置角色均使用复合外键 (id, channel, merchant_id)，
--      在库层拒绝跨端 / 跨商户组合，应用层无需再做租户拼装；
--   3. 互斥判定基于「有效角色闭包」（绑定角色 + RBAC1 祖先），由应用层展开，
--      库层只保证关系本身合法；
--   4. 破玻璃角色不参与约束：既不能加入互斥组，也不能作为前置角色；
--   5. iam_role.max_bindings 为单角色最大有效绑定数，NULL 表示不限；
--      配置变更与超限校验由应用层在事务内完成；
--   6. 本迁移只落表结构，不改变任何鉴权读写路径（P1 阶段）。

CREATE TABLE IF NOT EXISTS iam_sod_group (
    id               BIGINT        NOT NULL                  COMMENT '互斥角色组主键，雪花 ID',
    channel          VARCHAR(16)   NOT NULL                  COMMENT 'admin=A端平台 / biz=B端商户',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT 'B端所属商户ID；A端为空串',
    code             VARCHAR(64)   NOT NULL                  COMMENT '组编码，端内（同商户）唯一；软删后不可复用',
    name             VARCHAR(128)  NOT NULL                  COMMENT '组显示名',
    description      VARCHAR(512)  NOT NULL DEFAULT ''       COMMENT '组说明，建议写清互斥的业务理由',
    max_active_roles INT           NOT NULL DEFAULT 1        COMMENT '同一主体在组内至多同时持有的角色数；1=两两互斥',
    status           VARCHAR(16)   NOT NULL DEFAULT 'active' COMMENT 'active=生效 / disabled=停用（停用后不再拦截新绑定）',
    version          BIGINT        NOT NULL DEFAULT 1        COMMENT '乐观锁版本；更新须 WHERE version=?，冲突返回 40900',
    created_by       VARCHAR(64)   NOT NULL                  COMMENT '创建人 subject_id',
    updated_by       VARCHAR(64)   NOT NULL                  COMMENT '最后更新人 subject_id',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '创建时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)   NOT NULL                  COMMENT '最后更新时间（UTC，毫秒精度）',
    deleted_at       DATETIME(3)   NULL                      COMMENT '软删时间（UTC）；NULL=未删除，非空后 code 不可复用',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_sod_group_scope_code (channel, merchant_id, code) COMMENT '端内组编码唯一',
    UNIQUE KEY uk_iam_sod_group_id_scope (id, channel, merchant_id) COMMENT '供组-角色复合外键引用，库层防止跨商户组合',
    KEY idx_iam_sod_group_status (channel, merchant_id, status) COMMENT '按端别 + 商户 + 状态列组',
    CONSTRAINT ck_iam_sod_group_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_sod_group_tenant CHECK (
        (channel = 'admin' AND merchant_id = '')
     OR (channel = 'biz' AND merchant_id <> '')
    ),
    CONSTRAINT ck_iam_sod_group_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT ck_iam_sod_group_max_active CHECK (max_active_roles BETWEEN 1 AND 64),
    CONSTRAINT ck_iam_sod_group_version CHECK (version >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='RBAC2 静态职责分离组：同组角色不可被同一主体同时持有';

CREATE TABLE IF NOT EXISTS iam_sod_group_role (
    group_id         BIGINT        NOT NULL                  COMMENT '互斥组ID，关联 iam_sod_group.id',
    role_id          BIGINT        NOT NULL                  COMMENT '组内角色ID，关联 iam_role.id',
    channel          VARCHAR(16)   NOT NULL                  COMMENT '冗余端别；由复合外键锁定与组 / 角色一致',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT '冗余商户ID；由复合外键锁定与组 / 角色一致',
    granted_by       VARCHAR(64)   NOT NULL                  COMMENT '最近一次写入人 subject_id',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '加入组的时间（UTC，毫秒精度）',
    PRIMARY KEY (group_id, role_id),
    KEY idx_iam_sod_group_role_role (role_id) COMMENT '反查某角色属于哪些互斥组，绑定期校验主路径',
    CONSTRAINT fk_iam_sod_group_role_group FOREIGN KEY (group_id, channel, merchant_id)
        REFERENCES iam_sod_group (id, channel, merchant_id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_iam_sod_group_role_role FOREIGN KEY (role_id, channel, merchant_id)
        REFERENCES iam_role (id, channel, merchant_id)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='互斥组 → 角色；复合外键阻止跨端 / 跨商户组合';

CREATE TABLE IF NOT EXISTS iam_role_prerequisite (
    role_id          BIGINT        NOT NULL                  COMMENT '需要前置条件的角色ID',
    required_role_id BIGINT        NOT NULL                  COMMENT '授予 role_id 前必须已持有的角色ID',
    channel          VARCHAR(16)   NOT NULL                  COMMENT '冗余端别；由复合外键锁定与两个角色一致',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT '冗余商户ID；由复合外键锁定与两个角色一致',
    created_by       VARCHAR(64)   NOT NULL                  COMMENT '写入人 subject_id',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '写入时间（UTC，毫秒精度）',
    PRIMARY KEY (role_id, required_role_id),
    KEY idx_iam_role_prerequisite_required (required_role_id) COMMENT '删除 / 停用前置角色前的影响面检查',
    CONSTRAINT fk_iam_role_prerequisite_role FOREIGN KEY (role_id, channel, merchant_id)
        REFERENCES iam_role (id, channel, merchant_id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_iam_role_prerequisite_required FOREIGN KEY (required_role_id, channel, merchant_id)
        REFERENCES iam_role (id, channel, merchant_id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_role_prerequisite_self CHECK (role_id <> required_role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='RBAC2 前置角色：授予 role_id 前必须已持有 required_role_id';

ALTER TABLE iam_role
    ADD COLUMN max_bindings INT NULL DEFAULT NULL
        COMMENT '单角色最大有效绑定数（RBAC2 基数约束）；NULL=不限' AFTER parent_id,
    ADD CONSTRAINT ck_iam_role_max_bindings CHECK (max_bindings IS NULL OR max_bindings >= 1);
