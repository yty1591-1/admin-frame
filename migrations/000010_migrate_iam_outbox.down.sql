-- 000010_migrate_iam_outbox.down.sql
-- 回滚 ENT-03 T8：按 000002 原结构重建 iam_outbox，并把 IAM 主题事件搬回旧表。
-- 仅在回滚到旧 IAM 发布器时使用；执行前须停止平台 Relay。

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

INSERT INTO iam_outbox (
    id, event_type, aggregate_type, aggregate_id, payload,
    status, attempts, next_retry_at, created_at, updated_at
)
SELECT
    CAST(id AS UNSIGNED),
    event_type,
    aggregate_type,
    aggregate_id,
    payload,
    status,
    attempts,
    next_retry_at,
    created_at,
    updated_at
FROM async_outbox
WHERE topic IN ('iam.audit', 'iam.cache');

DELETE FROM async_outbox WHERE topic IN ('iam.audit', 'iam.cache');
