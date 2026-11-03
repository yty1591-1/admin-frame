-- 000013 (down): 回滚租户元数据列（T-11）。
--
-- code 承载业务编码，删除即丢失（回滚后再升级只能用 id 重新兜底），
-- 因此 down 仅用于开发环境结构还原，生产禁止执行。

ALTER TABLE tenant
    DROP KEY uk_tenant_code,
    DROP COLUMN code,
    DROP COLUMN contact_name,
    DROP COLUMN contact_email,
    DROP COLUMN industry,
    DROP COLUMN timezone,
    DROP COLUMN locale,
    DROP COLUMN external_id;
