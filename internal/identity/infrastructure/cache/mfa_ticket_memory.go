package cache

import (
	"context"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemoryMFATicketStore 是进程内的一次性票据存储，仅用于单副本 / 本地 / 单测。
//
// 多副本部署必须使用 Redis，否则同一票据可能在不同副本各消费一次。
type MemoryMFATicketStore struct {
	mu     sync.Mutex
	ticket map[string]time.Time
	clock  func() time.Time
}

// 编译期断言。
var _ app.MFATicketStore = (*MemoryMFATicketStore)(nil)

// NewMemoryMFATicketStore 创建空的进程内票据存储。
func NewMemoryMFATicketStore() *MemoryMFATicketStore {
	return &MemoryMFATicketStore{
		ticket: make(map[string]time.Time),
		clock:  func() time.Time { return time.Now().UTC() },
	}
}

// Consume 原子消费票据；首次消费返回 true，重放返回 false。
func (s *MemoryMFATicketStore) Consume(_ context.Context, jti string, ttl time.Duration) (bool, error) {
	if jti == "" {
		return false, apperr.BadRequest("mfa ticket id is required")
	}
	if ttl <= 0 {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if expiresAt, ok := s.ticket[jti]; ok {
		if now.Before(expiresAt) {
			return false, nil
		}
		delete(s.ticket, jti)
	}
	s.ticket[jti] = now.Add(ttl)
	return true, nil
}
