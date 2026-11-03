-- ENT-09：存量商户回填为租户记录（守卫上线的前置步骤）。
--
-- 背景：B 端租户守卫开启后，未登记的 merchant_id 会被判定为「租户不存在」并拒绝（403）。
-- 存量部署的 merchant_id 只存在于 IAM 与身份表里，因此必须在开守卫之前把它们登记成租户，
-- 否则一次发布就会让所有 B 端商户不可用（ADR-0006「后果」段已列明该上线顺序）。
--
-- 规则：
--   1. 从 iam_role / iam_binding / identity_account 取非空 merchant_id 的并集；
--   2. 状态置 active —— 这些商户当前正在被服务，登记动作不得改变可用性；
--   3. 名称占位为 merchant_id，reason 标记 backfill，待运营补全真实名称；
--   4. 已存在的租户整行跳过（ON DUPLICATE KEY UPDATE id = id），
--      不覆盖运营已修改的名称、状态与停用原因。
--
-- 回滚：只删除由本迁移创建的占位行（reason 前缀 + 名称等于 ID），
-- 运营改过名称的租户不会被误删。
INSERT INTO tenant (id, name, status, status_reason, version, created_at, updated_at)
SELECT m.merchant_id, m.merchant_id, 'active', 'backfill: 存量商户自动登记', 1, UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)
FROM (
    SELECT DISTINCT merchant_id FROM iam_role WHERE merchant_id <> ''
    UNION
    SELECT DISTINCT merchant_id FROM iam_role_binding WHERE merchant_id <> ''
    UNION
    SELECT DISTINCT merchant_id FROM identity_account WHERE merchant_id <> ''
) AS m
WHERE m.merchant_id IS NOT NULL
  AND m.merchant_id <> ''
ON DUPLICATE KEY UPDATE id = id;
