-- 回滚 ENT-09：删除租户成员与配额表。
--
-- 顺序：先删引用方（成员 / 配额），再删被引用方由 000008 的 down 负责。

DROP TABLE IF EXISTS tenant_quota;
DROP TABLE IF EXISTS tenant_member;
