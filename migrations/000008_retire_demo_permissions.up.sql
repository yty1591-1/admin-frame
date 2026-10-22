-- 000008 弃用订单/文件演示域权限点（只追加，不改写 000003）。
--
-- 演示域代码已移除，但权限码属于跨版本契约：保留目录条目与既有授权，
-- 仅将 status 置为 deprecated，禁止新授权；历史审计仍可解释这些权限码。
-- 文件名刻意不含 seed，避免被 catalog 的种子迁移解析测试重复计数。

UPDATE iam_permission
SET status = 'deprecated', updated_at = UTC_TIMESTAMP(3)
WHERE code IN ('order:read', 'order:write', 'file:read', 'file:write');
