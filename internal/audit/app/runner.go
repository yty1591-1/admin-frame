package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// Runner 以固定间隔驱动保留期清理任务（ENT-04 T6）。
//
// 单进程内只启动一次；多副本 worker 的互斥由数据库侧保证（分批条件删除天然幂等，
// 重复执行最多产生一次空批次，不会重复计数）。
type Runner struct {
	svc      *RetentionService
	interval time.Duration
	logger   *slog.Logger
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewRunner 构造清理任务驱动器；interval <= 0 时使用 24h。
func NewRunner(svc *RetentionService, interval time.Duration, logger *slog.Logger) (*Runner, error) {
	if svc == nil {
		return nil, apperr.Internal("audit retention runner requires a service", nil)
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &Runner{svc: svc, interval: interval, logger: logger, done: make(chan struct{})}, nil
}

// Start 启动后台清理循环：先立即执行一次（便于上线即观察到期数据），随后按间隔执行。
//
// ctx 取消后循环退出，Stop 等待进行中的一批清理完成，避免中断删除事务。
func (r *Runner) Start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	go func() {
		defer close(r.done)
		r.runOnce(loopCtx)
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				r.runOnce(loopCtx)
			}
		}
	}()
}

// Stop 停止循环并等待当前任务结束。
func (r *Runner) Stop(ctx context.Context) error {
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

// runOnce 执行一次清理并记录结果；失败只记日志，不影响进程退出码。
func (r *Runner) runOnce(ctx context.Context) {
	result, err := r.svc.PurgeOnce(ctx)
	if err != nil {
		if r.logger != nil {
			r.logger.WarnContext(ctx, "audit retention purge failed", "error", err)
		}
		return
	}
	if r.logger == nil {
		return
	}
	r.logger.InfoContext(ctx, "audit retention purge finished",
		"cutoff", result.Cutoff.Format(time.RFC3339),
		"purgeable", result.Purgeable,
		"deleted", result.Deleted,
		"batches", result.Batches,
		"dry_run", result.DryRun,
		"skipped", result.Skipped,
	)
}
