-- 回滚 ENT-09 存量商户回填：删除由 000010 创建的占位租户。
--
-- 只删「名称等于 ID 且 reason 为 backfill」的行：运营补全过名称、或已停用的租户
-- 不会被这条语句删掉——它们已经不属于「自动占位」的范畴。

DELETE FROM tenant
WHERE status_reason = 'backfill: 存量商户自动登记'
  AND name = id;
