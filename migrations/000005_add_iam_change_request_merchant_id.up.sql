-- 000005 为 IAM 高风险变更审批单补充商户维度，支撑 B 端（/biz/v1/iam/*）租户隔离。
--
-- 背景：M4 的审批单只有 A 端（channel=admin）。B 端开放后，同一张表要同时承载
-- 平台级与商户级申请；若不落库 merchant_id，审批人无法按租户过滤待办，
-- 也无法在应用阶段证明「这张单子属于当前商户」（SEC-14 越权防护）。
--
-- 约定：
--   * merchant_id 为空串表示平台级（A 端）审批单，非空表示商户级（B 端）。
--   * 端别与商户的绑定关系由唯一索引约束：B 端必须有商户，A 端必须没有商户。
--   * 时间列由应用写入 UTC，迁移本身不含时间逻辑，避免时区污染。
--
-- 注意：MySQL 8.0 不支持 ADD COLUMN IF NOT EXISTS，本文件依赖迁移框架
-- 「每个 version 只执行一次」的保证；重复执行会失败而非静默跳过。

ALTER TABLE iam_change_request
    ADD COLUMN merchant_id VARCHAR(64) NOT NULL DEFAULT ''
        COMMENT '商户ID；空串表示平台级（A 端），非空表示商户级（B 端）' AFTER channel,
    ADD KEY idx_iam_change_merchant_status (merchant_id, channel, status, requested_at)
        COMMENT 'B 端待办列表：按商户 + 端别 + 状态 + 申请时间扫描';

-- 历史数据归一：000002 ~ 000005 之间的审批单全部来自 A 端，merchant_id 取 DEFAULT 空串即正确。
-- 显式归一平台级单据，保证「A 端不得携带商户」这一不变量在存量数据上同样成立。
-- 端别与商户的强校验放在领域层（domain.ChangeRequest.Validate），迁移只做数据归一，
-- 避免把业务规则固化进 DDL 后难以随租户模型演进。
UPDATE iam_change_request SET merchant_id = '' WHERE channel = 'admin' AND merchant_id <> '';
