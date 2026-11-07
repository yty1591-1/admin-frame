package config

// TenantConfig 是租户域配置（T-23 / ENT-15：D-05 三段式处置参数）。
type TenantConfig struct {
	// Retention 租户终止后的数据处置时间线（tenant-data-lifecycle.md）。
	Retention TenantRetentionConfig `yaml:"retention"`
}

// TenantRetentionConfig 是三段式处置的时间线参数。
type TenantRetentionConfig struct {
	// HoldDays 保留期天数（从 terminated_at 起算；默认 90，合同可延长且延长必须留审计）。
	HoldDays int `yaml:"hold_days"`
	// ExportDays 导出窗口天数（默认 30；逾期未签收视为放弃并进入硬删除）。
	ExportDays int `yaml:"export_days"`
	// PurgeBatchSize 硬删除分批行数（默认 500，避免长事务）。
	PurgeBatchSize int `yaml:"purge_batch_size"`
	// DryRun 为 true 时只扫描与报告，不执行删除（演练模式）。
	DryRun bool `yaml:"dry_run"`
}
