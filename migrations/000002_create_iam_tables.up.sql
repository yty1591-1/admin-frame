-- IAM（RBAC 权限管理）表结构。
-- 数据库基线：MySQL 8.0.19+（InnoDB / utf8mb4_0900_ai_ci）。
-- 设计要点：
--   1. 功能权限（iam_role_permission）与数据范围（iam_role_data_scope）正交；
--   2. 角色扁平、无继承，通过一个主体绑定多个角色表达组合；
--   3. 时间列统一 DATETIME(3) + 应用写入 UTC，避免 TIMESTAMP 的 2038 上限与隐式时区转换；
--   4. JSON 列使用原生 JSON 类型，由数据库校验合法性；
--   5. 外键仅用于阻止悬空授权与误删（RESTRICT），业务删除仍以软删为主。

CREATE TABLE IF NOT EXISTS iam_permission (
    code             VARCHAR(128)  NOT NULL                  COMMENT '权限点稳定标识，resource:action 或 domain:resource:action，全小写；发布后不可改名或复用',
    name             VARCHAR(128)  NOT NULL                  COMMENT '权限点显示名，用于菜单 / 按钮 / 授权勾选',
    channel          VARCHAR(16)   NOT NULL                  COMMENT '可授予端别：admin=仅A端 / biz=仅B端 / both=两端均可',
    resource         VARCHAR(64)   NOT NULL                  COMMENT '资源段，取权限码首段，如 order；用于数据范围匹配',
    action           VARCHAR(64)   NOT NULL                  COMMENT '动作段，取权限码末段，如 read / write / grant',
    kind             VARCHAR(16)   NOT NULL                  COMMENT '权限点形态：menu=菜单 / button=按钮 / api=接口',
    data_bearing     TINYINT       NOT NULL DEFAULT 0        COMMENT '是否涉及数据范围：1=是（授权时必须配置 iam_role_data_scope）/ 0=否',
    parent_code      VARCHAR(128)  NOT NULL DEFAULT ''       COMMENT '父节点权限码，根节点为空串；用于菜单树',
    path             VARCHAR(256)  NOT NULL DEFAULT ''       COMMENT '树路径，逗号分隔的祖先编码，用于子树查询与排序',
    sort             INT           NOT NULL DEFAULT 0        COMMENT '同级排序值，升序，值越小越靠前',
    status           VARCHAR(16)   NOT NULL DEFAULT 'active' COMMENT '状态：active=可用 / deprecated=已弃用（保留授权但不可新授）',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '创建时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)   NOT NULL                  COMMENT '最后更新时间（UTC，毫秒精度），由应用写入',
    PRIMARY KEY (code),
    KEY idx_iam_permission_channel_status (channel, status) COMMENT '菜单树按端别 + 状态过滤'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 权限点目录，代码所有、库内只读同步；不存放角色授权关系';

CREATE TABLE IF NOT EXISTS iam_role (
    id               BIGINT        NOT NULL                  COMMENT '角色主键，雪花 ID（pkg/id 生成），禁止自增',
    channel          VARCHAR(16)   NOT NULL                  COMMENT '角色所属端别：admin=A端平台 / biz=B端商户',
    scope_type       VARCHAR(16)   NOT NULL                  COMMENT '角色作用域：platform=A端全局 / merchant=B端单商户',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT 'B端所属商户ID；scope_type=platform 时为空串，禁止用空串代表“全部商户”',
    code             VARCHAR(64)   NOT NULL                  COMMENT '角色编码，端内（同商户）唯一；软删后不可复用，防止重建洗白',
    name             VARCHAR(128)  NOT NULL                  COMMENT '角色显示名',
    description      VARCHAR(512)  NOT NULL DEFAULT ''       COMMENT '角色说明，建议写清职责边界',
    role_type        VARCHAR(16)   NOT NULL DEFAULT 'custom' COMMENT '角色类型：system=系统内置 / custom=自定义 / breakglass=破玻璃，仅此类型可在审批有效期内展开 *',
    status           VARCHAR(16)   NOT NULL DEFAULT 'active' COMMENT '状态：active=生效 / disabled=停用（停用后快照不再加载其权限）',
    version          BIGINT        NOT NULL DEFAULT 1        COMMENT '乐观锁版本；更新须 WHERE version=?，冲突返回 40900',
    created_by       VARCHAR(64)   NOT NULL                  COMMENT '创建人 subject_id',
    updated_by       VARCHAR(64)   NOT NULL                  COMMENT '最后更新人 subject_id',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '创建时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)   NOT NULL                  COMMENT '最后更新时间（UTC，毫秒精度）',
    deleted_at       DATETIME(3)   NULL                      COMMENT '软删时间（UTC）；NULL=未删除，非空后 code 不可复用',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_role_scope_code (channel, merchant_id, code) COMMENT '端内角色编码唯一，绑定与授权的幂等防线',
    KEY idx_iam_role_merchant_status (merchant_id, status) COMMENT 'B端按商户 + 状态列角色',
    KEY idx_iam_role_channel_scope (channel, scope_type, status) COMMENT 'A端按端别 + 作用域列角色'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 角色定义，按端别与商户作用域隔离；角色扁平、无继承';

CREATE TABLE IF NOT EXISTS iam_role_permission (
    role_id          BIGINT        NOT NULL COMMENT '角色ID，关联 iam_role.id',
    permission_code  VARCHAR(128)  NOT NULL COMMENT '权限点编码，关联 iam_permission.code；仅 active 状态可授予',
    granted_by       VARCHAR(64)   NOT NULL COMMENT '最近一次授予人 subject_id',
    created_at       DATETIME(3)   NOT NULL COMMENT '授予时间（UTC，毫秒精度）',
    PRIMARY KEY (role_id, permission_code),
    KEY idx_iam_rp_permission (permission_code) COMMENT '反查某权限点被哪些角色引用，用于影响面与弃用评估',
    CONSTRAINT fk_iam_rp_role FOREIGN KEY (role_id) REFERENCES iam_role (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_iam_rp_permission FOREIGN KEY (permission_code) REFERENCES iam_permission (code)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='角色与权限点的授予关系；一行代表一次有效授予，删除即撤权；外键阻止悬空授权';

CREATE TABLE IF NOT EXISTS iam_role_binding (
    id               BIGINT        NOT NULL            COMMENT '绑定主键，雪花 ID',
    channel          VARCHAR(16)   NOT NULL            COMMENT '端别：admin / biz；须与角色 channel 一致',
    subject_type     VARCHAR(32)   NOT NULL            COMMENT '主体类型：staff=A端员工 / merchant_staff=B端商户员工',
    subject_id       VARCHAR(64)   NOT NULL            COMMENT '主体ID；A端为员工ID，B端为商户员工ID',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT '' COMMENT 'B端所属商户ID（隔离维度）；A端为空串；禁止以空串表示不限商户',
    role_id          BIGINT        NOT NULL            COMMENT '角色ID，关联 iam_role.id；角色须与绑定端别、商户一致',
    expires_at       DATETIME(3)   NULL                COMMENT '绑定过期时间（UTC）；NULL=长期有效；查询须过滤已过期记录',
    granted_by       VARCHAR(64)   NOT NULL            COMMENT '授权人 subject_id，用于审计与职责分离校验',
    created_at       DATETIME(3)   NOT NULL            COMMENT '绑定时间（UTC，毫秒精度）',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_binding (channel, subject_type, subject_id, merchant_id, role_id)
        COMMENT '同一主体对同一角色的重复绑定幂等，防并发重复插入',
    KEY idx_iam_binding_subject (channel, subject_type, subject_id, merchant_id)
        COMMENT '快照回源主查询路径：按主体取全部有效绑定',
    KEY idx_iam_binding_role (role_id)
        COMMENT '反查角色被哪些主体绑定，删除角色前校验引用',
    CONSTRAINT fk_iam_binding_role FOREIGN KEY (role_id) REFERENCES iam_role (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 主体-角色绑定，支持限时有效；鉴权快照的唯一绑定来源';

CREATE TABLE IF NOT EXISTS iam_role_data_scope (
    role_id          BIGINT        NOT NULL COMMENT '角色ID，关联 iam_role.id',
    resource         VARCHAR(64)   NOT NULL COMMENT '资源段：* 表示全部资源，或权限点首段如 order / file',
    scope_type       VARCHAR(16)   NOT NULL COMMENT '范围类型：all=全部 / merchant=当前商户 / self=本人 / merchant_set=指定商户集合；B端仅允许 merchant、self',
    scope_values     JSON          NOT NULL COMMENT '范围取值：merchant_set 存 {"merchant_ids":[...]}（≤100）；其他类型存 {}',
    updated_at       DATETIME(3)   NOT NULL COMMENT '最后更新时间（UTC，毫秒精度）',
    PRIMARY KEY (role_id, resource),
    KEY idx_iam_rds_scope_type (scope_type) COMMENT '按范围类型审计与越权排查',
    CONSTRAINT fk_iam_rds_role FOREIGN KEY (role_id) REFERENCES iam_role (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='角色数据范围，与功能权限正交；授予 data_bearing 权限时必填';

CREATE TABLE IF NOT EXISTS iam_change_request (
    id               BIGINT        NOT NULL                   COMMENT '审批单主键，雪花 ID',
    channel          VARCHAR(16)   NOT NULL                   COMMENT '发起端别：admin / biz',
    action           VARCHAR(64)   NOT NULL                   COMMENT '申请动作：role_create / role_permission_replace / binding_create / breakglass 等',
    target_type      VARCHAR(32)   NOT NULL                   COMMENT '目标类型：iam_role / iam_binding / iam_breakglass',
    target_id        VARCHAR(64)   NOT NULL                   COMMENT '目标ID；新建类动作在应用前存业务临时ID或空串',
    payload          JSON          NOT NULL                   COMMENT '申请载荷；应用前须重新校验，不得直接信任',
    status           VARCHAR(16)   NOT NULL DEFAULT 'pending' COMMENT '状态机：pending=待审批 / approved=已通过 / rejected=已拒绝 / applied=已应用 / cancelled=已撤销',
    reason           VARCHAR(512)  NOT NULL DEFAULT ''        COMMENT '申请原因；iam.security.require_reason=true 时必填',
    requested_by     VARCHAR(64)   NOT NULL                   COMMENT '申请人 subject_id',
    requested_at     DATETIME(3)   NOT NULL                   COMMENT '申请时间（UTC，毫秒精度）',
    approved_by      VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '审批人 subject_id，必须与申请人不同（职责分离）',
    approved_at      DATETIME(3)   NULL                       COMMENT '审批时间（UTC）',
    applied_at       DATETIME(3)   NULL                       COMMENT '实际应用时间（UTC）',
    version          BIGINT        NOT NULL DEFAULT 1         COMMENT '乐观锁版本，防并发重复审批',
    PRIMARY KEY (id),
    KEY idx_iam_change_status (channel, status, requested_at) COMMENT '待办列表与超时审批扫描'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 高风险变更审批单；申请人与审批人强制不同，审批通过后才落到目标表';

CREATE TABLE IF NOT EXISTS iam_outbox (
    id               BIGINT        NOT NULL                   COMMENT '事件主键，雪花 ID；同时用作投递顺序依据',
    event_type       VARCHAR(64)   NOT NULL                   COMMENT '事件类型：audit.event / cache.invalidate 等',
    aggregate_type   VARCHAR(32)   NOT NULL                   COMMENT '聚合类型：iam_role / iam_binding',
    aggregate_id     VARCHAR(64)   NOT NULL                   COMMENT '聚合ID，用于缓存失效与去重',
    payload          JSON          NOT NULL                   COMMENT '事件载荷；禁止含 Token、密钥、完整 PII',
    status           VARCHAR(16)   NOT NULL DEFAULT 'pending' COMMENT '投递状态：pending=待投递 / processing=投递中 / succeeded=成功 / failed=失败 / dead=进死信',
    attempts         INT           NOT NULL DEFAULT 0         COMMENT '已投递次数，指数退避依据',
    next_retry_at    DATETIME(3)   NULL                       COMMENT '下次可投递时间（UTC）；NULL 表示立即可投递',
    created_at       DATETIME(3)   NOT NULL                   COMMENT '创建时间（UTC），与业务写入同事务',
    updated_at       DATETIME(3)   NOT NULL                   COMMENT '状态最后更新时间（UTC）',
    PRIMARY KEY (id),
    KEY idx_iam_outbox_pending (status, next_retry_at) COMMENT 'worker 拉取待投递事件，按状态 + 重试时间扫描',
    KEY idx_iam_outbox_aggregate (aggregate_type, aggregate_id) COMMENT '按聚合排查投递情况与缓存失效是否完成'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 事务性 Outbox：审计事件与缓存失效事件可靠投递，禁止绕过直接写日志冒充审计';
