package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// ErrOutboxPoison 表示事件载荷永久不可投递（毒消息）。
//
// 投递器返回包装了该哨兵的错误时，发布器不再重试，直接标 failed 留待人工处理。
var ErrOutboxPoison = errors.New("outbox event is poison")

// OutboxRecorder 把审计事件写入 iam_outbox，由后台发布器异步投递。
//
// 与 SlogRecorder 的差异：Record 只保证「事件已持久化到 outbox」，
// 投递失败不会回滚已提交的业务写入，而是通过重试 + 死信 + 积压指标暴露。
// 写入失败仍然失败关闭：调用方必须让业务响应返回 50000（AUD-06）。
type OutboxRecorder struct {
	// Events outbox 仓储；nil 时返回内部错误（禁止静默丢弃审计）。
	Events domain.OutboxRepository
}

// Record 校验事件并写入 outbox。
//
// 复用审计事件 ID 作为 outbox 主键：同一事件不会重复入队，
// 也让「审计事件」与「投递记录」可以在一次按 ID 查询中对上。
func (r OutboxRecorder) Record(ctx context.Context, event audit.Event) error {
	if r.Events == nil {
		return apperr.Internal("iam outbox repository is not configured", nil)
	}
	if err := event.Validate(); err != nil {
		return apperr.Internal("invalid audit event", err)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return apperr.Internal("encode audit event", err)
	}
	eventType := domain.OutboxEventAudit
	record, err := domain.NewOutboxEvent(domain.NewOutboxEventParams{
		ID:            event.EventID,
		Type:          eventType,
		AggregateType: event.ResourceType,
		AggregateID:   event.ResourceID,
		Payload:       payload,
		Now:           event.OccurredAt,
	})
	if err != nil {
		return apperr.Internal("build outbox event", err)
	}
	if err := r.Events.Append(ctx, record); err != nil {
		return apperr.Internal("append iam outbox event", err)
	}
	return nil
}

// OutboxDeliverer 投递一条 outbox 事件；返回包装 ErrOutboxPoison 的错误表示不可重试。
type OutboxDeliverer func(ctx context.Context, event *domain.OutboxEvent) error

// OutboxObserver 观测投递结果；result 取值 success / retry / dead / poison。
type OutboxObserver interface {
	OnOutboxDelivered(ctx context.Context, result string)
}

// OutboxPublisherOptions 是发布器装配参数。
type OutboxPublisherOptions struct {
	// Events outbox 仓储，必填。
	Events domain.OutboxRepository
	// Deliver 投递实现，必填。
	Deliver OutboxDeliverer
	// Policy 退避策略；零值取 DefaultOutboxRetryPolicy。
	Policy domain.OutboxRetryPolicy
	// BatchSize 单轮最多处理事件数；零值取 50，上限 500。
	BatchSize int
	// Logger 记录投递失败；nil 时静默（仅测试）。
	Logger *slog.Logger
	// Observer 指标出口，可为 nil。
	Observer OutboxObserver
	// Now 时钟注入，便于测试退避；nil 取 time.Now。
	Now func() time.Time
}

// OutboxPublisher 消费 iam_outbox，把事件投递给审计出口。
//
// 单实例与多实例语义一致：认领是条件更新，同一事件只会有一个 worker 投递；
// 投递失败按指数退避重试，重试耗尽进入 dead 并由 iam_outbox_pending 告警。
type OutboxPublisher struct {
	events    domain.OutboxRepository
	deliver   OutboxDeliverer
	policy    domain.OutboxRetryPolicy
	batchSize int
	logger    *slog.Logger
	observer  OutboxObserver
	now       func() time.Time
}

