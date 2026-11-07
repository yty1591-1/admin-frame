package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Validate 校验审计存储、范围与保留策略（AUD-06 / AUD-07）。
//
// 平台段信息（环境、数据库驱动、存储驱动）由组合层显式传入，域包不反向依赖 platform。
// 语义约定与其他配置段一致：零值 / 空串表示「未配置」，由 ApplyDefaults 填默认值，
// 因此这里只校验显式设定的值；负数与越界一律拒绝。
func (c *AuditConfig) Validate(env, dbDriver, storageDriver string) error {
	var errs []error
	store := strings.ToLower(strings.TrimSpace(c.Store))
	effective := c.EffectiveStore(dbDriver)
	if store != "" {
		switch store {
		case AuditStoreAuto, AuditStoreDB, AuditStoreLog:
		default:
			errs = append(errs, fmt.Errorf("unsupported audit.store: %s", c.Store))
		}
	}
	if effective == AuditStoreLog && isProdEnv(env) {
		errs = append(errs, errors.New("audit.store must resolve to db when env=prod (AUD-06)"))
	}
	if store == AuditStoreDB && strings.EqualFold(strings.TrimSpace(dbDriver), "memory") {
		errs = append(errs, errors.New("audit.store=db requires a persistent db.driver"))
	}
	if days := c.RetentionDays; days != 0 && (days < AuditMinRetentionDays || days > AuditMaxRetentionDays) {
		errs = append(errs, fmt.Errorf(
			"audit.retention_days must be in [%d, %d] (AUD-07)",
			AuditMinRetentionDays, AuditMaxRetentionDays,
		))
	}
	if v := c.Query.MaxRangeDays; v != 0 && (v < 1 || v > 366) {
		errs = append(errs, errors.New("audit.query.max_range_days must be in [1, 366]"))
	}
	if v := c.Query.MaxPageSize; v != 0 && (v < 1 || v > 100) {
		errs = append(errs, errors.New("audit.query.max_page_size must be in [1, 100]"))
	}
	if v := c.Query.DefaultPageSize; v != 0 {
		upper := c.Query.MaxPageSize
		if upper == 0 {
			upper = 100
		}
		if v < 1 || v > upper {
			errs = append(errs, errors.New("audit.query.default_page_size must be in [1, max_page_size]"))
		}
	}
	if v := c.Export.MaxRangeDays; v != 0 && (v < 1 || v > 366) {
		errs = append(errs, errors.New("audit.export.max_range_days must be in [1, 366]"))
	}
	if v := c.Export.MaxRows; v != 0 && (v < 1 || v > 1000000) {
		errs = append(errs, errors.New("audit.export.max_rows must be in [1, 1000000]"))
	}
	if v := c.Export.TTLDays; v != 0 && (v < 1 || v > 365) {
		errs = append(errs, errors.New("audit.export.ttl_days must be in [1, 365]"))
	}
	if v := c.Export.RetryMax; v != 0 && (v < 1 || v > 10) {
		errs = append(errs, errors.New("audit.export.retry_max must be in [1, 10]"))
	}
	if v := c.Export.PollInterval; v != 0 && v < time.Second {
		errs = append(errs, errors.New("audit.export.poll_interval must be >= 1s"))
	}
	if v := c.Export.StaleTimeout; v != 0 && v < time.Minute {
		errs = append(errs, errors.New("audit.export.stale_timeout must be >= 1m"))
	}
	if v := c.Retention.BatchSize; v != 0 && (v < 100 || v > 50000) {
		errs = append(errs, errors.New("audit.retention.batch_size must be in [100, 50000]"))
	}
	if prefix := strings.TrimSpace(c.Retention.ArchivePrefix); prefix != "" {
		if strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "..") {
			errs = append(errs, errors.New("audit.retention.archive_prefix must be a relative object key prefix without '..'"))
		}
		// memory 存储随进程消失：归档落到这里等于「删了却查不到」。
		if storageDriver == "memory" {
			errs = append(errs, errors.New("audit.retention.archive_prefix requires a persistent storage.driver (memory loses archives on restart)"))
		}
	}
	if v := c.Retention.CleanupInterval; v != 0 && v < time.Minute {
		errs = append(errs, errors.New("audit.retention.cleanup_interval must be >= 1m"))
	}
	return errors.Join(errs...)
}

// isProdEnv 报告环境是否为生产。
func isProdEnv(env string) bool {
	return strings.EqualFold(strings.TrimSpace(env), "prod")
}
