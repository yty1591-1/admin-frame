package spec

import "github.com/admin-frame/admin-frame/internal/configcenter/domain"

// tenantKeySpecs 返回租户域可迁移键声明。
//
// 零值表示「未配置」，因此默认值全部为 0 / false，与 TenantConfig 零值一致。
func tenantKeySpecs() []domain.KeySpec {
	keys := namespaceSpecs{namespace: NamespaceTenant, owner: ownerTenant}
	return []domain.KeySpec{
		keys.integer("tenant.retention.hold_days", 0, "终止后数据保留天数；0 表示使用内置默认"),
		keys.integer("tenant.retention.export_days", 0, "终止后导出窗口天数；0 表示使用内置默认"),
		keys.integer("tenant.retention.purge_batch_size", 0, "硬删除分批行数；0 表示使用内置默认"),
		keys.boolean("tenant.retention.dry_run", false, "租户数据处置演练模式"),
	}
}
