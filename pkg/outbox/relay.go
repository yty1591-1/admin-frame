package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/pkg/retry"
)

// Handler 处理一条 Outbox 事件；返回 nil 表示成功。
//
// 错误分类：
//   - retry.Permanent(err) 或重试耗尽 → DLQ（async_dlq）
//   - 其它错误 → 按 RetryPolicy 退避重试
//
// Handler 必须幂等：至少一次投递在租约回收 / 进程崩溃时会重复。
type Handler func(ctx context.Context, event *Event) error

// Observer 观测投递结果；result 取值 success / retry / dead / poison。
type Observer interface {
	OnOutboxDelivered(ctx context.Context, topic, result string)
}

// ObserverFunc 函数适配 Observer。
type ObserverFunc func(ctx context.Context, topic, result string)

// OnOutboxDelivered 实现 Observer。
func (f ObserverFunc) OnOutboxDelivered(ctx context.Context, topic, result string) {
	if f != nil {
		f(ctx, topic, result)
	}
}

// Params 是 Relay 可在运行期变更的参数（ADR-0015 白名单 `async.outbox.*`）。
//
// 字段为 0 或负数表示「沿用装配期值」，因此只发布其中一个键时不会把其余参数清零。
type Params struct {
	// PollInterval 无到期事件时的休眠。
	PollInterval time.Duration
	// BatchSize 单 worker 每轮认领上限。
	BatchSize int
	// Lease processing 租约。
	Lease time.Duration
	// MaxAttempts 最大投递次数（含首次）。
	MaxAttempts int
	// Backoff 退避算法；空值表示沿用装配期值。
	Backoff Backoff
}

// ParamsFunc 返回当前参数快照；每次循环结束都会重新读取，故必须廉价。
type ParamsFunc func() Params

// RelayOptions 是 Relay 装配参数。
type RelayOptions struct {
	// Store 必填。
	Store Store
	// Handler 必填。
	Handler Handler
	// Policy 退避策略；零值取 DefaultRetryPolicy。
	Policy RetryPolicy
	// BatchSize 单 worker 每轮认领上限；≤0 取 100。
	BatchSize int
	// Lease processing 租约；≤0 取 30s。
	Lease time.Duration
	// Concurrency 并发 worker 数；≤0 取 1。
	Concurrency int
	// PollInterval 无到期事件时的休眠；≤0 取 1s。
	PollInterval time.Duration
	// Params 运行期参数来源；非 nil 时覆盖上面四项（ADR-0015）。
	//
	// 覆盖只在取值合法时生效：越界值按装配期规则收敛，非法值被忽略，
	// 避免一次坏发布把 relay 打成忙循环或永不回收租约。
	Params ParamsFunc
	// ShutdownTimeout 单个在途 Handler 的收尾预算；≤0 取 10s。
	ShutdownTimeout time.Duration
	// Logger 记录失败；nil 时静默。
	Logger *slog.Logger
	// Observer 指标出口，可为 nil。
	Observer Observer
	// Now 时钟注入，便于测试退避；nil 取 time.Now。
	Now func() time.Time
}

// Relay 并发消费 Outbox，直至 ctx 取消。
//
// 多副本语义：ClaimBatch 为条件更新认领，同一事件只会被一个 Relay 投递；
// processing 超租约由 ReclaimStale 回收后重新投递（至少一次）。
type Relay struct {
	store           Store
	handler         Handler
	policy          RetryPolicy
	batchSize       int
	lease           time.Duration
	concurrency     int
	pollInterval    time.Duration
	shutdownTimeout time.Duration
	logger          *slog.Logger
	observer        Observer
	now             func() time.Time
	params          ParamsFunc
}

// NewRelay 校验依赖并构造 Relay。
func NewRelay(opts RelayOptions) (*Relay, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidEvent)
	}
	if opts.Handler == nil {
		return nil, fmt.Errorf("%w: handler is required", ErrInvalidEvent)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Relay{
		store:           opts.Store,
		handler:         opts.Handler,
		policy:          opts.Policy.Normalize(),
		batchSize:       normalizeBatchSize(opts.BatchSize),
		lease:           normalizeLease(opts.Lease),
		concurrency:     normalizeConcurrency(opts.Concurrency),
		pollInterval:    normalizePollInterval(opts.PollInterval),
		shutdownTimeout: normalizeShutdownTimeout(opts.ShutdownTimeout),
		logger:          opts.Logger,
		observer:        opts.Observer,
		now:             now,
		params:          opts.Params,
	}, nil
}

// currentParams 返回本轮使用的参数：装配期值叠加可热更覆盖。
//
// 覆盖值先按装配期同一套规则收敛（normalize*），越界不会突破安全区间。
func (r *Relay) currentParams() (pollInterval time.Duration, batchSize int, lease time.Duration, policy RetryPolicy) {
	pollInterval, batchSize, lease, policy = r.pollInterval, r.batchSize, r.lease, r.policy
	if r.params == nil {
		return pollInterval, batchSize, lease, policy
	}
	next := r.params()
	if next.PollInterval > 0 {
		pollInterval = normalizePollInterval(next.PollInterval)
	}
	if next.BatchSize > 0 {
		batchSize = normalizeBatchSize(next.BatchSize)
	}
	if next.Lease > 0 {
		lease = normalizeLease(next.Lease)
	}
	if next.MaxAttempts > 0 {
		// 与配置校验保持一致的上限，避免热更绕过声明期边界。
		attempts := next.MaxAttempts
		if attempts > maxAttemptsCeiling {
			attempts = maxAttemptsCeiling
		}
		policy.MaxAttempts = attempts
		policy = policy.Normalize()
	}
	if next.Backoff != "" {
		policy.Backoff = next.Backoff
		policy = policy.Normalize()
	}
	return pollInterval, batchSize, lease, policy
}

