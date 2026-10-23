package outbox

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DeadLetter 是死信记录的只读快照（测试与重放工具使用）。
type DeadLetter struct {
	SourceID   string
	Topic      string
	EventType  string
	Attempts   int
	LastError  string
	FailedAt   time.Time
	ReplayedAt *time.Time
}

// MemoryStore 是进程内 Outbox，供单测与本地联调；不提供跨进程语义。
type MemoryStore struct {
	mu       sync.Mutex
	events   map[string]*Event
	order    []string
	dead     map[string]*DeadLetter
	dlqOrder []string
}

// NewMemoryStore 创建空 MemoryStore。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		events: make(map[string]*Event),
		dead:   make(map[string]*DeadLetter),
	}
}

// Append 实现 Store。
func (s *MemoryStore) Append(_ context.Context, event *Event) error {
	if event == nil {
		return fmt.Errorf("%w: nil event", ErrInvalidEvent)
	}
	if event.Status != StatusPending && event.Status != "" {
		return fmt.Errorf("%w: append event %s is %s", ErrInvalidState, event.EventID, event.Status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.events[event.EventID]; exists {
		return fmt.Errorf("%w: event %s already exists", ErrInvalidEvent, event.EventID)
	}
	stored := event.Clone()
	stored.Status = StatusPending
	if stored.CreatedAt.IsZero() {
		stored.CreatedAt = time.Now().UTC()
		stored.UpdatedAt = stored.CreatedAt
	}
	s.events[stored.EventID] = stored
	s.order = append(s.order, stored.EventID)
	return nil
}

// ClaimBatch 实现 Store。
func (s *MemoryStore) ClaimBatch(_ context.Context, now time.Time, _ time.Duration, limit int) ([]*Event, error) {
	limit = normalizeBatchSize(limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Event, 0, limit)
	for _, id := range s.order {
		event := s.events[id]
		if event == nil || !event.IsDue(now) {
			continue
		}
		event.Status = StatusProcessing
		event.UpdatedAt = now.UTC()
		out = append(out, event.Clone())
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// MarkSucceeded 实现 Store。
func (s *MemoryStore) MarkSucceeded(_ context.Context, eventID string, now time.Time) error {
	return s.finish(eventID, StatusSucceeded, now, "")
}

// Reschedule 实现 Store。
func (s *MemoryStore) Reschedule(_ context.Context, eventID string, attempts int, nextRetryAt, now time.Time, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.events[eventID]
	if !ok {
		return fmt.Errorf("%w: event %s", ErrInvalidEvent, eventID)
	}
	if event.Status != StatusProcessing {
		return fmt.Errorf("%w: event %s is %s", ErrInvalidState, eventID, event.Status)
	}
	next := nextRetryAt.UTC()
	event.Status = StatusPending
	event.Attempts = attempts
	event.NextRetryAt = &next
	event.LastError = truncateCause(lastError)
	event.UpdatedAt = now.UTC()
	return nil
}

// MarkDead 实现 Store。
func (s *MemoryStore) MarkDead(_ context.Context, eventID string, now time.Time, lastError string) error {
	return s.finish(eventID, StatusDead, now, lastError)
}

// MarkFailed 实现 Store。
func (s *MemoryStore) MarkFailed(_ context.Context, eventID string, now time.Time, lastError string) error {
	return s.finish(eventID, StatusFailed, now, lastError)
}

func (s *MemoryStore) finish(eventID string, status Status, now time.Time, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.events[eventID]
	if !ok {
		return fmt.Errorf("%w: event %s", ErrInvalidEvent, eventID)
	}
	if event.Status != StatusProcessing {
		return fmt.Errorf("%w: event %s is %s", ErrInvalidState, eventID, event.Status)
	}
	event.Status = status
	event.NextRetryAt = nil
	event.LastError = truncateCause(lastError)
	event.UpdatedAt = now.UTC()
	if status == StatusDead || status == StatusFailed {
		s.recordDeadLetterLocked(event, now, lastError)
	}
	return nil
}

func (s *MemoryStore) recordDeadLetterLocked(event *Event, now time.Time, lastError string) {
	if s.dead[event.EventID] != nil {
		return
	}
	s.dead[event.EventID] = &DeadLetter{
		SourceID:  event.EventID,
		Topic:     event.Topic,
		EventType: event.EventType,
		Attempts:  event.Attempts,
		LastError: truncateCause(lastError),
		FailedAt:  now.UTC(),
	}
	s.dlqOrder = append(s.dlqOrder, event.EventID)
}

// ReclaimStale 实现 Store。
func (s *MemoryStore) ReclaimStale(_ context.Context, staleBefore, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var reclaimed int
	for _, event := range s.events {
		if event.Status != StatusProcessing || !event.UpdatedAt.Before(staleBefore.UTC()) {
			continue
		}
		event.Status = StatusPending
		event.UpdatedAt = now.UTC()
		reclaimed++
	}
	return reclaimed, nil
}

// Stats 实现 Store。
func (s *MemoryStore) Stats(_ context.Context) (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := Stats{DLQ: len(s.dead)}
	for _, event := range s.events {
		switch event.Status {
		case StatusPending, StatusProcessing, StatusDead, StatusFailed:
			stats.Pending++
			if stats.OldestCreatedAt.IsZero() || event.CreatedAt.Before(stats.OldestCreatedAt) {
				stats.OldestCreatedAt = event.CreatedAt
			}
		}
	}
	return stats, nil
}

// DeadLetters 返回死信快照（按失败顺序）。
func (s *MemoryStore) DeadLetters() []DeadLetter {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeadLetter, 0, len(s.dlqOrder))
	for _, id := range s.dlqOrder {
		out = append(out, *s.dead[id])
	}
	return out
}

// Event 返回事件快照（测试断言状态使用）。
func (s *MemoryStore) Event(eventID string) (*Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.events[eventID]
	if !ok {
		return nil, false
	}
	return event.Clone(), true
}

var _ Store = (*MemoryStore)(nil)

// ReplayDeadLetter 实现 DeadLetterReplayer。
func (s *MemoryStore) ReplayDeadLetter(_ context.Context, sourceID string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replayLocked(sourceID, now)
}

// ReplayAllDeadLetters 实现 DeadLetterReplayer。
func (s *MemoryStore) ReplayAllDeadLetters(_ context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := 0
	for _, id := range s.dlqOrder {
		if ok, err := s.replayLocked(id, now); err != nil {
			return replayed, err
		} else if ok {
			replayed++
		}
	}
	return replayed, nil
}

// replayLocked 要求调用方已持有 s.mu。
func (s *MemoryStore) replayLocked(sourceID string, now time.Time) (bool, error) {
	record, ok := s.dead[sourceID]
	if !ok || record.ReplayedAt != nil {
		return false, nil
	}
	event, ok := s.events[sourceID]
	if !ok {
		return false, nil
	}
	event.Status = StatusPending
	event.Attempts = 0
	event.NextRetryAt = nil
	event.LastError = ""
	event.UpdatedAt = now.UTC()
	replayedAt := now.UTC()
	record.ReplayedAt = &replayedAt
	return true, nil
}

var _ DeadLetterReplayer = (*MemoryStore)(nil)

// TopicStats 实现 TopicStatReader。
func (s *MemoryStore) TopicStats(_ context.Context, topic string) (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := Stats{}
	for _, event := range s.events {
		if event.Topic != topic {
			continue
		}
		switch event.Status {
		case StatusPending, StatusProcessing, StatusDead, StatusFailed:
			stats.Pending++
			if stats.OldestCreatedAt.IsZero() || event.CreatedAt.Before(stats.OldestCreatedAt) {
				stats.OldestCreatedAt = event.CreatedAt
			}
		}
	}
	for _, record := range s.dead {
		if record.Topic == topic && record.ReplayedAt == nil {
			stats.DLQ++
		}
	}
	return stats, nil
}

var _ TopicStatReader = (*MemoryStore)(nil)

// dedupeKey 是「事件 × 消费者」二维键。
type dedupeKey struct {
	eventID  string
	consumer string
}

type dedupeRecord struct {
	status    string
	updatedAt time.Time
}

// MemoryDeduper 是进程内消费幂等记录，供单测与本地联调；不提供跨进程语义。
type MemoryDeduper struct {
	mu      sync.Mutex
	records map[dedupeKey]dedupeRecord
}

// NewMemoryDeduper 创建空 MemoryDeduper。
func NewMemoryDeduper() *MemoryDeduper {
	return &MemoryDeduper{records: make(map[dedupeKey]dedupeRecord)}
}

// Acquire 实现 Deduper。
func (d *MemoryDeduper) Acquire(_ context.Context, eventID, consumer string, now time.Time, visibility time.Duration) (DedupeOutcome, error) {
	if eventID == "" {
		return DedupeAcquired, fmt.Errorf("%w: event id is required", ErrInvalidEvent)
	}
	key := dedupeKey{eventID: eventID, consumer: consumer}
	now = now.UTC()
	d.mu.Lock()
	defer d.mu.Unlock()
	record, ok := d.records[key]
	if ok {
		if record.status == dedupeStatusDone {
			return DedupeDone, nil
		}
		if now.Before(record.updatedAt.UTC().Add(normalizeVisibility(visibility))) {
			return DedupeBusy, nil
		}
	}
	d.records[key] = dedupeRecord{status: dedupeStatusProcessing, updatedAt: now}
	return DedupeAcquired, nil
}

// Complete 实现 Deduper。
func (d *MemoryDeduper) Complete(_ context.Context, eventID, consumer string) error {
	key := dedupeKey{eventID: eventID, consumer: consumer}
	d.mu.Lock()
	defer d.mu.Unlock()
	record, ok := d.records[key]
	if !ok || record.status != dedupeStatusProcessing {
		return fmt.Errorf("%w: consumer record %s/%s", ErrNotClaimed, eventID, consumer)
	}
	d.records[key] = dedupeRecord{status: dedupeStatusDone, updatedAt: time.Now().UTC()}
	return nil
}

// Release 实现 Deduper。
func (d *MemoryDeduper) Release(_ context.Context, eventID, consumer string) error {
	key := dedupeKey{eventID: eventID, consumer: consumer}
	d.mu.Lock()
	defer d.mu.Unlock()
	if record, ok := d.records[key]; ok && record.status == dedupeStatusProcessing {
		delete(d.records, key)
	}
	return nil
}

// Status 返回消费幂等记录状态（测试断言使用）。
func (d *MemoryDeduper) Status(eventID, consumer string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record, ok := d.records[dedupeKey{eventID: eventID, consumer: consumer}]
	if !ok {
		return "", false
	}
	return record.status, true
}

var _ Deduper = (*MemoryDeduper)(nil)
