-- 身份域独立审计存储（IDN-GAP-SEC-07）：追加写审计表。
--
-- 背景：审计此前只经 Outbox 进入结构化日志，保留期 / 检索 / 不可抵赖性依赖日志平台。
-- 设计：
--   1. event_id 唯一，Outbox 重试按 event_id 幂等；
--   2. 只允许 INSERT；应用不提供 UPDATE / DELETE 审计行的常规接口（AUD-06）；
--   3. 索引覆盖 actor / target / trace 三类检索（验收要求）；
--   4. 保留期基线 ≥180 天（AUD-07），由运维通过分区或归档任务执行，不走业务进程。
-- 回滚：删除审计表；审计数据不可恢复，回滚前须导出。

CREATE TABLE IF NOT EXISTS identity_audit_event (
    event_id        VARCHAR(64)   NOT NULL              COMMENT '审计事件 ID（雪花 ID），幂等键',
    action          VARCHAR(64)   NOT NULL              COMMENT '稳定动作枚举，如 auth.login.succeeded',
    actor_id        VARCHAR(64)   NOT NULL              COMMENT '操作者主体 ID',
    actor_type      VARCHAR(32)   NULL                  COMMENT '操作者类型：user / admin / system',
    channel         VARCHAR(16)   NOT NULL              COMMENT '端别：admin / biz / c / worker',
    merchant_id     VARCHAR(64)   NOT NULL DEFAULT ''   COMMENT 'B 端商户 ID；A/C 端为空串',
    resource_type   VARCHAR(64)   NOT NULL              COMMENT '目标资源类型，如 account / session',
    resource_id     VARCHAR(128)  NOT NULL              COMMENT '目标资源 ID',
    outcome         VARCHAR(16)   NOT NULL              COMMENT '结果：success / failure / denied',
    occurred_at     DATETIME(3)   NOT NULL              COMMENT '事件发生时间（UTC）',
    trace_id        VARCHAR(64)   NOT NULL              COMMENT '请求追踪 ID',
    service         VARCHAR(64)   NOT NULL              COMMENT '产生事件的服务名',
    reason          VARCHAR(255)  NULL                  COMMENT '操作原因；禁止包含 PII 与密钥',
    attrs           JSON          NULL                  COMMENT '附加结构化字段；禁止包含敏感原文',
    created_at      DATETIME(3)   NOT NULL              COMMENT '落库时间（UTC）',
    PRIMARY KEY (event_id),
    KEY idx_identity_audit_actor (actor_id, occurred_at)
        COMMENT '按操作者检索',
    KEY idx_identity_audit_target (resource_type, resource_id, occurred_at)
        COMMENT '按目标资源检索',
    KEY idx_identity_audit_trace (trace_id)
        COMMENT '按追踪 ID 检索',
    KEY idx_identity_audit_occurred (occurred_at)
        COMMENT '保留期归档 / 分区扫描'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='身份域审计事件：只追加；保留期 ≥180 天（AUD-07）';
