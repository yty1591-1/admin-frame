package iamruntime

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// NewOutboxTask 构造 worker 侧 IAM outbox 发布任务。
//
// worker 与 API 共用同一段发布器装配，避免两处投递语义漂移；
// worker 的内存 outbox 不与其他进程共享，因此要求持久化数据库。
func NewOutboxTask(deps *platform.Deps) (func(context.Context) error, error) {
	if deps == nil || deps.Config == nil {
		return nil, fmt.Errorf("iam outbox worker: deps is required")
	}
	if !deps.Config.IAM.Outbox.Enabled {
		return nil, nil
	}
	if deps.Database == nil {
		return nil, fmt.Errorf("iam outbox worker requires a persistent database")
	}
	return outboxTask(deps, buildOutboxRepository(deps), nil)
}

// outboxTask 按 outbox 配置构造可取消、可汇合的发布循环。
func outboxTask(
	deps *platform.Deps,
	repo domain.OutboxRepository,
	observer app.OutboxObserver,
) (func(context.Context) error, error) {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	publisher, err := app.NewOutboxPublisher(app.OutboxPublisherOptions{
		Events:    repo,
		Deliver:   app.AuditOutboxDeliverer(audit.SlogEmitter{Log: logger}),
		Policy:    domain.OutboxRetryPolicy{MaxAttempts: deps.Config.IAM.Outbox.MaxAttempts},
		BatchSize: deps.Config.IAM.Outbox.BatchSize,
		Logger:    logger,
		Observer:  observer,
	})
	if err != nil {
		return nil, fmt.Errorf("iam outbox publisher: %w", err)
	}
	interval := deps.Config.IAM.Outbox.PollInterval
	return func(ctx context.Context) error {
		return publisher.Run(ctx, interval)
	}, nil
}
