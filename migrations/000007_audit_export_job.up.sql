-- ENT-04 T5：审计异步导出任务表。
--
-- 同步导出让请求线程持有整段 CSV 流式输出，大范围导出会把 API 连接长期占住，
-- 且失败后无法重试；异步任务把「请求」与「执行」拆开：API 只写任务行，worker
-- 负责生成文件并上传对象存储，下载走独立接口。
--
-- 状态机：pending → running → succeeded / failed；到 ttl_days 后由清理任务把
-- 任务置为 expired 并删除对象（对象键保留在行上，便于排查残留）。
-- 只由 cmd/migrate 或部署 Job 执行（DATA_STANDARDS）。

CREATE TABLE IF NOT EXISTS audit_export_job (
    export_id     VARCHAR(64)   NOT NULL                   COMMENT '导出任务 ID（雪花串），也是下载资源标识',
    actor_id      VARCHAR(64)   NOT NULL                   COMMENT '发起导出的主体 ID；仅本人可查/下载',
    actor_type    VARCHAR(16)   NOT NULL DEFAULT ''        COMMENT '主体类型，如 admin',
    channel       VARCHAR(16)   NOT NULL                   COMMENT '端别：admin / biz / c',
    merchant_id   VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT 'B 端商户 ID，可空',
    trace_id      VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '创建请求的追踪 ID，便于与访问日志关联',
    filter_json   JSON          NOT NULL                   COMMENT '导出过滤条件快照（from/to 与各过滤项）',
    status        VARCHAR(16)   NOT NULL DEFAULT 'pending' COMMENT 'pending / running / succeeded / failed / expired',
    attempts      INT           NOT NULL DEFAULT 0         COMMENT '已执行次数（含失败重试）',
    row_count     INT           NOT NULL DEFAULT 0         COMMENT '成功导出的行数',
    object_key    VARCHAR(512)  NOT NULL DEFAULT ''        COMMENT '对象存储键；成功后才有值',
    last_error    VARCHAR(512)  NOT NULL DEFAULT ''        COMMENT '最近一次失败原因（截断）',
    created_at    DATETIME(3)   NOT NULL                   COMMENT '创建时间（UTC）',
    started_at    DATETIME(3)   NULL                       COMMENT '最后一次开始执行时间（UTC）',
    finished_at   DATETIME(3)   NULL                       COMMENT '进入终态时间（UTC）',
    expires_at    DATETIME(3)   NOT NULL                   COMMENT '文件与记录保留截止时间（UTC）',
    PRIMARY KEY (export_id),
    KEY idx_audit_export_status (status, created_at) COMMENT 'worker 按创建顺序抢占待执行任务',
    KEY idx_audit_export_actor (actor_id, created_at) COMMENT '按发起人检索',
    KEY idx_audit_export_expires (expires_at) COMMENT '到期清理扫描'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='审计异步导出任务：请求与执行分离，文件落对象存储';