// NewOutboxPublisher 校验依赖并构造发布器。
func NewOutboxPublisher(opts OutboxPublisherOptions) (*OutboxPublisher, error) {
	if opts.Events == nil {
		return nil, fmt.Errorf("iam outbox publisher: events repository is required")
	}
	if opts.Deliver == nil {
		return nil, fmt.Errorf("iam outbox publisher: deliver function is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &OutboxPublisher{
		events:    opts.Events,
		deliver:   opts.Deliver,
		policy:    opts.Policy.Normalize(),
		batchSize: normalizeOutboxBatch(opts.BatchSize),
		logger:    opts.Logger,
		observer:  opts.Observer,
		now:       now,
	}, nil
}

// Drain 处理一轮到期事件，返回成功投递数。
//
// 单个事件失败不阻断本轮其它事件；返回的错误是各失败原因的聚合，
// 供 worker 日志记录，是否告警由调用方决定。
func (p *OutboxPublisher) Drain(ctx context.Context) (int, error) {
	if p == nil || p.events == nil {
		return 0, apperr.Internal("iam outbox publisher is not configured", nil)
	}
	now := p.now().UTC()
	// 先回收租约过期的 in-flight 事件：标记成功时崩溃会让事件永久滞留 processing。
	reclaimed, err := p.events.ReclaimStale(ctx, now.Add(-domain.OutboxProcessingLease), now)
	if err != nil {
		return 0, fmt.Errorf("reclaim stale outbox events: %w", err)
	}
	if reclaimed > 0 && p.logger != nil {
		p.logger.WarnContext(ctx, "reclaimed stale iam outbox events",
			"count", reclaimed, "lease", domain.OutboxProcessingLease.String())
	}
	due, err := p.events.ListDue(ctx, now, p.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list due outbox events: %w", err)
	}
	delivered := 0
	var errs []error
	for _, event := range due {
		if err := ctx.Err(); err != nil {
			return delivered, err
		}
		claimed, claimErr := p.events.Claim(ctx, event.ID, now)
		if claimErr != nil {
			errs = append(errs, fmt.Errorf("claim outbox event %s: %w", event.ID, claimErr))
			continue
		}
		if !claimed {
			// 已被其它实例认领，跳过；不是错误。
			continue
		}
		if deliverErr := p.deliver(ctx, event); deliverErr != nil {
			errs = append(errs, p.handleFailure(ctx, event, deliverErr, now))
			continue
		}
		if markErr := p.events.MarkSucceeded(ctx, event.ID, now); markErr != nil {
			errs = append(errs, fmt.Errorf("mark outbox event %s succeeded: %w", event.ID, markErr))
			continue
		}
		delivered++
		p.observe(ctx, "success")
	}
	return delivered, errors.Join(errs...)
}

// handleFailure 记录失败：毒消息直接 failed，其余退避重试 / 死信。
func (p *OutboxPublisher) handleFailure(ctx context.Context, event *domain.OutboxEvent, deliverErr error, now time.Time) error {
	event.Attempts++
	if errors.Is(deliverErr, ErrOutboxPoison) {
		if err := p.events.MarkPoison(ctx, event.ID, now); err != nil {
			return fmt.Errorf("mark outbox event %s poison: %w", event.ID, err)
		}
		p.observe(ctx, "poison")
		p.logFailure(ctx, event, deliverErr, "poison")
		return fmt.Errorf("deliver outbox event %s: %w", event.ID, deliverErr)
	}
	if p.policy.Exhausted(event.Attempts) {
		if err := p.events.MarkDead(ctx, event.ID, now); err != nil {
			return fmt.Errorf("mark outbox event %s dead: %w", event.ID, err)
		}
		p.observe(ctx, "dead")
		p.logFailure(ctx, event, deliverErr, "dead")
		return fmt.Errorf("deliver outbox event %s: %w", event.ID, deliverErr)
	}
	nextRetryAt := now.Add(p.policy.NextRetry(event.Attempts))
	if err := p.events.Reschedule(ctx, event.ID, event.Attempts, nextRetryAt, now); err != nil {
		return fmt.Errorf("reschedule outbox event %s: %w", event.ID, err)
	}
	p.observe(ctx, "retry")
	p.logFailure(ctx, event, deliverErr, "retry")
	return fmt.Errorf("deliver outbox event %s: %w", event.ID, deliverErr)
}

// Run 按固定间隔持续投递，直到 ctx 取消。
//
// 启动即执行一轮，避免进程重启后要等满一个周期才开始补投。
func (p *OutboxPublisher) Run(ctx context.Context, interval time.Duration) error {
	if p == nil {
		return apperr.Internal("iam outbox publisher is not configured", nil)
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := p.Drain(ctx); err != nil && ctx.Err() == nil {
			if p.logger != nil {
				p.logger.ErrorContext(ctx, "iam outbox drain failed", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (p *OutboxPublisher) observe(ctx context.Context, result string) {
	if p.observer != nil {
		p.observer.OnOutboxDelivered(ctx, result)
	}
}

func (p *OutboxPublisher) logFailure(ctx context.Context, event *domain.OutboxEvent, err error, result string) {
	if p.logger == nil {
		return
	}
	p.logger.WarnContext(ctx, "iam outbox delivery failed",
		"event_id", event.ID,
		"event_type", string(event.Type),
		"aggregate_type", event.AggregateType,
		"aggregate_id", event.AggregateID,
		"attempts", event.Attempts,
		"result", result,
		"err", err,
	)
}

func normalizeOutboxBatch(size int) int {
	if size <= 0 {
		return 50
	}
	if size > 500 {
		return 500
	}
	return size
}

// AuditOutboxDeliverer 把 audit.event 投递给审计出口，未知类型按毒消息处理。
//
// 返回 ErrOutboxPoison 表示事件永远不可能投递成功（如载荷损坏 / 类型未知），
// 发布器会直接标记 failed 而不是无限重试。
func AuditOutboxDeliverer(emitter audit.Emitter) OutboxDeliverer {
	return func(ctx context.Context, event *domain.OutboxEvent) error {
		if event == nil {
			return fmt.Errorf("%w: nil outbox event", ErrOutboxPoison)
		}
		switch event.Type {
		case domain.OutboxEventAudit:
			var auditEvent audit.Event
			if err := json.Unmarshal(event.Payload, &auditEvent); err != nil {
				return fmt.Errorf("%w: decode audit payload: %w", ErrOutboxPoison, err)
			}
			if err := auditEvent.Validate(); err != nil {
				return fmt.Errorf("%w: invalid audit payload: %w", ErrOutboxPoison, err)
			}
			if emitter == nil {
				return apperr.Internal("audit emitter is not configured", nil)
			}
			if err := emitter.Emit(ctx, auditEvent); err != nil {
				return fmt.Errorf("emit audit event: %w", err)
			}
			return nil
		case domain.OutboxEventCacheInvalidate:
			// 缓存失效事件当前由 authz 快照读取兜底；接入广播后在此投递。
			return fmt.Errorf("%w: cache.invalidate delivery is not implemented", ErrOutboxPoison)
		default:
			return fmt.Errorf("%w: unsupported event type %q", ErrOutboxPoison, string(event.Type))
		}
	}
}

// 编译期断言：OutboxRecorder 必须满足用例侧审计端口。
var _ ChangeRecorder = OutboxRecorder{}
