-- IAM（RBAC 权限管理）模块基线：权限目录、菜单、角色、模板、授权绑定、数据范围、
-- 变更审批、静态职责分离（SOD）与前置角色。
-- 本文件由增量迁移按领域合并而成（原 000002 / 000005 / 000006）：
-- 角色继承（parent_id）与 RBAC2 约束（互斥组 / 前置角色 / 角色基数）已直接体现在基线结构中。
-- 数据库基线：MySQL 8.0.19+（InnoDB / utf8mb4_0900_ai_ci）。
-- 设计要点：
--   1. 功能权限（iam_role_permission）与数据范围（iam_role_data_scope）正交；
--   2. 角色扁平、无继承，通过一个主体绑定多个角色表达组合；RBAC1 单亲继承见 parent_id；
--   3. 时间列统一 DATETIME(3) + 应用写入 UTC，避免 TIMESTAMP 的 2038 上限与隐式时区转换；
--   4. JSON 列使用原生 JSON 类型，由数据库校验合法性；
--   5. 外键用于阻止悬空授权、跨端 / 跨商户绑定与误删（RESTRICT），业务删除仍以软删为主；
--   6. 审批单含 merchant_id 商户维度：空串=平台级（A 端），非空=商户级（B 端）；
--   7. 审计 / 缓存失效事件写平台 async_outbox（见 000004），IAM 不再维护自有 outbox 表；
--   8. 企业级加固（2026-10-26）：
--      - 枚举、租户组合与 JSON 载荷结构由 CHECK 约束在库层拒绝，不依赖单点应用校验；
--      - 绑定撤销改为软撤销（revoked_at/revoked_by），active_key 生成列保证「同一主体同一角色
--        至多一条生效绑定」，同时在库内保留完整授权历史，供访问复核；
--      - 角色模板（iam_role_template）支持「平台定义、商户实例化」，并以 template_version
--        记录实例基线，避免商户规模增长后角色定义与授权配置重复膨胀；
--      - 权限目录收敛为平铺表（菜单 / 路由树由 iam_menu 承载），resource 由权限码首段生成；
--      - 审批单补充时效、决策人、决策说明与载荷版本，并以 pending_key 生成列阻止同一目标并发重复提单。
--   9. 菜单 / 路由资源（iam_menu）：
--      - 菜单按端别隔离，A 端（admin）与 B 端（biz）的菜单树互不合并；
--      - (channel, code) 与 (channel, route_name) 唯一，避免 vue-router 同名路由互相覆盖；
--      - 自引用外键保证父子节点同端，避免跨端挂载；
--      - 种子维护基线，运营通过管理接口调整展示字段；version 提供乐观锁，
--        避免并发编辑同一条菜单时后写覆盖先写。
--  10. RBAC1 / RBAC2：
--      - parent_id 为「同端别 + 同商户」内的单亲角色树，复合自引用外键拒绝跨端 / 跨商户继承；
--      - iam_sod_group 表达同租户内互斥角色集，iam_role_prerequisite 表达前置角色；
--      - max_bindings 为单角色最大有效绑定数，NULL 表示不限。

