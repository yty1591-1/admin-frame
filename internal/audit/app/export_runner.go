package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// ExportRunner 以固定间隔驱动异步导出任务的执行与到期清理（ENT-04 T5）。
//
// 与保留期清理一样由 worker 独占：多副本时用行锁抢占任务，重复启动最多多跑
// 一个空轮次，不会重复导出同一条任务。
type ExportRunner struct {
	svc            *ExportJobService
	pollInterval   time.Duration
	expireInterval time.Duration
	maxPerTick     int
	logger         *slog.Logger
	cancel         context.CancelFunc
	done           chan struct{}
}

// NewExportRunner 构造导出任务驱动器。
//
// pollInterval <= 0 时取 10s；expireInterval <= 0 时取 1h；
// maxPerTick <= 0 时取 5（单轮最多连续处理 5 条，避免一次 tick 独占 worker）。
func NewExportRunner(svc *ExportJobService, pollInterval, expireInterval time.Duration, maxPerTick int, logger *slog.Logger) (*ExportRunner, error) {
	if svc == nil {
		return nil, apperr.Internal("audit export runner requires a service", nil)
	}
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second
	}
	if expireInterval <= 0 {
		expireInterval = time.Hour
	}
	if maxPerTick <= 0 {
		maxPerTick = 5
	}
	return &ExportRunner{
		svc:            svc,
		pollInterval:   pollInterval,
		expireInterval: expireInterval,
		maxPerTick:     maxPerTick,
		logger:         logger,
		done:           make(chan struct{}),
	}, nil
}

// Start 启动后台循环：先立即处理一轮（上线即可消费积压），随后按间隔轮询。
func (r *ExportRunner) Start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	go func() {
		defer close(r.done)
		lastExpire := time.Time{}
		r.runOnce(loopCtx, &lastExpire)
		ticker := time.NewTicker(r.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				r.runOnce(loopCtx, &lastExpire)
			}
		}
	}()
}

// Stop 停止循环并等待当前任务结束。
func (r *ExportRunner) Stop(ctx context.Context) error {
	if r == nil || r.cancel == nil {
		return nil
	}
	r.cancel()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runOnce 处理一轮：回收僵尸任务 → 执行至多 maxPerTick 条 → 到期清理。
func (r *ExportRunner) runOnce(ctx context.Context, lastExpire *time.Time) {
	if _, err := r.svc.RequeueStale(ctx); err != nil && r.logger != nil {
		r.logger.WarnContext(ctx, "audit export requeue stale failed", "error", err)
	}
	for i := 0; i < r.maxPerTick; i++ {
		processed, err := r.svc.ProcessOnce(ctx)
		if err != nil && r.logger != nil {
			// 单条任务失败不影响后续任务；错误已在服务层落到 last_error 与审计。
			r.logger.WarnContext(ctx, "audit export job failed", "error", err)
		}
		if !processed {
			break
		}
	}
	if time.Since(*lastExpire) < r.expireInterval {
		return
	}
	*lastExpire = time.Now()
	if count, err := r.svc.ExpireOnce(ctx); err != nil {
		if r.logger != nil {
			r.logger.WarnContext(ctx, "audit export expire failed", "error", err)
		}
		return
	} else if count > 0 && r.logger != nil {
		r.logger.InfoContext(ctx, "audit export files expired", "count", count)
	}
}
