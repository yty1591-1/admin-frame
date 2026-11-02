package app

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// RetentionOptions 是保留期清理参数，来自 audit.retention 配置。
type RetentionOptions struct {
	// RetentionDays 保留天数；早于 now-RetentionDays 的事件到期。
	RetentionDays int
	// BatchSize 单批删除行数。
	BatchSize int
	// MaxBatches 单次执行的最大批次数，避免一次清理长时间占用数据库。
	MaxBatches int
	// LegalHold 为 true 时只统计不删除（法务保留期）。
	LegalHold bool
	// DryRun 为 true 时只统计不删除（演练 / 首次上线观察）。
	DryRun bool
}

// RetentionService 执行审计事件到期清理（ENT-04 T6 / AUD-07）。
//
// 只按 occurred_at 批量删除，不提供按主体 / 动作的定向删除能力；
// 每次清理都写一条元审计事件，保证「删除事实」可追溯（AUDIT_STANDARDS §5）。
type RetentionService struct {
	store   domain.RetentionStore
	emitter audit.Emitter
	ids     id.Generator
	logger  *slog.Logger
	opts    RetentionOptions
	service string
	now     func() time.Time
}

// NewRetentionService 构造保留期清理服务；store 与 ids 必填。
func NewRetentionService(store domain.RetentionStore, emitter audit.Emitter, ids id.Generator, logger *slog.Logger, opts RetentionOptions, service string) (*RetentionService, error) {
	if store == nil {
		return nil, apperr.Internal("audit retention service requires a store", nil)
	}
	if ids == nil {
		return nil, apperr.Internal("audit retention service requires an id generator", nil)
	}
	if opts.RetentionDays < 1 {
		return nil, apperr.Internal("audit retention days must be positive", nil)
	}
	if opts.BatchSize < 1 {
		opts.BatchSize = 5000
	}
	if opts.MaxBatches < 1 {
		opts.MaxBatches = 20
	}
	return &RetentionService{
		store:   store,
		emitter: emitter,
		ids:     ids,
		logger:  logger,
		opts:    opts,
		service: service,
		now:     func() time.Time { return time.Now().UTC() },
	}, nil
}

// PurgeResult 是单次清理结果。
type PurgeResult struct {
	// Cutoff 本次清理的时间界；早于该时刻的事件到期。
	Cutoff time.Time
	// Purgeable 到期行数（清理前统计）。
	Purgeable int
	// Deleted 实际删除行数。
	Deleted int
	// Batches 实际执行的删除批次数。
	Batches int
	// DryRun 本次是否仅统计。
	DryRun bool
	// Skipped 本次是否因法务保留而跳过删除。
	Skipped bool
}

// PurgeOnce 执行一次到期清理。
//
// 法务保留（LegalHold）与演练（DryRun）只统计到期行数，不执行删除；
// 其余情况按批次删除并在结束后写元审计事件。
func (s *RetentionService) PurgeOnce(ctx context.Context) (PurgeResult, error) {
	cutoff := s.now().AddDate(0, 0, -s.opts.RetentionDays)
	result := PurgeResult{Cutoff: cutoff, DryRun: s.opts.DryRun, Skipped: s.opts.LegalHold}

	purgeable, err := s.store.CountBefore(ctx, cutoff)
	if err != nil {
		return result, apperr.Internal("count expired audit events", err)
	}
	result.Purgeable = purgeable

	if s.opts.LegalHold || s.opts.DryRun {
		s.logInfo(ctx, "audit retention scan only",
			"cutoff", cutoff.Format(time.RFC3339),
			"purgeable", purgeable,
			"legal_hold", s.opts.LegalHold,
			"dry_run", s.opts.DryRun)
		return result, nil
	}
	if purgeable == 0 {
		return result, nil
	}

	for batch := 0; batch < s.opts.MaxBatches; batch++ {
		deleted, delErr := s.store.DeleteBefore(ctx, cutoff, s.opts.BatchSize)
		if delErr != nil {
			return result, apperr.Internal("delete expired audit events", delErr)
		}
		if deleted == 0 {
			break
		}
		result.Deleted += deleted
		result.Batches++
		if deleted < s.opts.BatchSize {
			break
		}
	}

	if err := s.emitPurge(ctx, result); err != nil {
		return result, apperr.Internal("emit audit retention event", err)
	}
	s.logInfo(ctx, "audit retention purge",
		"cutoff", cutoff.Format(time.RFC3339),
		"purgeable", result.Purgeable,
		"deleted", result.Deleted,
		"batches", result.Batches)
	return result, nil
}

// emitPurge 记录清理动作本身（元审计）。
func (s *RetentionService) emitPurge(ctx context.Context, result PurgeResult) error {
	if s.emitter == nil {
		return nil
	}
	eventID, err := s.ids.NextString()
	if err != nil {
		return err
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       audit.ActionAuditRetentionPurge,
		ActorID:      "system",
		ActorType:    "system",
		Channel:      "worker",
		ResourceType: "audit_event",
		ResourceID:   result.Cutoff.Format("20060102T150405Z"),
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   s.now(),
		// 清理是定时任务，无 HTTP 请求链路；用事件 ID 兜底保证必填字段完整。
		TraceID: eventID,
		Service: s.service,
		Attrs: map[string]string{
			"cutoff":    result.Cutoff.Format(time.RFC3339),
			"purgeable": strconv.Itoa(result.Purgeable),
			"deleted":   strconv.Itoa(result.Deleted),
			"batches":   strconv.Itoa(result.Batches),
		},
	}
	return s.emitter.Emit(ctx, event)
}

// logInfo 在 logger 存在时输出结构化日志。
func (s *RetentionService) logInfo(ctx context.Context, msg string, attrs ...any) {
	if s.logger == nil {
		return
	}
	s.logger.InfoContext(ctx, msg, attrs...)
}
