-- 000013 (up): 租户元数据（ENT-09 T-11）——code 唯一编码 + 联系人 / 行业 / 时区 / 语言 / 外部 ID。
--
-- 设计要点：
--   1. code 是人类可读的业务编码，全局唯一（uk_tenant_code）：运营与对账用它
--      而不是雪崩长度的雪花 ID 定位租户；创建后不可变（业务键，改了会破坏
--      外部系统的对账引用）。
--   2. 存量回填策略：先以 id 兜底回填（id 天然唯一，不会撞唯一键），
--      再收紧 NOT NULL + 唯一索引；需要语义化编码的租户由运营走数据订正
--      （唯一键仍在，人工改不会被并发写穿）。
--   3. 联系人 / 行业 / 时区 / 语言 / 外部 ID 全部允许 NULL：元数据是运营
--      补录信息，缺失不应阻塞创建与开通主流程。
--   4. external_id 用于对接客户侧系统（CRM / 结算），本系统不校验其格式，
--      只限长度——格式是对方系统的约定，猜错了反而对不上。

ALTER TABLE tenant
    ADD COLUMN code         VARCHAR(64)  NULL COMMENT '业务编码，全局唯一；创建后不可变' AFTER id,
    ADD COLUMN contact_name VARCHAR(64)  NULL COMMENT '联系人姓名' AFTER plan_id,
    ADD COLUMN contact_email VARCHAR(128) NULL COMMENT '联系邮箱' AFTER contact_name,
    ADD COLUMN industry     VARCHAR(64)  NULL COMMENT '所属行业' AFTER contact_email,
    ADD COLUMN timezone     VARCHAR(64)  NULL COMMENT 'IANA 时区，如 Asia/Shanghai' AFTER industry,
    ADD COLUMN locale       VARCHAR(16)  NULL COMMENT '语言标签，如 zh-CN' AFTER timezone,
    ADD COLUMN external_id  VARCHAR(128) NULL COMMENT '外部系统 ID（CRM / 结算）' AFTER locale;

-- 存量回填：code 以 id 兜底（唯一、非空），运营再按需订正为语义化编码。
UPDATE tenant SET code = id WHERE code IS NULL OR code = '';

ALTER TABLE tenant
    MODIFY COLUMN code VARCHAR(64) NOT NULL COMMENT '业务编码，全局唯一；创建后不可变',
    ADD UNIQUE KEY uk_tenant_code (code);
