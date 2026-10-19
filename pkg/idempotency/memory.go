package idempotency

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// MemoryStore 进程内幂等存储（单测 / 单实例演示）。
// 跨实例请实现基于 Redis 或唯一索引表的 Store。
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]Record
	now     func() time.Time
}

// NewMemoryStore 构造空存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		entries: make(map[string]Record),
		now:     time.Now,
	}
}

// Begin 实现 Store。
func (s *MemoryStore) Begin(_ context.Context, key, fingerprint string, ttl time.Duration) (Record, bool, error) {
	if s == nil {
		return Record{}, false, ErrStoreUnavailable
	}
	if key == "" || fingerprint == "" {
		return Record{}, false, ErrInvalidKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked()

	if existing, ok := s.entries[key]; ok {
		if existing.Fingerprint != fingerprint {
			return Record{}, false, ErrConflict
		}
		switch existing.State {
		case StateCompleted:
			return existing.Clone(), false, nil
		case StateInProgress:
			return Record{}, false, ErrInProgress
		default:
			return Record{}, false, ErrConflict
		}
	}

	now := s.now()
	rec := Record{
		Key:         key,
		Fingerprint: fingerprint,
		State:       StateInProgress,
		ExpiresAt:   expireAt(now, ttl),
	}
	s.entries[key] = rec
	return rec.Clone(), true, nil
}

// Complete 实现 Store。
func (s *MemoryStore) Complete(_ context.Context, key, fingerprint string, statusCode int, header http.Header, body []byte, ttl time.Duration) error {
	if s == nil {
		return ErrStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked()

	existing, ok := s.entries[key]
	if !ok {
		return ErrConflict
	}
	if existing.Fingerprint != fingerprint || existing.State != StateInProgress {
		return ErrConflict
	}
	now := s.now()
	existing.State = StateCompleted
	existing.StatusCode = statusCode
	existing.Header = filterHeaders(header)
	existing.Body = append([]byte(nil), body...)
	existing.ExpiresAt = expireAt(now, ttl)
	s.entries[key] = existing
	return nil
}

// Release 实现 Store。
func (s *MemoryStore) Release(_ context.Context, key, fingerprint string) error {
	if s == nil {
		return ErrStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.entries[key]
	if !ok {
		return nil
	}
	if existing.Fingerprint != fingerprint {
		return ErrConflict
	}
	if existing.State == StateInProgress {
		delete(s.entries, key)
	}
	return nil
}

// Len 返回当前条目数（测试用）。
func (s *MemoryStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked()
	return len(s.entries)
}

func (s *MemoryStore) purgeExpiredLocked() {
	now := s.now()
	for k, v := range s.entries {
		if !v.ExpiresAt.IsZero() && !v.ExpiresAt.After(now) {
			delete(s.entries, k)
		}
	}
}

func expireAt(now time.Time, ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return now.Add(ttl)
}
