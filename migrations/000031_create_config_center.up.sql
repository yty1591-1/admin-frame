-- 000031 (up): 配置中心 M1 数据模型（config-item-plan §附录 A / ADR-0013）。
--
-- 设计要点：
--   1. config_item 是配置键的元数据与当前版本指针；值落在 config_item_version，
--      版本不可变（只追加），current_version 由发布流程推进。
--   2. sensitive=1 的项不得在本表族存值，必须走 config_secret 子域（M2）；
--      value_text 仅承载非密钥配置。
--   3. config_release 是不可变发布快照头，config_release_item 固定键到版本；
--      config_release_pointer 以 (env, namespace) 为粒度做 CAS 切换，回滚即指回旧 release。
--   4. 所有主键为雪花 ID，禁止自增；时间列统一 DATETIME(3)，排序 / 比较均按 UTC。
--   5. 契约冻结：列名、唯一键与索引不得偏离 docs/config-center-plan.md 附录 A；
--      本迁移的列注释与表注释即字段字典，供 DATA_STANDARDS 数据字典与排障使用。

CREATE TABLE IF NOT EXISTS config_item (
    id              BIGINT       NOT NULL COMMENT '主键：雪花 ID（分布式生成，禁止自增）',
    env             VARCHAR(16)  NOT NULL COMMENT '环境枚举：local/dev/test/staging/prod；未登记环境拒绝写入',
    namespace       VARCHAR(64)  NOT NULL COMMENT '命名空间：小写字母开头的短横线/下划线标识（如 platform、identity）',
    `key`           VARCHAR(128) NOT NULL COMMENT '配置键：小写点分形式（如 http.timeout），同 env+namespace 内唯一',
    value_type      VARCHAR(16)  NOT NULL COMMENT '值类型：string/int/bool/duration/json，决定业务侧解码方式',
    description     VARCHAR(512) NOT NULL DEFAULT '' COMMENT '用途说明（≤512 字节），必填语义由应用层校验',
    owner           VARCHAR(64)  NOT NULL COMMENT '负责人或团队标识；用于治理与变更通知',
    status          VARCHAR(16)  NOT NULL DEFAULT 'draft' COMMENT '治理状态：draft/active/deprecated/archived；archived 为终态不可复活',
    current_version BIGINT       NOT NULL DEFAULT 0 COMMENT '当前版本号，指向 config_item_version.version；0=尚未写入任何值',
    version         BIGINT       NOT NULL DEFAULT 1 COMMENT '元数据乐观锁：更新 CAS expectedVersion，发布推进 current_version 时同步 +1',
    sensitive       TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '1=敏感项标记；敏感值不得写入本表族，必须走 config_secret 子域（M2）',
    validation_json JSON         NULL COMMENT '值校验规则 JSON（如 min/max/enum/pattern）；NULL=不做值校验',
    expires_at      DATETIME(3)  NULL COMMENT '到期时间（UTC）；NULL=长期有效，非 NULL 由治理任务扫描',
    created_by      VARCHAR(64)  NOT NULL COMMENT '创建主体：操作者 ID 或系统标识',
    updated_by      VARCHAR(64)  NOT NULL COMMENT '最近更新主体：操作者 ID 或系统标识',
    created_at      DATETIME(3)  NOT NULL COMMENT '创建时间（UTC，毫秒精度）',
    updated_at      DATETIME(3)  NOT NULL COMMENT '更新时间（UTC，毫秒精度）',
    PRIMARY KEY (id),
    UNIQUE KEY uk_config_item (env, namespace, `key`),
    KEY idx_config_item_status (env, namespace, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='配置键元数据与当前版本指针（M1）；值存于 config_item_version，敏感项值走 config_secret';

CREATE TABLE IF NOT EXISTS config_item_version (
    id             BIGINT       NOT NULL COMMENT '主键：雪花 ID（版本行不可变，只追加）',
    item_id        BIGINT       NOT NULL COMMENT '所属配置键 ID（config_item.id）',
    version        BIGINT       NOT NULL COMMENT '版本号：同 item 内单调递增且永不修改；与 item_id 组成唯一键',
    value_text     MEDIUMTEXT   NOT NULL COMMENT '非密钥配置值（原文）；sensitive=1 的值禁止写入本列，必须走 config_secret',
    value_hash     CHAR(64)     NOT NULL COMMENT '值摘要：SHA-256(value_text) 小写十六进制；发布快照与客户端校验一致性',
    change_summary VARCHAR(512) NOT NULL DEFAULT '' COMMENT '变更摘要（≤512 字节）：说明本次修改原因与影响',
    created_by     VARCHAR(64)  NOT NULL COMMENT '写入主体：操作者 ID 或系统标识',
    created_at     DATETIME(3)  NOT NULL COMMENT '写入时间（UTC，毫秒精度）；版本排序依据',
    PRIMARY KEY (id),
    UNIQUE KEY uk_config_item_version (item_id, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='配置键版本快照（只追加、不可变）；发布快照按 item_id+version 固定引用';

CREATE TABLE IF NOT EXISTS config_release (
    id                  BIGINT       NOT NULL COMMENT '主键：雪花 ID',
    env                 VARCHAR(16)  NOT NULL COMMENT '发布作用域环境：local/dev/test/staging/prod',
    namespace           VARCHAR(64)  NOT NULL COMMENT '发布作用域命名空间，与 config_item.namespace 对齐',
    release_no          BIGINT       NOT NULL COMMENT '发布号：同 env+namespace 内单调递增（唯一键兜底并发）',
    name                VARCHAR(128) NOT NULL COMMENT '发布名称（≤128 字节），如「2026-11 限流阈值调整」',
    description         VARCHAR(512) NOT NULL DEFAULT '' COMMENT '发布说明：变更范围、风险与回滚预案摘要',
    status              VARCHAR(16)  NOT NULL COMMENT '状态机：draft/pending_review/approved/active/superseded/rolled_back/rejected/cancelled',
    strategy            VARCHAR(16)  NOT NULL DEFAULT 'all' COMMENT '灰度策略：all/percentage/tenant_list/channel_list/instance_list',
    target_rule_json    JSON         NULL COMMENT '灰度目标规则 JSON（百分比/租户/渠道/实例白名单）；strategy!=all 时必填',
    snapshot_hash       CHAR(64)     NOT NULL COMMENT '发布快照摘要：快照条目聚合 SHA-256（小写十六进制），用于一致性与防篡改校验',
    snapshot_size       INT          NOT NULL DEFAULT 0 COMMENT '快照条目数（config_release_item 行数冗余，便于审计展示）',
    snapshot_signature  VARCHAR(256) NOT NULL DEFAULT '' COMMENT 'Ed25519 快照签名（base64）；客户端离线校验，M1-5 生成',
    snapshot_key_id     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '签名密钥版本标识；配合公钥轮换定位验签公钥',
    based_on_release_id BIGINT       NULL COMMENT '基线发布 ID（config_release.id）；NULL=无基线（首次或从当前态构建）',
    created_by          VARCHAR(64)  NOT NULL COMMENT '创建主体：操作者 ID',
    submitted_by        VARCHAR(64)  NULL COMMENT '提交送审主体；NULL=尚未提交审批',
    approved_by         VARCHAR(64)  NULL COMMENT '审批通过主体（须异于 submitted_by）；NULL=未审批',
    approved_at         DATETIME(3)  NULL COMMENT '审批通过时间（UTC）；NULL=未审批',
    published_at        DATETIME(3)  NULL COMMENT '生效时间（UTC）；NULL=尚未通过指针切换生效',
    created_at          DATETIME(3)  NOT NULL COMMENT '创建时间（UTC，毫秒精度）',
    updated_at          DATETIME(3)  NOT NULL COMMENT '更新时间（UTC，毫秒精度），状态迁移时刷新',
    PRIMARY KEY (id),
    UNIQUE KEY uk_config_release_no (env, namespace, release_no),
    KEY idx_config_release_status (env, namespace, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='配置发布快照头（不可变内容 + 审批状态机）；生效与回滚只改 config_release_pointer';

CREATE TABLE IF NOT EXISTS config_release_item (
    release_id   BIGINT       NOT NULL COMMENT '发布 ID（config_release.id）',
    item_id      BIGINT       NOT NULL COMMENT '配置键 ID（config_item.id）；与 release_id 组成主键，一键一条',
    item_version BIGINT       NOT NULL COMMENT '固定的配置键版本号（config_item_version.version），快照不可变',
    config_key   VARCHAR(128) NOT NULL COMMENT '配置键冗余快照（小写点分），供离线渲染与审计免联表',
    value_hash   CHAR(64)     NOT NULL COMMENT '该版本值摘要 SHA-256，发布后校验值未被篡改',
    PRIMARY KEY (release_id, item_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='发布快照条目：键到版本的固定映射（不可变）；回滚即切换指针指向旧 release';

CREATE TABLE IF NOT EXISTS config_release_pointer (
    env               VARCHAR(16) NOT NULL COMMENT '指针作用域环境：local/dev/test/staging/prod',
    namespace         VARCHAR(64) NOT NULL COMMENT '指针作用域命名空间；与 env 组成主键',
    active_release_id BIGINT      NOT NULL COMMENT '当前生效发布 ID（config_release.id）；回滚即指回历史 release',
    version           BIGINT      NOT NULL DEFAULT 1 COMMENT 'CAS 版本：切换成功 +1；与 expectedVersion 不符返回指针冲突（409）',
    updated_at        DATETIME(3) NOT NULL COMMENT '最近切换时间（UTC，毫秒精度）',
    PRIMARY KEY (env, namespace)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='当前生效发布指针（env+namespace 粒度）；发布/回滚的原子切换点（CAS）';

CREATE TABLE IF NOT EXISTS config_release_review (
    id         BIGINT       NOT NULL COMMENT '主键：雪花 ID（审批记录只追加）',
    release_id BIGINT       NOT NULL COMMENT '发布 ID（config_release.id）',
    reviewer   VARCHAR(64)  NOT NULL COMMENT '审批人主体；生产环境提交人不得自审（应用层保证）',
    decision   VARCHAR(16)  NOT NULL COMMENT '审批结论：approved/rejected',
    comment    VARCHAR(512) NOT NULL DEFAULT '' COMMENT '审批意见（≤512 字节），拒绝时须写明原因',
    created_at DATETIME(3)  NOT NULL COMMENT '审批时间（UTC，毫秒精度）',
    PRIMARY KEY (id),
    KEY idx_config_review_release (release_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='发布审批记录（追加写）；支撑双人复核与职责分离审计';
