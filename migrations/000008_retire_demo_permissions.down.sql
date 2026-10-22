-- 回滚 000008：恢复订单/文件演示域权限点为 active。
-- 仅用于回滚演练；演示域代码不会再恢复。

UPDATE iam_permission
SET status = 'active', updated_at = UTC_TIMESTAMP(3)
WHERE code IN ('order:read', 'order:write', 'file:read', 'file:write');
