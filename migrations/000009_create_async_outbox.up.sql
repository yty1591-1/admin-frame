-- 000009_create_async_outbox.up.sql
-- ENT-03：平台级异步基础设施的持久化底座。
-- 仅由 cmd/migrate 或部署 Job 执行；API / worker 启动不做迁移（DATA_STANDARDS）。

CREATE TABLE IF NOT EXISTS async_outbox (
    id               VARCHAR(64)   NOT NULL                   COMMENT '事件主键（event_id）；同时作为投递顺序与幂等依据',
    topic            VARCHAR(128)  NOT NULL                   COMMENT '消费路由主题，如 iam.audit',
    event_type       VARCHAR(128)  NOT NULL                   COMMENT '事件类型，如 audit.event',
    version          VARCHAR(16)   NOT NULL DEFAULT 'v1'      COMMENT '事件 schema 版本；不兼容变更发布新版本',
    tenant_id        VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '租户隔离键，可空',
    aggregate_type   VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '聚合类型，用于排查与去重',
    aggregate_id     VARCHAR(128)  NOT NULL DEFAULT ''        COMMENT '聚合 ID',
    idempotency_key  VARCHAR(128)  NOT NULL DEFAULT ''        COMMENT '业务幂等键；空时消费方回退 event_id',
    trace_id         VARCHAR(64)   NOT NULL DEFAULT ''        COMMENT '追踪 ID，与 OBS / RequestID 对齐',
    payload          JSON          NOT NULL                   COMMENT '事件载荷；禁止含 Token、密钥、完整 PII',
    status           VARCHAR(16)   NOT NULL DEFAULT 'pending' COMMENT 'pending / processing / succeeded / failed / dead',
    attempts         INT           NOT NULL DEFAULT 0         COMMENT '已投递失败次数，指数退避依据',
    next_retry_at    DATETIME(3)   NULL                       COMMENT '下次可投递时间（UTC）；NULL 表示立即',
    last_error       VARCHAR(512)  NOT NULL DEFAULT ''        COMMENT '最近一次失败原因（截断）',
    occurred_at      DATETIME(3)   NOT NULL                   COMMENT '业务发生时间（UTC）',
    created_at       DATETIME(3)   NOT NULL                   COMMENT '入盒时间（UTC），与业务写入同事务',
    updated_at       DATETIME(3)   NOT NULL                   COMMENT '状态最后更新时间（UTC）',
    PRIMARY KEY (id),
    KEY idx_async_outbox_due (status, next_retry_at) COMMENT 'Relay 拉取到期事件',
    KEY idx_async_outbox_created (created_at) COMMENT '积压滞后统计',
    KEY idx_async_outbox_topic (topic, created_at) COMMENT '按主题排查投递'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='平台事务性 Outbox：业务提交后由 Relay 至少一次投递';

CREATE TABLE IF NOT EXISTS async_dlq (
    id           BIGINT        NOT NULL AUTO_INCREMENT,
    source_id    VARCHAR(64)   NOT NULL                   COMMENT '原 outbox 事件 ID',
    topic        VARCHAR(128)  NOT NULL                   COMMENT '原路由主题',
    event_type   VARCHAR(128)  NOT NULL                   COMMENT '原事件类型',
    payload      JSON          NOT NULL                   COMMENT '原载荷快照',
    attempts     INT           NOT NULL DEFAULT 0         COMMENT '失败前累计尝试次数',
    last_error   VARCHAR(1024) NOT NULL DEFAULT ''        COMMENT '最终失败原因',
    failed_at    DATETIME(3)   NOT NULL                   COMMENT '进入死信时间（UTC）',
    replayed_at  DATETIME(3)   NULL                       COMMENT '重放时间（UTC）；NULL 表示未处理',
    PRIMARY KEY (id),
    UNIQUE KEY uk_async_dlq_source (source_id) COMMENT '同一事件只保留一条死信记录',
    KEY idx_async_dlq_failed (failed_at) COMMENT '死信积压与保留期清理'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='异步死信：毒消息与重试耗尽事件，需告警与人工重放';

CREATE TABLE IF NOT EXISTS async_consumed (
    event_id    VARCHAR(64)  NOT NULL                  COMMENT '事件 ID',
    consumer    VARCHAR(64)  NOT NULL                  COMMENT '消费者名称（同一事件可被多消费者各处理一次）',
    status      VARCHAR(16)  NOT NULL                  COMMENT 'processing / done',
    updated_at  DATETIME(3)  NOT NULL                  COMMENT '状态更新时间（UTC），processing 超时后可接管',
    PRIMARY KEY (event_id, consumer),
    KEY idx_async_consumed_updated (updated_at) COMMENT '清理与接管扫描'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='消费幂等记录：保证至少一次投递下业务副作用不重复';
