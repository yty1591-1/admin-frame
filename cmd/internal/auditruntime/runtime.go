// Package auditruntime 装配审计查询域（ENT-04）。
//
// 装配职责：按 audit.store 决定是否注册查询路由、构造查询与清理用例。
// 只在持久化数据库可用时启用；内存模式下审计仍走 SlogEmitter（AUDIT_STANDARDS §9）。
package auditruntime

import (
	"context"
	"fmt"
	"time"

	auditapp "github.com/admin-frame/admin-frame/internal/audit/app"
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
	handler, err := auditrest.NewHandler(auditrest.HandlerConfig{
		Query: service,
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
	service, err := auditapp.NewRetentionService(store, emitter, deps.IDs, deps.Logger, auditapp.RetentionOptions{
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
	)
	return &RetentionHandle{runner: runner}, nil
}

// Stop 停止清理任务并等待在途批次结束。
func (h *RetentionHandle) Stop(ctx context.Context) error {
	if h == nil || h.runner == nil {
		return nil
	}
	return h.runner.Stop(ctx)
}
