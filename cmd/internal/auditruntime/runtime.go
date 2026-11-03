// Package auditruntime 装配审计查询域（ENT-04）。
//
// 装配职责：按 audit.store 决定是否注册查询路由、构造查询与清理用例。
// 只在持久化数据库可用时启用；内存模式下审计仍走 SlogEmitter（AUDIT_STANDARDS §9）。
package auditruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	auditapp "github.com/admin-frame/admin-frame/internal/audit/app"
	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/audit/infrastructure/persistence"
	auditrest "github.com/admin-frame/admin-frame/internal/audit/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// Runtime 是审计查询域的运行时产物。
type Runtime struct {
	// Handler A 端审计查询与导出 Handler；为 nil 表示本进程未启用审计查询。
	Handler *auditrest.Handler
}

// New 按配置构造审计查询运行时。
//
// 未启用（audit.store=log 或数据库不可用）时返回空 Runtime 而非错误：
// 审计查询是可选能力，不应因为它让只提供登录的进程无法启动。
func New(deps *platform.Deps) (*Runtime, error) {
	if deps == nil || deps.Config == nil || deps.Database == nil {
		return &Runtime{}, nil
	}
	cfg := deps.Config
	if cfg.Audit.EffectiveStore(cfg.DB.Driver) != platform.AuditStoreDB {
		return &Runtime{}, nil
	}

	reader, err := persistence.NewSQLReader(deps.Database.SQL(), "")
	if err != nil {
		return nil, fmt.Errorf("audit reader: %w", err)
	}
	emitter := audit.MultiEmitter{
		audit.SQLEmitter{DB: deps.Database.SQL()},
		audit.SlogEmitter{Log: deps.Logger},
	}
	opts := auditapp.QueryOptions{
		MaxRange:       time.Duration(cfg.Audit.Query.MaxRangeDays) * 24 * time.Hour,
		MaxPageSize:    cfg.Audit.Query.MaxPageSize,
		ExportMaxRange: time.Duration(cfg.Audit.Export.MaxRangeDays) * 24 * time.Hour,
		ExportMaxRows:  cfg.Audit.Export.MaxRows,
	}
	service, err := auditapp.NewQueryService(reader, emitter, deps.IDs, opts, cfg.Service)
	if err != nil {
		return nil, err
	}
	// 异步导出：只有显式开启且对象存储可用时才装配；否则 Handler 的相关路由返回 503。
	var jobs *auditapp.ExportJobService
	if cfg.Audit.Export.Async {
		jobs, err = newExportJobService(deps, reader, emitter)
		if err != nil {
			return nil, err
		}
	}
	handler, err := auditrest.NewHandler(auditrest.HandlerConfig{
		Query: service,
		Jobs:  jobs,
		QueryLimits: pagination.Config{
			DefaultPageSize: cfg.Audit.Query.DefaultPageSize,
			MaxPageSize:     cfg.Audit.Query.MaxPageSize,
		},
		MaxRange:       opts.MaxRange,
		ExportMaxRange: opts.ExportMaxRange,
		Channel:        cfg.Channel,
		Service:        cfg.Service,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{Handler: handler}, nil
}

// RetentionHandle 是清理任务的关闭句柄。
type RetentionHandle struct {
	runner *auditapp.Runner
}

// StartRetention 启动审计到期清理任务；未启用落库或配置关闭时返回 nil。
//
// 与身份域清理一致：进程退出时通过 Stop 等待在途批次结束（REL-08）。
func StartRetention(ctx context.Context, deps *platform.Deps) (*RetentionHandle, error) {
	if deps == nil || deps.Config == nil || deps.Database == nil {
		return nil, nil
	}
	cfg := deps.Config
	if cfg.Audit.EffectiveStore(cfg.DB.Driver) != platform.AuditStoreDB {
		return nil, nil
	}
	store, err := persistence.NewSQLRetentionStore(deps.Database.SQL(), "")
	if err != nil {
		return nil, fmt.Errorf("audit retention store: %w", err)
	}
	emitter := audit.MultiEmitter{
		audit.SQLEmitter{DB: deps.Database.SQL()},
		audit.SlogEmitter{Log: deps.Logger},
	}
	// 归档桶与导出桶一致：同属审计数据，生命周期与权限应统一治理。
	archiver, err := newRetentionArchiver(deps)
	if err != nil {
		return nil, err
	}
	service, err := auditapp.NewRetentionService(store, archiver, emitter, deps.IDs, deps.Logger, auditapp.RetentionOptions{
		RetentionDays: cfg.Audit.RetentionDays,
		BatchSize:     cfg.Audit.Retention.BatchSize,
		LegalHold:     cfg.Audit.LegalHold,
		DryRun:        cfg.Audit.Retention.DryRun,
	}, cfg.Service)
	if err != nil {
		return nil, err
	}
	runner, err := auditapp.NewRunner(service, cfg.Audit.Retention.CleanupInterval, deps.Logger)
	if err != nil {
		return nil, err
	}
	runner.Start(ctx)
	deps.Logger.Info("audit retention started",
		"retention_days", cfg.Audit.RetentionDays,
		"batch_size", cfg.Audit.Retention.BatchSize,
		"interval", cfg.Audit.Retention.CleanupInterval.String(),
		"dry_run", cfg.Audit.Retention.DryRun,
		"legal_hold", cfg.Audit.LegalHold,
		"archive_prefix", cfg.Audit.Retention.ArchivePrefix,
	)
	return &RetentionHandle{runner: runner}, nil
}

// newRetentionArchiver 按 audit.retention.archive_prefix 构造归档器。
//
// 前缀非空但桶不可用时直接失败：静默回退成「只删不归档」正是 AUD-07 禁止的销毁路径。
func newRetentionArchiver(deps *platform.Deps) (auditapp.RetentionArchiver, error) {
	prefix := strings.TrimSpace(deps.Config.Audit.Retention.ArchivePrefix)
	if prefix == "" {
		return nil, nil
	}
	objects, ok := deps.Storage.Get(deps.Config.Audit.Export.Bucket)
	if !ok || objects == nil {
		return nil, fmt.Errorf("audit.retention.archive_prefix requires storage bucket %q", deps.Config.Audit.Export.Bucket)
	}
	return auditapp.ObjectArchiver{Objects: objects, Prefix: prefix}, nil
}

// Stop 停止清理任务并等待在途批次结束。
func (h *RetentionHandle) Stop(ctx context.Context) error {
	if h == nil || h.runner == nil {
		return nil
	}
	return h.runner.Stop(ctx)
}

// newExportJobService 构造异步导出用例。
//
// 对象存储按 audit.export.bucket 取命名桶，空表示默认桶；桶不存在直接失败关闭：
// 静默回落到默认桶会让导出文件与业务附件混在一起，容量与权限都难治理。
func newExportJobService(
	deps *platform.Deps,
	reader domain.Reader,
	emitter audit.Emitter,
) (*auditapp.ExportJobService, error) {
	cfg := deps.Config
	store, err := persistence.NewSQLExportStore(deps.Database.SQL(), "")
	if err != nil {
		return nil, fmt.Errorf("audit export store: %w", err)
	}
	objects, ok := deps.Storage.Get(cfg.Audit.Export.Bucket)
	if !ok || objects == nil {
		return nil, fmt.Errorf("audit.export.async requires storage bucket %q", cfg.Audit.Export.Bucket)
	}
	service, err := auditapp.NewExportJobService(auditapp.ExportJobConfig{
		Store:   store,
		Reader:  reader,
		Objects: objects,
		Emitter: emitter,
		IDs:     deps.IDs,
		Options: auditapp.ExportJobOptions{
			MaxRange:     time.Duration(cfg.Audit.Export.MaxRangeDays) * 24 * time.Hour,
			MaxRows:      cfg.Audit.Export.MaxRows,
			TTL:          time.Duration(cfg.Audit.Export.TTLDays) * 24 * time.Hour,
			RetryMax:     cfg.Audit.Export.RetryMax,
			StaleTimeout: cfg.Audit.Export.StaleTimeout,
			ObjectPrefix: "audit/exports",
		},
		Service: cfg.Service,
		Logger:  deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("audit export service: %w", err)
	}
	return service, nil
}

// ExportHandle 是异步导出任务的关闭句柄。
type ExportHandle struct {
	runner *auditapp.ExportRunner
}

// StartExportRunner 启动异步导出任务循环（仅 worker 调用）。
//
// 与保留期清理同宿主：API 进程不跑该循环，避免多副本重复执行同一任务
// （任务层已有行锁抢占，这里再按进程角色收敛一次，减少无效轮询）。
func StartExportRunner(ctx context.Context, deps *platform.Deps) (*ExportHandle, error) {
	if deps == nil || deps.Config == nil || deps.Database == nil {
		return nil, nil
	}
	cfg := deps.Config
	if !cfg.Audit.Export.Async || cfg.Audit.EffectiveStore(cfg.DB.Driver) != platform.AuditStoreDB {
		return nil, nil
	}
	reader, err := persistence.NewSQLReader(deps.Database.SQL(), "")
	if err != nil {
		return nil, fmt.Errorf("audit reader: %w", err)
	}
	emitter := audit.MultiEmitter{
		audit.SQLEmitter{DB: deps.Database.SQL()},
		audit.SlogEmitter{Log: deps.Logger},
	}
	service, err := newExportJobService(deps, reader, emitter)
	if err != nil {
		return nil, err
	}
	runner, err := auditapp.NewExportRunner(service, cfg.Audit.Export.PollInterval,
		time.Hour, 5, deps.Logger)
	if err != nil {
		return nil, err
	}
	runner.Start(ctx)
	deps.Logger.Info("audit export runner started",
		"poll_interval", cfg.Audit.Export.PollInterval.String(),
		"ttl_days", cfg.Audit.Export.TTLDays,
		"retry_max", cfg.Audit.Export.RetryMax,
		"max_rows", cfg.Audit.Export.MaxRows,
	)
	return &ExportHandle{runner: runner}, nil
}

// Stop 停止导出循环并等待当前任务结束。
func (h *ExportHandle) Stop(ctx context.Context) error {
	if h == nil || h.runner == nil {
		return nil
	}
	return h.runner.Stop(ctx)
}
