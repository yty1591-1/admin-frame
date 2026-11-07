package config

import "errors"

// Validate 校验租户处置时间线边界（ENT-15）。
//
// 保留期不得长于 3650 天，导出窗口同理；批量大小上限避免一次清理锁定整表。
// 零值表示「未配置」，保持历史配置可直接启动。
func (c *TenantConfig) Validate() error {
	var errs []error
	if c.Retention.HoldDays < 0 || c.Retention.HoldDays > 3650 {
		errs = append(errs, errors.New("tenant.retention.hold_days must be within [0, 3650]"))
	}
	if c.Retention.ExportDays < 0 || c.Retention.ExportDays > 3650 {
		errs = append(errs, errors.New("tenant.retention.export_days must be within [0, 3650]"))
	}
	if c.Retention.PurgeBatchSize < 0 || c.Retention.PurgeBatchSize > 100000 {
		errs = append(errs, errors.New("tenant.retention.purge_batch_size must be within [0, 100000]"))
	}
	return errors.Join(errs...)
}
