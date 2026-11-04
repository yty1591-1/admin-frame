-- 回滚：删除审批事件表。审批留痕同时存在于审计事件（tenant.approval.*），
-- 但本表删除后无法在应用内重建「谁在何时批准了哪个租户」的完整序列。
DROP TABLE IF EXISTS tenant_approval;
