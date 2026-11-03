-- 回滚 ENT-09：删除租户主数据表。
--
-- 租户记录是业务隔离边界，生产回滚前必须确认库内 tenant 为空或已导出：
-- 迁移回滚无法恢复被删除的租户数据。

DROP TABLE IF EXISTS tenant;
