-- 000016 (up): 通知中心（ENT-10）——通知单 / 渠道投递 / 模板 / 偏好 / 黑名单。
--
-- 设计要点：
--   1. 正文与 PII 不落库：notification_request 只存 params_digest（渲染参数
--      摘要）与 recipient_ref（subject_id 或加盐 HMAC），明文地址 / 验证码 /
--      邮件正文只存在于 Outbox 事件载荷与渲染瞬时内存；通知表是 90 天
--      保留数据（归口 ENT-15），最小化是硬约束。
--   2. 不建外键到 tenant 表：平台级通知（A 端代发、系统告警）使用哨兵值
--      'platform'，FK 会把它挡在库外；隔离由仓储层强制 tenant_id 过滤保证。
--   3. notification_request.idempotency_key 全局唯一：重复事件 / DLQ 重放
--      / 双副本并发消费在这里收敛为一次受理（ErrDuplicateRequest 幂等吸收）。
--   4. notification_delivery 按 (request_id, channel) 唯一：一个渠道一条
--      明细，重试在同一行上累加 attempt；与 Outbox 重试计数分离。
--   5. 状态机：request = pending→dispatched→(sent→delivered|undelivered)|failed|rejected；
--      delivery = pending→(accepted|retrying|failed)，accepted→(delivered|undelivered)。
--      白名单在 domain 包 Transitions() / DeliveryTransitions() 有快照测试锁定，
--      改列语义必须同步改状态机与快照。
--   6. 模板按 (key, channel, locale) 定位、version 递增；渲染按版本锁定，
--      重试期间内容不会漂移。偏好按主体五元组唯一；黑名单 target 只存
--      加盐 HMAC（64 位十六进制），拖库不可还原投递目标。