CREATE TABLE IF NOT EXISTS iam_permission (
    code             VARCHAR(128)  NOT NULL                  COMMENT '权限点稳定标识，resource:action 或 domain:resource:action，全小写；发布后不可改名或复用',
    name             VARCHAR(128)  NOT NULL                  COMMENT '权限点显示名，用于授权勾选',
    channel          VARCHAR(16)   NOT NULL                  COMMENT '可授予端别：admin=仅A端 / biz=仅B端 / both=两端均可',
    resource         VARCHAR(64)   GENERATED ALWAYS AS (SUBSTRING_INDEX(code, ':', 1)) STORED
                                                             COMMENT '资源段，由权限码首段派生；供数据范围匹配与 SQL 侧分组 / 联表',
    data_bearing     TINYINT       NOT NULL DEFAULT 0        COMMENT '是否涉及数据范围：1=是（授权时必须配置 iam_role_data_scope）/ 0=否',
    sort             INT           NOT NULL DEFAULT 0        COMMENT '排序值，升序，值越小越靠前',
    status           VARCHAR(16)   NOT NULL DEFAULT 'active' COMMENT '状态：active=可用 / deprecated=已弃用（保留授权但不可新授）',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '创建时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)   NOT NULL                  COMMENT '最后更新时间（UTC，毫秒精度），由应用写入',
    PRIMARY KEY (code),
    KEY idx_iam_permission_channel_status (channel, status, sort) COMMENT '按端别 + 状态过滤并按排序输出',
    KEY idx_iam_permission_resource (resource) COMMENT '按资源段分组 / 联表查询',
    CONSTRAINT ck_iam_permission_channel CHECK (channel IN ('admin', 'biz', 'both')),
    CONSTRAINT ck_iam_permission_status CHECK (status IN ('active', 'deprecated')),
    CONSTRAINT ck_iam_permission_data_bearing CHECK (data_bearing IN (0, 1))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 权限点目录，代码所有、库内只读同步；不存放角色授权关系';

CREATE TABLE IF NOT EXISTS iam_menu (
    id              BIGINT       NOT NULL                  COMMENT '菜单主键，雪花 ID 或种子保留段固定值',
    channel         VARCHAR(16)  NOT NULL                  COMMENT '归属端别：admin=web-admin / biz=web-biz；两端菜单不合并',
    code            VARCHAR(128) NOT NULL                  COMMENT '菜单稳定标识，如 system.role；父子引用与种子幂等键',
    parent_code     VARCHAR(128) NULL DEFAULT NULL         COMMENT '父菜单 code；根节点为 NULL',
    node_type       VARCHAR(16)  NOT NULL DEFAULT 'menu'   COMMENT 'catalog=目录 / menu=页面 / iframe=内嵌 / link=外链',
    route_name      VARCHAR(64)  NOT NULL                  COMMENT '路由 name，PascalCase，端内唯一；keep-alive 与标签页依赖',
    route_path      VARCHAR(256) NOT NULL                  COMMENT '路由 URL：目录为 /system，子页可为相对段 role',
    component       VARCHAR(256) NOT NULL DEFAULT ''       COMMENT 'BasicLayout / IFrameView / /system/role/index；catalog 与 link 为空',
    redirect        VARCHAR(256) NOT NULL DEFAULT ''       COMMENT '目录默认跳转，catalog 必填',
    title           VARCHAR(128) NOT NULL                  COMMENT '显示名；i18n 缺失时兜底',
    title_key       VARCHAR(128) NOT NULL DEFAULT ''       COMMENT 'i18n key，如 page.system.role；非空时前端优先',
    icon            VARCHAR(64)  NOT NULL DEFAULT ''       COMMENT '图标注册表 key，如 lucide:settings',
    permission_code VARCHAR(128) NULL DEFAULT NULL         COMMENT '可见所需权限点；NULL=登录即可见',
    sort            INT          NOT NULL DEFAULT 0        COMMENT '同级升序，映射 meta.order',
    hide_in_menu    TINYINT      NOT NULL DEFAULT 0        COMMENT '1=路由可达但不进侧边栏',
    keep_alive      TINYINT      NOT NULL DEFAULT 0        COMMENT '1=页面缓存',
    external_url    VARCHAR(512) NOT NULL DEFAULT ''       COMMENT 'iframe / link 目标地址',
    status          VARCHAR(16)  NOT NULL DEFAULT 'active' COMMENT 'active / disabled',
    version         BIGINT       NOT NULL DEFAULT 1        COMMENT '乐观锁版本；每次写入自增',
    updated_by      VARCHAR(64)  NOT NULL                  COMMENT '最后维护人；种子写入 seed',
    created_at      DATETIME(3)  NOT NULL                  COMMENT '创建时间（UTC）',
    updated_at      DATETIME(3)  NOT NULL                  COMMENT '更新时间（UTC）',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_menu_code (channel, code) COMMENT '端内菜单编码唯一',
    UNIQUE KEY uk_iam_menu_route_name (channel, route_name) COMMENT 'vue-router 同名路由会互相覆盖',
    KEY idx_iam_menu_tree (channel, status, parent_code, sort) COMMENT '菜单树按端别 + 状态 + 父节点排序输出',
    KEY idx_iam_menu_permission (permission_code) COMMENT '按权限点反查受影响菜单',
    CONSTRAINT fk_iam_menu_parent FOREIGN KEY (channel, parent_code) REFERENCES iam_menu (channel, code)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_iam_menu_permission FOREIGN KEY (permission_code) REFERENCES iam_permission (code)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_menu_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_menu_node_type CHECK (node_type IN ('catalog', 'menu', 'iframe', 'link')),
    CONSTRAINT ck_iam_menu_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT ck_iam_menu_flags CHECK (hide_in_menu IN (0, 1) AND keep_alive IN (0, 1)),
    CONSTRAINT ck_iam_menu_parent_self CHECK (parent_code IS NULL OR parent_code <> code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='前端菜单 / 路由资源；可见性由权限码推导，不存放授权关系';

CREATE TABLE IF NOT EXISTS iam_role (
    id               BIGINT        NOT NULL                  COMMENT '角色主键，雪花 ID（pkg/id 生成），禁止自增',
    channel          VARCHAR(16)   NOT NULL                  COMMENT '角色所属端别：admin=A端平台 / biz=B端商户',
    scope_type       VARCHAR(16)   NOT NULL                  COMMENT '角色作用域：platform=A端全局 / merchant=B端单商户',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT ''       COMMENT 'B端所属商户ID；scope_type=platform 时为空串，禁止用空串代表“全部商户”',
    parent_id        BIGINT        NULL                      COMMENT '父角色ID；NULL=根角色（无继承）；父角色须同端别、同商户',
    max_bindings     INT           NULL                      COMMENT '单角色最大有效绑定数（RBAC2 基数约束）；NULL=不限',
    code             VARCHAR(64)   NOT NULL                  COMMENT '角色编码，端内（同商户）唯一；软删后不可复用，防止重建洗白',
    name             VARCHAR(128)  NOT NULL                  COMMENT '角色显示名',
    description      VARCHAR(512)  NOT NULL DEFAULT ''       COMMENT '角色说明，建议写清职责边界',
    role_type        VARCHAR(16)   NOT NULL DEFAULT 'custom' COMMENT '角色类型：system=系统内置 / custom=自定义 / breakglass=破玻璃，仅此类型可在审批有效期内展开 *',
    status           VARCHAR(16)   NOT NULL DEFAULT 'active' COMMENT '状态：active=生效 / disabled=停用（停用后快照不再加载其权限）',
    template_id      BIGINT        NOT NULL DEFAULT 0        COMMENT '来源角色模板 ID；0=非模板实例（手工创建或系统种子）',
    template_version BIGINT        NOT NULL DEFAULT 0        COMMENT '实例化时模板版本；与模板当前 version 不一致表示存在漂移，供同步任务比对',
    version          BIGINT        NOT NULL DEFAULT 1        COMMENT '乐观锁版本；更新须 WHERE version=?，冲突返回 40900',
    created_by       VARCHAR(64)   NOT NULL                  COMMENT '创建人 subject_id',
    updated_by       VARCHAR(64)   NOT NULL                  COMMENT '最后更新人 subject_id',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '创建时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)   NOT NULL                  COMMENT '最后更新时间（UTC，毫秒精度）',
    deleted_at       DATETIME(3)   NULL                      COMMENT '软删时间（UTC）；NULL=未删除，非空后 code 不可复用',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_role_scope_code (channel, merchant_id, code) COMMENT '端内角色编码唯一，绑定与授权的幂等防线',
    UNIQUE KEY uk_iam_role_id_channel (id, channel) COMMENT '供数据范围 / 绑定复合外键引用，库层防止跨端引用',
    UNIQUE KEY uk_iam_role_id_scope (id, channel, merchant_id) COMMENT '供绑定 / 继承 / RBAC2 复合外键引用，库层防止跨商户绑定',
    KEY idx_iam_role_merchant_status (merchant_id, status) COMMENT 'B端按商户 + 状态列角色',
    KEY idx_iam_role_channel_scope (channel, scope_type, status) COMMENT 'A端按端别 + 作用域列角色',
    KEY idx_iam_role_template (template_id, template_version) COMMENT '模板漂移扫描：按模板聚合实例并比对基线版本',
    KEY idx_iam_role_parent (parent_id, channel, merchant_id) COMMENT '按父角色查子树，同时作为复合外键的索引',
    CONSTRAINT fk_iam_role_parent FOREIGN KEY (parent_id, channel, merchant_id)
        REFERENCES iam_role (id, channel, merchant_id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_role_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_role_scope_type CHECK (scope_type IN ('platform', 'merchant')),
    CONSTRAINT ck_iam_role_tenant CHECK (
        (scope_type = 'platform' AND channel = 'admin' AND merchant_id = '')
     OR (scope_type = 'merchant' AND channel = 'biz' AND merchant_id <> '')
    ),
    CONSTRAINT ck_iam_role_type CHECK (role_type IN ('system', 'custom', 'breakglass')),
    CONSTRAINT ck_iam_role_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT ck_iam_role_version CHECK (version >= 1),
    CONSTRAINT ck_iam_role_max_bindings CHECK (max_bindings IS NULL OR max_bindings >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 角色定义，按端别与商户作用域隔离；支持同租户内单亲继承，不参与破玻璃继承';

CREATE TABLE IF NOT EXISTS iam_role_template (
    id               BIGINT        NOT NULL                  COMMENT '模板主键，雪花 ID（固定种子可用保留段）',
    channel          VARCHAR(16)   NOT NULL                  COMMENT '适用端别：admin=A端模板 / biz=B端模板',
    code             VARCHAR(64)   NOT NULL                  COMMENT '模板编码，端内唯一；软删后不可复用',
    name             VARCHAR(128)  NOT NULL                  COMMENT '模板显示名',
    description      VARCHAR(512)  NOT NULL DEFAULT ''       COMMENT '模板说明与适用场景',
    status           VARCHAR(16)   NOT NULL DEFAULT 'active' COMMENT '状态：active=可实例化 / disabled=停止新实例化（既有实例保留）',
    version          BIGINT        NOT NULL DEFAULT 1        COMMENT '模板版本；权限或数据范围变更时递增，用于识别实例漂移',
    created_by       VARCHAR(64)   NOT NULL                  COMMENT '创建人 subject_id',
    updated_by       VARCHAR(64)   NOT NULL                  COMMENT '最后更新人 subject_id',
    created_at       DATETIME(3)   NOT NULL                  COMMENT '创建时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)   NOT NULL                  COMMENT '最后更新时间（UTC，毫秒精度）',
    deleted_at       DATETIME(3)   NULL                      COMMENT '软删时间（UTC）；NULL=未删除',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_role_template_code (channel, code) COMMENT '端内模板编码唯一',
    KEY idx_iam_role_template_status (channel, status),
    CONSTRAINT ck_iam_role_template_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_role_template_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT ck_iam_role_template_version CHECK (version >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='角色模板：平台定义权限与数据范围基线，B端按商户实例化，避免逐商户重复维护';

CREATE TABLE IF NOT EXISTS iam_role_template_permission (
    template_id      BIGINT        NOT NULL COMMENT '模板ID，关联 iam_role_template.id',
    permission_code  VARCHAR(128)  NOT NULL COMMENT '权限点编码，关联 iam_permission.code；仅 active 状态可写入',
    granted_by       VARCHAR(64)   NOT NULL COMMENT '最近一次写入人 subject_id',
    created_at       DATETIME(3)   NOT NULL COMMENT '写入时间（UTC，毫秒精度）',
    PRIMARY KEY (template_id, permission_code),
    KEY idx_iam_rtp_permission (permission_code) COMMENT '反查权限点被哪些模板引用，用于影响面评估',
    CONSTRAINT fk_iam_rtp_template FOREIGN KEY (template_id) REFERENCES iam_role_template (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_iam_rtp_permission FOREIGN KEY (permission_code) REFERENCES iam_permission (code)
        ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='角色模板权限基线；实例化与同步时复制到 iam_role_permission';

CREATE TABLE IF NOT EXISTS iam_role_template_data_scope (
    template_id      BIGINT        NOT NULL COMMENT '模板ID，关联 iam_role_template.id',
    resource         VARCHAR(64)   NOT NULL COMMENT '资源段：* 表示全部资源，或权限点首段如 order / file',
    scope_type       VARCHAR(16)   NOT NULL COMMENT '范围类型：all / merchant / self / merchant_set；须与模板 channel 相容',
    scope_values     JSON          NOT NULL COMMENT '范围取值：merchant_set 存 {"merchant_ids":[...]}（≤100）；其他类型存 {}',
    granted_by       VARCHAR(64)   NOT NULL COMMENT '最近一次写入人 subject_id',
    updated_at       DATETIME(3)   NOT NULL COMMENT '最后更新时间（UTC，毫秒精度）',
    PRIMARY KEY (template_id, resource),
    KEY idx_iam_rtds_scope_type (scope_type),
    CONSTRAINT fk_iam_rtds_template FOREIGN KEY (template_id) REFERENCES iam_role_template (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_rtds_scope_type CHECK (scope_type IN ('all', 'merchant', 'self', 'merchant_set')),
    CONSTRAINT ck_iam_rtds_scope_values CHECK (
        (scope_type = 'merchant_set'
            AND JSON_LENGTH(scope_values, '$.merchant_ids') BETWEEN 1 AND 100)
     OR (scope_type <> 'merchant_set' AND JSON_LENGTH(scope_values) = 0)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='角色模板数据范围基线；与模板权限正交';

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
    revoked_at       DATETIME(3)   NULL                COMMENT '解绑时间（UTC）；NULL=生效；非空后该行仅用于历史与访问复核',
    revoked_by       VARCHAR(64)   NOT NULL DEFAULT '' COMMENT '解绑操作人 subject_id；未解绑时为空串',
    revoke_reason    VARCHAR(512)  NOT NULL DEFAULT '' COMMENT '解绑原因；强制填写策略由应用配置决定',
    active_key       BIGINT        GENERATED ALWAYS AS (CASE WHEN revoked_at IS NULL THEN 1 ELSE id END) STORED
                                                   COMMENT '生效绑定的唯一哨兵：生效行恒为 1，撤销行取自身 id，使同一主体同一角色至多一条生效绑定且保留全部历史',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_binding (channel, subject_type, subject_id, merchant_id, role_id, active_key)
        COMMENT '同一主体对同一角色的生效绑定幂等；撤销后重新授权会新增一行而不是复活旧行',
    KEY idx_iam_binding_subject (channel, subject_type, subject_id, merchant_id, revoked_at)
        COMMENT '快照回源主查询路径：按主体取全部生效绑定',
    KEY idx_iam_binding_expiry (expires_at)
        COMMENT '过期绑定清理扫描：避免全表扫描',
    CONSTRAINT fk_iam_binding_role_scope FOREIGN KEY (role_id, channel, merchant_id)
        REFERENCES iam_role (id, channel, merchant_id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_binding_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_binding_subject_type CHECK (subject_type IN ('staff', 'merchant_staff')),
    CONSTRAINT ck_iam_binding_tenant CHECK (
        (channel = 'admin' AND subject_type = 'staff' AND merchant_id = '')
     OR (channel = 'biz' AND subject_type = 'merchant_staff' AND merchant_id <> '')
    ),
    CONSTRAINT ck_iam_binding_revoke CHECK (
        (revoked_at IS NULL AND revoked_by = '')
     OR (revoked_at IS NOT NULL AND revoked_by <> '')
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 主体-角色绑定，支持限时有效与软撤销；鉴权快照的唯一绑定来源';

CREATE TABLE IF NOT EXISTS iam_role_data_scope (
    role_id          BIGINT        NOT NULL COMMENT '角色ID，关联 iam_role.id',
    channel          VARCHAR(16)   NOT NULL COMMENT '角色端别冗余列；由复合外键锁定与 iam_role 一致，用于库层拒绝 B 端越权范围',
    resource         VARCHAR(64)   NOT NULL COMMENT '资源段：* 表示全部资源，或权限点首段如 order / file',
    scope_type       VARCHAR(16)   NOT NULL COMMENT '范围类型：all=全部 / merchant=当前商户 / self=本人 / merchant_set=指定商户集合；B端仅允许 merchant、self',
    scope_values     JSON          NOT NULL COMMENT '范围取值：merchant_set 存 {"merchant_ids":[...]}（≤100）；其他类型存 {}',
    granted_by       VARCHAR(64)   NOT NULL COMMENT '最近一次配置人 subject_id',
    created_at       DATETIME(3)   NOT NULL COMMENT '首次配置时间（UTC，毫秒精度）',
    updated_at       DATETIME(3)   NOT NULL COMMENT '最后更新时间（UTC，毫秒精度）',
    PRIMARY KEY (role_id, resource),
    KEY idx_iam_rds_scope_type (scope_type) COMMENT '按范围类型审计与越权排查',
    CONSTRAINT fk_iam_rds_role_channel FOREIGN KEY (role_id, channel) REFERENCES iam_role (id, channel)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_rds_scope_type CHECK (scope_type IN ('all', 'merchant', 'self', 'merchant_set')),
    CONSTRAINT ck_iam_rds_channel_scope CHECK (channel = 'admin' OR scope_type IN ('merchant', 'self')),
    CONSTRAINT ck_iam_rds_scope_values CHECK (
        (scope_type = 'merchant_set'
            AND JSON_LENGTH(scope_values, '$.merchant_ids') BETWEEN 1 AND 100)
     OR (scope_type <> 'merchant_set' AND JSON_LENGTH(scope_values) = 0)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='角色数据范围，与功能权限正交；授予 data_bearing 权限时必填';

CREATE TABLE IF NOT EXISTS iam_change_request (
    id               BIGINT        NOT NULL                   COMMENT '审批单主键，雪花 ID',
    channel          VARCHAR(16)   NOT NULL                   COMMENT '发起端别：admin / biz',
    merchant_id      VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '商户ID；空串表示平台级（A 端），非空表示商户级（B 端）',
    action           VARCHAR(64)   NOT NULL                   COMMENT '申请动作：role_create / role_permission_replace / binding_create / breakglass 等',
    target_type      VARCHAR(32)   NOT NULL                   COMMENT '目标类型：iam_role / iam_binding / iam_breakglass',
    target_id        VARCHAR(64)   NOT NULL                   COMMENT '目标ID；新建类动作在应用前存业务临时ID或空串',
    payload          JSON          NOT NULL                   COMMENT '申请载荷；应用前须重新校验，不得直接信任',
    payload_version  INT           NOT NULL DEFAULT 1         COMMENT '载荷结构版本；载荷语义变更时递增，供审批人识别与回溯',
    status           VARCHAR(16)   NOT NULL DEFAULT 'pending' COMMENT '状态机：pending=待审批 / approved=已通过 / rejected=已拒绝 / applied=已应用 / cancelled=已撤销 / expired=已超时',
    reason           VARCHAR(512)  NOT NULL DEFAULT ''        COMMENT '申请原因；iam.security.require_reason=true 时必填',
    requested_by     VARCHAR(64)   NOT NULL                   COMMENT '申请人 subject_id',
    requested_at     DATETIME(3)   NOT NULL                   COMMENT '申请时间（UTC，毫秒精度）',
    expires_at       DATETIME(3)   NULL                       COMMENT '审批时效截止（UTC）；NULL=按配置计算，超时后不得再通过',
    approved_by      VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '审批人 subject_id，必须与申请人不同（职责分离）',
    approved_at      DATETIME(3)   NULL                       COMMENT '审批时间（UTC）',
    decision_reason  VARCHAR(512)  NOT NULL DEFAULT ''        COMMENT '审批结论说明；拒绝时必填，通过时可选',
    applied_by       VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '执行应用的操作人 subject_id；系统执行时写 system',
    applied_at       DATETIME(3)   NULL                       COMMENT '实际应用时间（UTC）',
    version          BIGINT        NOT NULL DEFAULT 1         COMMENT '乐观锁版本，防并发重复审批',
    pending_key      VARCHAR(255)  GENERATED ALWAYS AS (
        CASE WHEN status = 'pending' AND target_id <> ''
             THEN CONCAT(channel, '|', merchant_id, '|', action, '|', target_id)
        END
    ) STORED COMMENT '待审批幂等键：同一端别 / 商户 / 目标至多一张待审批单，终态行取 NULL 不参与唯一约束',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_change_pending (pending_key)
        COMMENT '阻止并发重复提单；新建类动作 target_id 为空时不参与（应用层按载荷幂等）',
    KEY idx_iam_change_status (channel, status, requested_at) COMMENT '待办列表与超时审批扫描',
    KEY idx_iam_change_expiry (status, expires_at) COMMENT '超时审批扫描：仅扫描 pending 且已过期的单据',
    KEY idx_iam_change_merchant_status (merchant_id, channel, status, requested_at) COMMENT 'B 端待办列表：按商户 + 端别 + 状态 + 申请时间扫描',
    CONSTRAINT ck_iam_change_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_change_status CHECK (status IN ('pending', 'approved', 'rejected', 'applied', 'cancelled', 'expired')),
    CONSTRAINT ck_iam_change_tenant CHECK (
        (channel = 'admin' AND merchant_id = '')
     OR (channel = 'biz' AND merchant_id <> '')
    ),
    CONSTRAINT ck_iam_change_version CHECK (version >= 1),
    CONSTRAINT ck_iam_change_payload CHECK (JSON_TYPE(payload) = 'OBJECT')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='IAM 高风险变更审批单；申请人与审批人强制不同，审批通过后才落到目标表';

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
