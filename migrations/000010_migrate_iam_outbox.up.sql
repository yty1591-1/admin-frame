-- 000010_migrate_iam_outbox.up.sql
-- ENT-03 T8：IAM 自有 Outbox 完整迁移到平台事务性 Outbox（async_outbox），随后删除旧表。
-- 仅由 cmd/migrate 或部署 Job 执行；执行前须停止 IAM 旧发布器（API 内发布器与 worker）。
-- 迁移映射：audit.event → topic=iam.audit；cache.invalidate → topic=iam.cache；
-- 版本固定 v1，occurred_at 回填 created_at（历史事件无业务发生时间）。

INSERT INTO async_outbox (
    id, topic, event_type, version, tenant_id, aggregate_type, aggregate_id,
    idempotency_key, trace_id, payload, status, attempts, next_retry_at,
    last_error, occurred_at, created_at, updated_at
)
SELECT
    CAST(id AS CHAR(64)),
    CASE event_type
        WHEN 'cache.invalidate' THEN 'iam.cache'
        ELSE 'iam.audit'
    END,
    event_type,
    'v1',
    '',
    aggregate_type,
    aggregate_id,
    '',
    '',
    payload,
    status,
    attempts,
    next_retry_at,
    '',
    created_at,
    created_at,
    updated_at
FROM iam_outbox;

DROP TABLE IF EXISTS iam_outbox;
