package config

import (
	"strings"
	"time"
)

// ApplyDefaults 填充审计默认值；缺省组合在 prod 仍会被校验拒绝。
func (c *AuditConfig) ApplyDefaults() {
	if strings.TrimSpace(c.Store) == "" {
		c.Store = AuditStoreAuto
	}
	if c.RetentionDays == 0 {
		c.RetentionDays = AuditMinRetentionDays
	}
	if c.Query.MaxRangeDays == 0 {
		c.Query.MaxRangeDays = 31
	}
	if c.Query.DefaultPageSize == 0 {
		c.Query.DefaultPageSize = 20
	}
	if c.Query.MaxPageSize == 0 {
		c.Query.MaxPageSize = 100
	}
	if c.Export.MaxRangeDays == 0 {
		c.Export.MaxRangeDays = 31
	}
	if c.Export.MaxRows == 0 {
		c.Export.MaxRows = 50000
	}
	if c.Export.TTLDays == 0 {
		c.Export.TTLDays = 7
	}
	if c.Export.RetryMax == 0 {
		c.Export.RetryMax = 3
	}
	if c.Export.PollInterval == 0 {
		c.Export.PollInterval = 10 * time.Second
	}
	if c.Export.StaleTimeout == 0 {
		c.Export.StaleTimeout = 10 * time.Minute
	}
	if c.Retention.BatchSize == 0 {
		c.Retention.BatchSize = 5000
	}
	if c.Retention.CleanupInterval == 0 {
		c.Retention.CleanupInterval = 24 * time.Hour
	}
}
