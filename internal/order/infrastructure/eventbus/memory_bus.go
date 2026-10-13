package eventbus

import (
	"context"
	"log/slog"
	"sync"

	"github.com/admin-frame/admin-frame/internal/order/domain"
)

// MemoryBus 进程内事件总线，供 worker 轮询演示。
type MemoryBus struct {
	mu     sync.Mutex
	events []domain.Event
	log    *slog.Logger
}

// NewMemoryBus 创建进程内事件总线。
func NewMemoryBus(log *slog.Logger) *MemoryBus {
	return &MemoryBus{log: log}
}

// Publish 追加事件到内存队列（at-most 演示语义，非持久化）。
func (b *MemoryBus) Publish(_ context.Context, events ...domain.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, events...)
	for _, e := range events {
		b.log.Info("domain event published", "event", e.EventName())
	}
	return nil
}

// Drain 取出并清空已缓冲事件，供 worker 消费。
func (b *MemoryBus) Drain() []domain.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.events
	b.events = nil
	return out
}