// Run 启动并发 worker 并阻塞至 ctx 取消；ctx 取消后等待在途 Handler 收尾。
func (r *Relay) Run(ctx context.Context) error {
	if r == nil || r.store == nil || r.handler == nil {
		return fmt.Errorf("%w: relay is not configured", ErrInvalidEvent)
	}
	var wg sync.WaitGroup
	wg.Add(r.concurrency)
	for i := 0; i < r.concurrency; i++ {
		go func() {
			defer wg.Done()
			r.worker(ctx)
		}()
	}
	wg.Wait()
	return nil
}

// DrainOnce 执行一轮认领与投递，返回处理条数（测试与重放工具使用）。
func (r *Relay) DrainOnce(ctx context.Context) (int, error) {
	_, batchSize, lease, policy := r.currentParams()
	now := r.now().UTC()
	reclaimed, err := r.store.ReclaimStale(ctx, now.Add(-lease), now)
	if err != nil {
		return 0, fmt.Errorf("reclaim stale outbox events: %w", err)
	}
	if reclaimed > 0 && r.logger != nil {
		r.logger.WarnContext(ctx, "reclaimed stale outbox events",
			"count", reclaimed, "lease", lease.String())
	}
	events, err := r.store.ClaimBatch(ctx, now, lease, batchSize)
	if err != nil {
		return 0, fmt.Errorf("claim outbox events: %w", err)
	}
	handled := 0
	for _, event := range events {
		if ctx.Err() != nil {
			return handled, ctx.Err()
		}
		if err := r.deliver(ctx, event, policy); err != nil {
			if ctx.Err() != nil {
				// 关闭中：交给租约回收，避免用已取消的 ctx 写状态。
				return handled, nil
			}
			r.logFailure(ctx, event, err)
		}
		handled++
	}
	return handled, nil
}

func (r *Relay) worker(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		handled, err := r.DrainOnce(ctx)
		if err != nil && ctx.Err() == nil && r.logger != nil {
			r.logger.ErrorContext(ctx, "outbox relay drain failed", "err", err)
		}
		if handled > 0 {
			continue
		}
		pollInterval, _, _, _ := r.currentParams()
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// deliver 调用 Handler 并根据结果落状态。
func (r *Relay) deliver(ctx context.Context, event *Event, policy RetryPolicy) error {
	handlerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.shutdownTimeout)
	defer cancel()

	handlerErr := r.callHandler(handlerCtx, event)
	now := r.now().UTC()
	if handlerErr == nil {
		if err := r.store.MarkSucceeded(ctx, event.EventID, now); err != nil {
			return fmt.Errorf("mark event %s succeeded: %w", event.EventID, err)
		}
		r.observe(ctx, event.Topic, "success")
		return nil
	}

	attempts, next, exhausted := event.NextAttempt(policy, now)
	cause := handlerErr.Error()
	if retry.IsPermanent(handlerErr) {
		if err := r.store.MarkFailed(ctx, event.EventID, now, cause); err != nil {
			return fmt.Errorf("mark event %s failed: %w", event.EventID, err)
		}
		r.observe(ctx, event.Topic, "poison")
		return handlerErr
	}
	if exhausted {
		if err := r.store.MarkDead(ctx, event.EventID, now, cause); err != nil {
			return fmt.Errorf("mark event %s dead: %w", event.EventID, err)
		}
		r.observe(ctx, event.Topic, "dead")
		return handlerErr
	}
	if err := r.store.Reschedule(ctx, event.EventID, attempts, next, now, cause); err != nil {
		return fmt.Errorf("reschedule event %s: %w", event.EventID, err)
	}
	r.observe(ctx, event.Topic, "retry")
	return handlerErr
}

// callHandler 捕获 panic 并转为永久错误，避免毒消息拖垮 worker。
func (r *Relay) callHandler(ctx context.Context, event *Event) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = retry.Permanent(fmt.Errorf("handler panic: %v", recovered))
		}
	}()
	return r.handler(ctx, event)
}

func (r *Relay) observe(ctx context.Context, topic, result string) {
	if r.observer != nil {
		r.observer.OnOutboxDelivered(ctx, topic, result)
	}
}

func (r *Relay) logFailure(ctx context.Context, event *Event, err error) {
	if r.logger == nil {
		return
	}
	r.logger.WarnContext(ctx, "outbox delivery failed",
		"event_id", event.EventID,
		"event_type", event.EventType,
		"topic", event.Topic,
		"attempts", event.Attempts,
		"err", err,
	)
}

// maxAttemptsCeiling 是最大投递次数上限，与 async.outbox.max_attempts 的声明区间一致。
const maxAttemptsCeiling = 20

func normalizeBatchSize(size int) int {
	if size <= 0 {
		return 100
	}
	if size > 500 {
		return 500
	}
	return size
}

func normalizeLease(lease time.Duration) time.Duration {
	if lease <= 0 {
		return 30 * time.Second
	}
	return lease
}

func normalizeConcurrency(n int) int {
	if n <= 0 {
		return 1
	}
	if n > 32 {
		return 32
	}
	return n
}

func normalizePollInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return time.Second
	}
	return interval
}

func normalizeShutdownTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 10 * time.Second
	}
	return timeout
}
