-- 000012 (down): 回滚套餐模型。
--
-- 物化进 tenant_quota 的配额保留：它们已是租户事实数据（可能被运营覆盖过），
-- 删套餐目录不应连带删除租户的开账上限。

ALTER TABLE tenant DROP FOREIGN KEY fk_tenant_plan;
ALTER TABLE tenant DROP COLUMN plan_id;
DROP TABLE IF EXISTS tenant_plan;
