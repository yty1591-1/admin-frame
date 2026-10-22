package persistence

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemoryOutboxRepository 是基于内存 map 的 outbox 仓储。
//
// 仅用于单测与 local / demo：outbox 的价值来自「与业务写入同事务、跨实例可见」，
// 生产必须使用 SQLOutboxRepository。
type MemoryOutboxRepository struct {
	mu     sync.RWMutex
	events map[string]*domain.OutboxEvent
}

// NewMemoryOutboxRepository 创建空的 outbox 仓储。
func NewMemoryOutboxRepository() *MemoryOutboxRepository {
	return &MemoryOutboxRepository{events: make(map[string]*domain.OutboxEvent)}
}

// Append 写入一条 pending 事件；ID 冲突返回 Conflict（outbox 事件不可覆盖）。
func (r *MemoryOutboxRepository) Append(_ context.Context, event *domain.OutboxEvent) error {
	if event == nil {
		return apperr.BadRequest("outbox event is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.events[event.ID]; exists {
		return apperr.Conflict("outbox event id already exists")
	}
	r.events[event.ID] = cloneOutboxEvent(event)
	return nil
}

// ListDue 返回到期事件，按 ID 升序（投递顺序与写入顺序一致）。
func (r *MemoryOutboxRepository) ListDue(_ context.Context, now time.Time, limit int) ([]*domain.OutboxEvent, error) {
	limit = normalizeOutboxLimit(limit)
	r.mu.RLock()
	defer r.mu.RUnlock()

	due := make([]*domain.OutboxEvent, 0, limit)
	for _, event := range r.events {
		if event.IsDue(now) {
			due = append(due, cloneOutboxEvent(event))
		}
	}
	slices.SortFunc(due, func(a, b *domain.OutboxEvent) int {
		if a.ID == b.ID {
			return 0
		}
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	if len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}

// Claim 原子认领事件；返回 false 表示已被其它 worker 认领。
func (r *MemoryOutboxRepository) Claim(_ context.Context, id string, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event, ok := r.events[id]
	if !ok || event.Status != domain.OutboxPending {
		return false, nil
	}
	event.Status = domain.OutboxProcessing
	event.UpdatedAt = now.UTC()
	return true, nil
}

// ReclaimStale 把租约过期的 in-flight 事件放回 pending。
func (r *MemoryOutboxRepository) ReclaimStale(_ context.Context, staleBefore, now time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reclaimed := 0
	for _, event := range r.events {
		if event.Status != domain.OutboxProcessing || !event.UpdatedAt.Before(staleBefore) {
			continue
		}
		event.Status = domain.OutboxPending
		event.NextRetryAt = nil
		event.UpdatedAt = now.UTC()
		reclaimed++
	}
	return reclaimed, nil
}

// MarkSucceeded 标记投递成功。
func (r *MemoryOutboxRepository) MarkSucceeded(_ context.Context, id string, now time.Time) error {
	return r.transition(id, func(event *domain.OutboxEvent) error {
		return event.MarkSucceeded(now)
	})
}

// Reschedule 记录失败并放回 pending。
func (r *MemoryOutboxRepository) Reschedule(_ context.Context, id string, attempts int, nextRetryAt, now time.Time) error {
	return r.transition(id, func(event *domain.OutboxEvent) error {
		event.Status = domain.OutboxPending
		event.Attempts = attempts
		event.NextRetryAt = cloneTimePtr(&nextRetryAt)
		event.UpdatedAt = now.UTC()
		return nil
	})
}

// MarkDead 标记重试耗尽。
func (r *MemoryOutboxRepository) MarkDead(_ context.Context, id string, now time.Time) error {
	return r.transition(id, func(event *domain.OutboxEvent) error {
		return event.MarkDead(now)
	})
}

// MarkPoison 标记载荷永久不可投递。
func (r *MemoryOutboxRepository) MarkPoison(_ context.Context, id string, now time.Time) error {
	return r.transition(id, func(event *domain.OutboxEvent) error {
		return event.MarkPoison(now)
	})
}

// Stats 返回积压统计；dead / failed 也算未完成，必须进入告警口径。
func (r *MemoryOutboxRepository) Stats(_ context.Context) (domain.OutboxStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stats := domain.OutboxStats{}
	for _, event := range r.events {
		switch event.Status {
		case domain.OutboxPending, domain.OutboxProcessing, domain.OutboxDead, domain.OutboxFailed:
			stats.Pending++
			if stats.OldestCreatedAt.IsZero() || event.CreatedAt.Before(stats.OldestCreatedAt) {
				stats.OldestCreatedAt = event.CreatedAt
			}
		}
	}
	return stats, nil
}

func (r *MemoryOutboxRepository) transition(id string, mutate func(*domain.OutboxEvent) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	event, ok := r.events[id]
	if !ok {
		return apperr.NotFound("outbox event not found")
	}
	if event.Status != domain.OutboxProcessing {
		return apperr.Conflict("outbox event is not processing")
	}
	return mutate(event)
}

// normalizeOutboxLimit 收敛批量大小，避免单次拉取过大。
func normalizeOutboxLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func cloneOutboxEvent(event *domain.OutboxEvent) *domain.OutboxEvent {
	if event == nil {
		return nil
	}
	clone := *event
	clone.Payload = append([]byte(nil), event.Payload...)
	clone.NextRetryAt = cloneTimePtr(event.NextRetryAt)
	return &clone
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := value.UTC()
	return &copied
}
