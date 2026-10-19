package mq

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryOutbox 进程内外盒，供单测与本地联调。
type MemoryOutbox struct {
	mu   sync.Mutex
	recs []OutboxRecord
}

// NewMemoryOutbox 创建空外盒。
func NewMemoryOutbox() *MemoryOutbox {
	return &MemoryOutbox{}
}

// Enqueue 实现 OutboxStore。
func (o *MemoryOutbox) Enqueue(_ context.Context, rec OutboxRecord) error {
	if o == nil {
		return ErrInvalidConfig
	}
	if rec.ID == "" {
		return fmt.Errorf("%w: outbox id required", ErrInvalidMessage)
	}
	if err := rec.Message.Validate(); err != nil {
		return err
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.recs = append(o.recs, rec)
	return nil
}

// FetchPending 实现 OutboxStore。
func (o *MemoryOutbox) FetchPending(_ context.Context, limit int) ([]OutboxRecord, error) {
	if o == nil {
		return nil, ErrInvalidConfig
	}
	if limit <= 0 {
		limit = 32
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]OutboxRecord, 0, limit)
	for _, r := range o.recs {
		if !r.PublishedAt.IsZero() {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// MarkPublished 实现 OutboxStore。
func (o *MemoryOutbox) MarkPublished(_ context.Context, id string, at time.Time) error {
	if o == nil {
		return ErrInvalidConfig
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.recs {
		if o.recs[i].ID == id {
			o.recs[i].PublishedAt = at
			return nil
		}
	}
	return fmt.Errorf("%w: outbox id %q", ErrNoHandler, id)
}

var _ OutboxStore = (*MemoryOutbox)(nil)
