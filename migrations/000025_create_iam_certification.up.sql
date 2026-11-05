-- P1-7 访问复核（Access Certification）基线表。
--
-- 设计要点：
--   1. 活动（campaign）承载范围与截止时间；任务（task）承载「主体 + 角色 + 绑定」的复核单元；
--   2. 任务唯一键 (campaign_id, subject_id, role_id) 保证重复生成清单幂等，
--      已裁决任务不会被重新生成的草稿覆盖；
--   3. binding_fingerprint 记录生成时的绑定关键字段摘要，裁决驳回前二次校验，
--      防止复核期间解绑 / 重绑 / 改期后旧任务误回收（TOCTOU）；
--   4. 只做加法：不修改既有 IAM 表与授权关系。
CREATE TABLE IF NOT EXISTS iam_certification_campaign (
    id               BIGINT        NOT NULL                  COMMENT '活动主键，雪花 ID（pkg/id 生成），禁止自增',
    name             VARCHAR(128)  NOT NULL                  COMMENT '活动显示名，如「2026 Q4 特权账号复核」',
    channel          VARCHAR(16)   NOT NULL                  COMMENT '端别：admin=A端平台 / biz=B端商户',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT 'B端所属商户ID；A端为空串',
    role_ids         JSON          NOT NULL                  COMMENT '复核角色范围：空数组=全部角色；元素为角色ID字符串',
    privileged_only  TINYINT       NOT NULL DEFAULT 0        COMMENT '1=只复核含高危权限（iam:* / data:export / config:*）的角色',
    status           VARCHAR(16)   NOT NULL DEFAULT 'draft'  COMMENT '状态机：draft=草稿 / active=进行中 / closed=已关闭',
    due_at           DATETIME(3)   NOT NULL                  COMMENT '裁决截止时间（UTC）；到期后不允许人工裁决',
    created_by       VARCHAR(64)   NOT NULL                  COMMENT '创建人 subject_id；默认复核人',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '创建时间（UTC，毫秒精度）',
    closed_at        DATETIME(3)   NULL                      COMMENT '关闭时间（UTC）；NULL=未关闭',
    version          BIGINT        NOT NULL DEFAULT 1        COMMENT '乐观锁版本；更新须 WHERE version=?，冲突返回 40900',
    task_count       INT           NOT NULL DEFAULT 0        COMMENT '活动下任务总数，生成清单后同步',
    PRIMARY KEY (id),
    KEY idx_iam_cert_campaign_status (channel, merchant_id, status, created_at)
        COMMENT '列表：按端别 + 商户 + 状态 + 创建时间扫描',
    CONSTRAINT ck_iam_cert_campaign_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_cert_campaign_status CHECK (status IN ('draft', 'active', 'closed')),
    CONSTRAINT ck_iam_cert_campaign_tenant CHECK (
        (channel = 'admin' AND merchant_id = '')
     OR (channel = 'biz' AND merchant_id <> '')
    ),
    CONSTRAINT ck_iam_cert_campaign_version CHECK (version >= 1),
    CONSTRAINT ck_iam_cert_campaign_task_count CHECK (task_count >= 0),
    CONSTRAINT ck_iam_cert_campaign_role_ids CHECK (JSON_TYPE(role_ids) = 'ARRAY')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 访问复核活动：范围、截止时间与生命周期';

CREATE TABLE IF NOT EXISTS iam_certification_task (
    id                  BIGINT        NOT NULL                  COMMENT '任务主键，雪花 ID',
    campaign_id         BIGINT        NOT NULL                  COMMENT '所属活动，关联 iam_certification_campaign.id',
    subject_type        VARCHAR(32)   NOT NULL                  COMMENT '主体类型：staff=A端员工 / merchant_staff=B端商户员工',
    subject_id          VARCHAR(64)   NOT NULL                  COMMENT '主体ID',
    merchant_id         VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT 'B端商户ID；A端为空串',
    role_id             BIGINT        NOT NULL                  COMMENT '复核角色ID，关联 iam_role.id',
    binding_id          BIGINT        NOT NULL                  COMMENT '被复核的绑定ID，关联 iam_role_binding.id',
    binding_fingerprint CHAR(64)      NOT NULL                  COMMENT '绑定关键字段 sha256 摘要，裁决前二次校验',
    reviewer_id         VARCHAR(64)   NOT NULL                  COMMENT '复核人 subject_id；默认活动创建人',
    status              VARCHAR(16)   NOT NULL DEFAULT 'pending' COMMENT '状态：pending / confirmed / revoked / expired / skipped',
    decision            VARCHAR(16)   NOT NULL DEFAULT ''       COMMENT '裁决动作：confirmed=保留 / revoked=驳回；自动动作可空',
    reason              VARCHAR(512)  NOT NULL DEFAULT ''       COMMENT '裁决 / 跳过原因',
    decided_by          VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT '裁决人 subject_id；自动动作写 system:certification',
    decided_at          DATETIME(3)   NULL                      COMMENT '裁决 / 过期 / 跳过时间（UTC）',
    created_at          DATETIME(3)   NOT NULL                  COMMENT '生成时间（UTC，毫秒精度）',
    version             BIGINT        NOT NULL DEFAULT 1        COMMENT '乐观锁版本；每次状态变更自增',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_cert_task (campaign_id, subject_id, role_id)
        COMMENT '重复生成清单幂等：同一活动内同一主体同一角色至多一条任务',
    KEY idx_iam_cert_task_status (campaign_id, status, created_at, id)
        COMMENT '任务列表与到期扫描：按活动 + 状态 + 时间排序',
    KEY idx_iam_cert_task_binding (binding_id)
        COMMENT '按绑定反查复核历史',
    CONSTRAINT fk_iam_cert_task_campaign FOREIGN KEY (campaign_id)
        REFERENCES iam_certification_campaign (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_iam_cert_task_binding FOREIGN KEY (binding_id)
        REFERENCES iam_role_binding (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_cert_task_status CHECK (
        status IN ('pending', 'confirmed', 'revoked', 'expired', 'skipped')
    ),
    CONSTRAINT ck_iam_cert_task_decision CHECK (
        decision IN ('', 'confirmed', 'revoked')
    ),
    CONSTRAINT ck_iam_cert_task_version CHECK (version >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 访问复核任务：主体-角色-绑定复核单元与裁决证据';
