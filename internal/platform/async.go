package platform

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// AsyncRuntime 是平台级通用异步基础设施的进程内装配（ENT-03）。
//
// 职责边界：
//   - 构造 Store / Deduper / Router（不 import 任何业务域）；
//   - 业务域通过 Register 声明 topic 与 handler；
//   - Run 启动 Relay 直到 ctx 取消，等待在途 handler 收敛。
type AsyncRuntime struct {
	cfg      *Config
	logger   *slog.Logger
	store    outbox.Store
	deduper  outbox.Deduper
	router   *outbox.Router
	observer outbox.Observer
}

// NewAsyncRuntime 按 async 配置装配通用异步组件；未启用时返回 (nil, nil)。
//
// async.enabled=true 要求持久化数据库：内存 Outbox 不跨进程，不能承载 worker 投递。
func NewAsyncRuntime(deps *Deps) (*AsyncRuntime, error) {
	if deps == nil || deps.Config == nil {
		return nil, fmt.Errorf("async runtime: deps is required")
	}
	cfg := deps.Config
	if !cfg.Async.Enabled {
		return nil, nil
	}
	if cfg.Async.Driver != "" && cfg.Async.Driver != AsyncDriverDB {
		return nil, fmt.Errorf("async runtime: driver %q is not implemented (only db is supported)", cfg.Async.Driver)
	}
	if deps.Database == nil {
		return nil, fmt.Errorf("async runtime: enabled requires a persistent database")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	store := NewAsyncStore(deps.Database)
	rt := &AsyncRuntime{
		cfg:     cfg,
		logger:  logger,
		store:   store,
		deduper: NewAsyncDeduper(deps.Database),
		router:  outbox.NewRouter(),
	}
	if deps.Metrics != nil {
		metrics, err := NewAsyncMetrics(deps.Metrics.RegistererWithoutChannel())
		if err != nil {
			return nil, err
		}
		if err := metrics.RegisterGauges(store.Stats); err != nil {
			return nil, err
		}
		rt.observer = metrics
	}
	return rt, nil
}

// NewAsyncStore 按数据库装配通用 Outbox Store；database 为 nil 时退回内存实现。
//
// 内存实现只用于单测 / 本地联调；async.enabled=true 时配置校验已拒绝该组合。
func NewAsyncStore(database *db.DB) outbox.Store {
	if database == nil {
		return outbox.NewMemoryStore()
	}
	return outbox.NewMySQLStore(database.SQL())
}

// NewAsyncDeduper 按数据库装配消费幂等记录；database 为 nil 时退回内存实现。
func NewAsyncDeduper(database *db.DB) outbox.Deduper {
	if database == nil {
		return outbox.NewMemoryDeduper()
	}
	return outbox.NewMySQLDeduper(database.SQL())
}

// Store 返回通用 Outbox Store，供业务域在同一事务内写入事件。
func (r *AsyncRuntime) Store() outbox.Store {
	if r == nil {
		return nil
	}
	return r.store
}

// AddObserver 组合额外的投递观察者（业务域保留自有指标）。
//
// 必须在 Run 之前调用；后注册的观察者与平台指标各自收到同一份回调。
func (r *AsyncRuntime) AddObserver(observer outbox.Observer) {
	if r == nil || observer == nil {
		return
	}
	if r.observer == nil {
		r.observer = observer
		return
	}
	existing := r.observer
	r.observer = outbox.ObserverFunc(func(ctx context.Context, topic, result string) {
		existing.OnOutboxDelivered(ctx, topic, result)
		observer.OnOutboxDelivered(ctx, topic, result)
	})
}

// PublishInAPI 报告 API 进程是否应启动 Relay（生产由 worker 独占）。
func (r *AsyncRuntime) PublishInAPI() bool {
	return r != nil && r.cfg.Async.Outbox.PublishInAPI
}

// Register 注册 topic 对应的 handler，并自动包裹消费幂等。
//
// 消费幂等记录按 topic 隔离：同一事件被不同 topic 消费互不影响；
// 重复投递由 async_consumed 收敛，handler 仍需保证自身业务幂等。
func (r *AsyncRuntime) Register(topic string, handler outbox.Handler) error {
	if r == nil {
		return fmt.Errorf("async runtime is not enabled")
	}
	topic = strings.TrimSpace(topic)
	wrapped, err := outbox.Idempotent(handler, outbox.IdempotentOptions{
		Deduper:           r.deduper,
		Consumer:          consumerName(topic),
		VisibilityTimeout: r.cfg.Async.Consumer.VisibilityTimeout,
		Logger:            r.logger,
	})
	if err != nil {
		return err
	}
	return r.router.Register(topic, wrapped)
}

// Run 启动 Relay 并阻塞至 ctx 取消；返回后在途 handler 已收敛。
func (r *AsyncRuntime) Run(ctx context.Context) error {
	if r == nil {
		return nil
	}
	policy := outbox.DefaultRetryPolicy()
	policy.MaxAttempts = r.cfg.Async.Outbox.MaxAttempts
	if r.cfg.Async.Consumer.RetryBackoff == AsyncBackoffFixed {
		policy.Backoff = outbox.BackoffFixed
	}
	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store:        r.store,
		Handler:      r.limitedHandler(),
		Policy:       policy,
		BatchSize:    r.cfg.Async.Outbox.BatchSize,
		Lease:        r.cfg.Async.Outbox.Lease,
		Concurrency:  r.cfg.Async.Outbox.Concurrency,
		PollInterval: r.cfg.Async.Outbox.PollInterval,
		Logger:       r.logger,
		Observer:     r.observer,
	})
	if err != nil {
		return fmt.Errorf("async relay: %w", err)
	}
	r.logger.InfoContext(ctx, "async relay started",
		"topics", r.router.Topics(),
		"poll_interval", r.cfg.Async.Outbox.PollInterval.String(),
		"concurrency", r.cfg.Async.Outbox.Concurrency,
		"consumer_concurrency", r.cfg.Async.Consumer.Concurrency,
	)
	if err := relay.Run(ctx); err != nil {
		return fmt.Errorf("async relay: %w", err)
	}
	return nil
}

// limitedHandler 用 async.consumer.concurrency 限制并发执行的 handler 数。
func (r *AsyncRuntime) limitedHandler() outbox.Handler {
	limit := r.cfg.Async.Consumer.Concurrency
	if limit <= 0 {
		limit = 4
	}
	sem := make(chan struct{}, limit)
	dispatch := r.router.Handler()
	return func(ctx context.Context, event *outbox.Event) error {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			return ctx.Err()
		}
		return dispatch(ctx, event)
	}
}

// consumerName 生成 async_consumed.consumer 值（列宽 VARCHAR(64)）。
//
// topic 唯一标识一个 handler，因此直接使用 topic；超长时按 rune 截断保证列不溢出。
func consumerName(topic string) string {
	const maxRunes = 64
	runes := []rune(topic)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return topic
}
