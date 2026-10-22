-- 回滚 000005：移除审批单的商户维度。
-- 回滚会丢失商户归属信息；执行前应确认没有 B 端审批单仍处于 pending / approved 状态，
-- 否则回滚后这些单子会被当成平台级单据，存在越权风险。
-- 注意：本迁移只删列，不删行，merchant_id 的值随列一并丢失。

ALTER TABLE iam_change_request
    DROP KEY idx_iam_change_merchant_status,
    DROP COLUMN merchant_id;
