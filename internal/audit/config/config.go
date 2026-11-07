package config

import (
	"strings"
	"time"
)

// AuditConfig 是审计存储、查询、导出与保留策略（docs/AUDIT_STANDARDS.md）。
//
// 默认 store=auto，按 db.driver 自动选择出口：持久库写审计表，内存库只写日志。
// 生产（env=prod）必须落库，否则审计不可查询、不可导出，违反 ENT-04 关闭判据。
type AuditConfig struct {
	// Store 存储出口：auto / db / log。
	Store string `yaml:"store"`
	// RetentionDays 保留天数；不得低于 AUD-07 基线（180 天）。
	RetentionDays int `yaml:"retention_days"`
	// LegalHold 为 true 时保留任务只统计不删除，供法务保留期使用。
	LegalHold bool `yaml:"legal_hold"`
	// Query 查询接口限制。
	Query AuditQueryConfig `yaml:"query"`
	// Export 导出接口限制。
	Export AuditExportConfig `yaml:"export"`
	// Retention 到期清理任务参数。
	Retention AuditRetentionConfig `yaml:"retention"`
}

// AuditQueryConfig 是审计查询的强制时间窗与分页上限。
//
// 无时间范围或超窗查询一律拒绝：审计表是追加写的宽表，全表扫描会拖垮业务库。
type AuditQueryConfig struct {
	// MaxRangeDays 单次查询允许的最大时间跨度（天）。
	MaxRangeDays int `yaml:"max_range_days"`
	// DefaultPageSize 未传 page_size 时的每页条数。
	DefaultPageSize int `yaml:"default_page_size"`
	// MaxPageSize 每页条数上限。
	MaxPageSize int `yaml:"max_page_size"`
}

// AuditExportConfig 是审计导出的范围与行数上限。
type AuditExportConfig struct {
	// MaxRangeDays 单次导出的最大时间跨度（天）。
	MaxRangeDays int `yaml:"max_range_days"`
	// MaxRows 单次导出的最大行数；超出即拒绝，要求缩小时间范围。
	MaxRows int `yaml:"max_rows"`
	// Async 为 true 时提供 POST 异步导出与任务查询 / 下载；false 只保留同步流式导出。
	Async bool `yaml:"async"`
	// TTLDays 导出文件与任务记录的保留天数；到期由 worker 删除对象。
	TTLDays int `yaml:"ttl_days"`
	// RetryMax 单个任务的最大执行次数（含首次）；失败按此上限重排。
	RetryMax int `yaml:"retry_max"`
	// PollInterval worker 轮询待执行任务的间隔。
	PollInterval time.Duration `yaml:"poll_interval"`
	// StaleTimeout running 状态超过该时长未完成即视为 worker 失效，重新排队。
	StaleTimeout time.Duration `yaml:"stale_timeout"`
	// Bucket 导出文件落哪个对象存储桶（storage.buckets 的命名桶）；空表示默认桶。
	Bucket string `yaml:"bucket"`
}

// AuditRetentionConfig 是到期清理任务参数。
type AuditRetentionConfig struct {
	// BatchSize 单批删除行数；分批删除避免长事务与锁等待。
	BatchSize int `yaml:"batch_size"`
	// CleanupInterval 清理任务执行间隔。
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
	// DryRun 为 true 时只统计待清理行数，不执行删除。
	DryRun bool `yaml:"dry_run"`
	// ArchivePrefix 到期数据的归档前缀（如 audit/expired）；空表示不归档、直接删除。
	//
	// 非空时必须「先归档成功再删除」：对象存储不可用即中止清理，避免销毁无副本。
	ArchivePrefix string `yaml:"archive_prefix"`
}

// EffectiveStore 返回按数据库驱动解析后的审计出口。
//
// 只在配置校验通过后调用；非法组合在校验期已拒绝，这里保证不会返回空值。
func (c AuditConfig) EffectiveStore(dbDriver string) string {
	switch strings.ToLower(strings.TrimSpace(c.Store)) {
	case AuditStoreDB:
		return AuditStoreDB
	case AuditStoreLog:
		return AuditStoreLog
	default:
		if strings.EqualFold(strings.TrimSpace(dbDriver), "memory") {
			return AuditStoreLog
		}
		return AuditStoreDB
	}
}

// 审计存储出口（audit.store）。
const (
	// AuditStoreAuto 按数据库驱动自动选择：持久库写 db，内存库退化 log。
	AuditStoreAuto = "auto"
	// AuditStoreDB 审计事件落独立审计表（AUD-06 追加写）；生产唯一允许的取值。
	AuditStoreDB = "db"
	// AuditStoreLog 仅写结构化日志，供 local / dev / test 使用（AUDIT_STANDARDS §9 例外 1）。
	AuditStoreLog = "log"
)

// 审计保留期基线（AUD-07）。
const (
	// AuditMinRetentionDays 认证 / 权限 / 配置类事件的最短保留天数。
	AuditMinRetentionDays = 180
	// AuditMaxRetentionDays 允许配置的保留天数上限，避免误写超长值导致任务永不清理。
	AuditMaxRetentionDays = 3650
)