CREATE TABLE IF NOT EXISTS notification_request (
    id              VARCHAR(64)   NOT NULL COMMENT '通知单 ID（雪花，事件 request_id 同值）',
    tenant_id       VARCHAR(64)   NOT NULL COMMENT '租户隔离键；平台级取哨兵 platform',
    scene           VARCHAR(64)   NOT NULL COMMENT '业务场景键（路由 / 偏好分类 / 配额分键依据）',
    recipient_ref   VARCHAR(128)  NOT NULL COMMENT '收件人引用（subject_id 或加盐 HMAC，无明文地址）',
    channels        VARCHAR(128)  NOT NULL COMMENT '期望渠道（逗号分隔，按优先顺序）',
    template_key    VARCHAR(128)  NOT NULL COMMENT '渲染模板键',
    locale          VARCHAR(16)   NOT NULL DEFAULT '' COMMENT '渲染语言标签；空取默认',
    params_digest   CHAR(64)      NOT NULL COMMENT '渲染参数 SHA-256 摘要（明文参数不落库）',
    idempotency_key VARCHAR(128)  NOT NULL COMMENT '幂等键（重复事件 / 重放在此收敛）',
    priority        VARCHAR(16)   NOT NULL COMMENT 'critical | normal | low',
    status          VARCHAR(16)   NOT NULL COMMENT 'pending | dispatched | sent | delivered | undelivered | failed | rejected',
    reject_reason   VARCHAR(255)  NOT NULL DEFAULT '' COMMENT '拒发原因（rejected 必填，运营可读）',
    version         BIGINT        NOT NULL DEFAULT 1 COMMENT '乐观锁版本（回执回写与编排进度 CAS）',
    actor           VARCHAR(64)   NOT NULL COMMENT '触发主体（操作者或系统标识）',
    reference       VARCHAR(128)  NOT NULL DEFAULT '' COMMENT '业务引用（低基数非 PII，如邀请 ID）',
    expires_at      DATETIME(3)   NOT NULL COMMENT '过期时间；编排时过期即弃发',
    created_at      DATETIME(3)   NOT NULL,
    updated_at      DATETIME(3)   NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_request_idempotency (idempotency_key),
    KEY idx_request_tenant (tenant_id, status, created_at),
    KEY idx_request_scene (scene, created_at)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '通知单（ENT-10）：PII 最小化、幂等键唯一、状态机单调';

CREATE TABLE IF NOT EXISTS notification_delivery (
    id            VARCHAR(64)  NOT NULL COMMENT '投递明细 ID',
    request_id    VARCHAR(64)  NOT NULL COMMENT '所属通知单 ID',
    tenant_id     VARCHAR(64)  NOT NULL COMMENT '租户隔离键（与通知单一致，冗余便于直查）',
    channel       VARCHAR(16)  NOT NULL COMMENT 'email | sms | push | inbox(预留)',
    provider      VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '实际供应商（主备切换后记录实际值）',
    status        VARCHAR(16)  NOT NULL COMMENT 'pending | retrying | accepted | failed | delivered | undelivered',
    attempt       INT          NOT NULL DEFAULT 1 COMMENT '累计尝试次数（渠道维度，与 Outbox 重试分离）',
    semantic_code VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '编排层归一化失败语义码',
    provider_code VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '供应商原始码（仅排障，不入指标标签）',
    last_error    VARCHAR(255) NOT NULL DEFAULT '' COMMENT '脱敏失败摘要（不含收件人与正文）',
    message_id    VARCHAR(128) NOT NULL DEFAULT '' COMMENT '渠道消息 ID（回执对账键）',
    next_retry_at DATETIME(3)  NULL COMMENT '下次重试时间（retrying 非空）',
    finished_at   DATETIME(3)  NULL COMMENT '终态时间',
    created_at    DATETIME(3)  NOT NULL,
    updated_at    DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_delivery_request_channel (request_id, channel),
    KEY idx_delivery_status (status, next_retry_at),
    KEY idx_delivery_tenant (tenant_id, created_at)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '渠道投递明细（ENT-10）：一渠道一行、重试累加、诊断字段脱敏';

CREATE TABLE IF NOT EXISTS notification_template (
    id           VARCHAR(64)  NOT NULL COMMENT '模板行 ID',
    template_key VARCHAR(128) NOT NULL COMMENT '模板键',
    channel      VARCHAR(16)  NOT NULL COMMENT '目标渠道',
    locale       VARCHAR(16)  NOT NULL DEFAULT '' COMMENT '语言标签；空串是默认 locale 的合法值',
    version      INT          NOT NULL COMMENT '版本号：同键重传即新版本，渲染按版本锁定',
    subject      VARCHAR(255) NOT NULL DEFAULT '' COMMENT '主题（email 必填，其他渠道留空）',
    body         TEXT         NOT NULL COMMENT '正文 / 模板体（占位符 {{name}}，声明式变量白名单）',
    enabled      TINYINT(1)   NOT NULL DEFAULT 1 COMMENT '停用的模板不可渲染',
    created_by   VARCHAR(64)  NOT NULL COMMENT '操作主体（审计留痕）',
    created_at   DATETIME(3)  NOT NULL,
    updated_at   DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_template_version (template_key, channel, locale, version),
    KEY idx_template_lookup (template_key, channel, locale, enabled)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '通知模板（ENT-10）：版本递增、enabled 开关、变更全审计';

CREATE TABLE IF NOT EXISTS notification_preference (
    id           VARCHAR(64) NOT NULL COMMENT '偏好行 ID',
    tenant_id    VARCHAR(64) NOT NULL COMMENT '租户隔离键；平台级取哨兵 platform',
    subject_type VARCHAR(16) NOT NULL COMMENT 'user | tenant',
    subject_id   VARCHAR(64) NOT NULL COMMENT '主体 ID',
    scene_class  VARCHAR(16) NOT NULL COMMENT 'transactional | operational | marketing',
    channel      VARCHAR(16) NOT NULL COMMENT '渠道',
    enabled      TINYINT(1)  NOT NULL DEFAULT 1 COMMENT '0 = 已退订（记录不存在默认允许）',
    created_at   DATETIME(3) NOT NULL,
    updated_at   DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_preference_subject (tenant_id, subject_type, subject_id, scene_class, channel),
    KEY idx_preference_lookup (tenant_id, subject_type, subject_id)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '渠道偏好（ENT-10）：opt-in 模型、退订是显式动作、事务类不可退订';

CREATE TABLE IF NOT EXISTS notification_blacklist (
    id          VARCHAR(64)  NOT NULL COMMENT '黑名单行 ID',
    tenant_id   VARCHAR(64)  NOT NULL COMMENT '租户隔离键；platform 条目全租户生效',
    channel     VARCHAR(16)  NOT NULL COMMENT '渠道',
    target_hmac CHAR(64)     NOT NULL COMMENT '目标地址加盐 HMAC 十六进制（无明文，拖库不可还原）',
    source      VARCHAR(16)  NOT NULL COMMENT 'complaint | bounce | manual',
    reason      VARCHAR(255) NOT NULL DEFAULT '' COMMENT '运营可读原因',
    created_by  VARCHAR(64)  NOT NULL COMMENT '操作主体',
    created_at  DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_blacklist_target (tenant_id, channel, target_hmac),
    KEY idx_blacklist_lookup (channel, target_hmac)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci
  COMMENT = '渠道黑名单（ENT-10）：目标 HMAC 化、投诉/退信/手工三来源';
