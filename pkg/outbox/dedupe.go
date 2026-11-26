package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ErrConsumerBusy 表示同一事件正被其它消费者处理（消费幂等记录处于 processing 租约内）。
var ErrConsumerBusy = errors.New("outbox: event is being processed by another consumer")

// DedupeOutcome 是抢占用消费幂等记录的结果。
type DedupeOutcome int

const (
	// DedupeAcquired 已获得处理权，调用方应执行 handler。
	DedupeAcquired DedupeOutcome = iota
	// DedupeDone 同一消费者已处理完成，跳过本次投递。
	DedupeDone
	// DedupeBusy 其它投递正在处理且租约未过期，应稍后重试。
	DedupeBusy
)

// String 便于日志与断言。
func (o DedupeOutcome) String() string {
	switch o {
	case DedupeAcquired:
		return "acquired"
	case DedupeDone:
		return "done"
	case DedupeBusy:
		return "busy"
	default:
		return "unknown"
	}
}

// 消费幂等记录状态（与 async_consumed.status 对齐）。
const (
	dedupeStatusProcessing = "processing"
	dedupeStatusDone       = "done"
)

// Deduper 记录「事件 × 消费者」的处理进度，把至少一次投递收敛为幂等副作用。
//
// Acquire 在记录不存在或 processing 租约过期时抢占成功；
// 处理成功调用 Complete，处理失败调用 Release 释放，交由 Relay 退避重试。
type Deduper interface {
	// Acquire 抢占处理权；visibility 为 processing 记录的接管窗口。
	Acquire(ctx context.Context, eventID, consumer string, now time.Time, visibility time.Duration) (DedupeOutcome, error)
	// Complete 把记录置为 done。
	Complete(ctx context.Context, eventID, consumer string) error
	// Release 删除 processing 记录，允许下次投递立即重试。
	Release(ctx context.Context, eventID, consumer string) error
}

// IdempotentOptions 是消费幂等包装的装配参数。
type IdempotentOptions struct {
	// Deduper 必填。
	Deduper Deduper
	// Consumer 消费者名称（同一事件可被不同消费者各处理一次）；必填。
	Consumer string
	// VisibilityTimeout processing 记录的接管窗口；≤0 取 60s。
	VisibilityTimeout time.Duration
	// Visibility 非空时在每次调用上取当前接管窗口，覆盖 VisibilityTimeout
	// （ADR-0015 热更）；只影响**本次抢占**，已存在的租约按其写入时的时长过期。
	Visibility func() time.Duration
	// Now 时钟注入，便于测试；nil 取 time.Now。
	Now func() time.Time
	// Logger 记录 Release 失败；nil 时静默。
	Logger *slog.Logger
}

// Idempotent 用 Deduper 包装 handler，保证重复投递不产生重复副作用。
//
// 语义：
//   - 已 done → 直接返回 nil（本次投递视为成功）；
//   - processing 租约内 → 返回可重试错误，稍后由 Relay 重新投递；
//   - handler 成功 → Complete；失败 → Release 后原样返回错误（交由 Relay 分类）。
func Idempotent(handler Handler, opts IdempotentOptions) (Handler, error) {
	if handler == nil {
		return nil, fmt.Errorf("%w: handler is required", ErrInvalidEvent)
	}
	if opts.Deduper == nil {
		return nil, fmt.Errorf("%w: deduper is required", ErrInvalidEvent)
	}
	consumer := strings.TrimSpace(opts.Consumer)
	if consumer == "" {
		return nil, fmt.Errorf("%w: consumer name is required", ErrInvalidEvent)
	}
	visibility := normalizeVisibility(opts.VisibilityTimeout)
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger

	return func(ctx context.Context, event *Event) error {
		if event == nil {
			return fmt.Errorf("%w: nil event", ErrInvalidEvent)
		}
		leaseWindow := visibility
		if opts.Visibility != nil {
			// 非法值（≤0）按装配期规则收敛，避免坏发布产生永不过期的租约。
			leaseWindow = normalizeVisibility(opts.Visibility())
		}
		outcome, err := opts.Deduper.Acquire(ctx, event.EventID, consumer, now().UTC(), leaseWindow)
		if err != nil {
			return fmt.Errorf("acquire consumer lease %s/%s: %w", event.EventID, consumer, err)
		}
		switch outcome {
		case DedupeDone:
			return nil
		case DedupeBusy:
			return fmt.Errorf("%w: event %s consumer %s", ErrConsumerBusy, event.EventID, consumer)
		}

		handlerErr := handler(ctx, event)
		if handlerErr != nil {
			if releaseErr := opts.Deduper.Release(ctx, event.EventID, consumer); releaseErr != nil && logger != nil {
				logger.WarnContext(ctx, "release consumer lease failed",
					"event_id", event.EventID,
					"consumer", consumer,
					"err", releaseErr,
				)
			}
			return handlerErr
		}
		if err := opts.Deduper.Complete(ctx, event.EventID, consumer); err != nil {
			return fmt.Errorf("complete consumer record %s/%s: %w", event.EventID, consumer, err)
		}
		return nil
	}, nil
}

// normalizeVisibility 补齐消费幂等接管窗口。
func normalizeVisibility(visibility time.Duration) time.Duration {
	if visibility <= 0 {
		return 60 * time.Second
	}
	return visibility
}
