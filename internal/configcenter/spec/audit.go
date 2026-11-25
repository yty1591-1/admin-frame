package spec

import "github.com/admin-frame/admin-frame/internal/configcenter/domain"

// auditKeySpecs 返回审计域可迁移键声明。
//
// 排除项：导出桶与归档前缀随对象存储部署，由基础设施配置管理。
func auditKeySpecs() []domain.KeySpec {
	keys := namespaceSpecs{namespace: NamespaceAudit, owner: ownerAudit}
	return []domain.KeySpec{
		keys.enum("audit.store", "auto", "审计存储出口", "auto", "db", "log"),
		keys.integerMin("audit.retention_days", 180, 1, "审计事件保留天数"),
		keys.boolean("audit.legal_hold", false, "法务保留期：只统计不删除"),
		keys.integerMin("audit.query.max_range_days", 31, 1, "审计查询最大时间跨度（天）"),
		keys.integerMin("audit.query.default_page_size", 20, 1, "审计查询默认每页条数"),
		keys.integerMin("audit.query.max_page_size", 100, 1, "审计查询每页条数上限"),
		keys.integerMin("audit.export.max_range_days", 31, 1, "审计导出最大时间跨度（天）"),
		keys.integerMin("audit.export.max_rows", 50000, 1, "审计导出最大行数"),
		keys.boolean("audit.export.async", false, "是否提供异步导出"),
		keys.integerMin("audit.export.ttl_days", 7, 1, "导出文件与任务记录保留天数"),
		keys.integerMin("audit.export.retry_max", 3, 1, "单个导出任务最大执行次数"),
		keys.duration("audit.export.poll_interval", "10s", "导出任务轮询间隔"),
		keys.duration("audit.export.stale_timeout", "10m", "running 任务失效超时"),
		keys.integerMin("audit.retention.batch_size", 5000, 1, "到期清理单批删除行数"),
		keys.duration("audit.retention.cleanup_interval", "24h", "到期清理任务执行间隔"),
		keys.boolean("audit.retention.dry_run", false, "到期清理演练模式：只统计不删除"),
	}
}
