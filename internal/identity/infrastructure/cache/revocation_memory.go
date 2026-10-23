package cache

import (
	"context"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemoryRevocationStore 是进程内撤销存储，仅用于单副本 / 本地 / 单测。
//
// 多副本部署必须使用 Redis，否则登出与停用在其他副本上无法及时生效。
type MemoryRevocationStore struct {
	mu      sync.RWMutex
	states  map[string]memoryEntry[app.AccountState]
	revoked map[string]memoryEntry[struct{}]

	clock func() time.Time
}

type memoryEntry[T any] struct {
	value     T
	expiresAt time.Time
}

// 编译期断言。
var _ app.RevocationStore = (*MemoryRevocationStore)(nil)

// NewMemoryRevocationStore 创建空的进程内撤销存储。
func NewMemoryRevocationStore() *MemoryRevocationStore {
	return &MemoryRevocationStore{
		states:  make(map[string]memoryEntry[app.AccountState]),
		revoked: make(map[string]memoryEntry[struct{}]),
		clock:   func() time.Time { return time.Now().UTC() },
	}
}

// LoadAccountState 读取账号撤销快照；过期视为未命中。
func (s *MemoryRevocationStore) LoadAccountState(_ context.Context, accountID string) (app.AccountState, bool, error) {
	if accountID == "" {
		return app.AccountState{}, false, apperr.BadRequest("account id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.states[accountID]
	if !ok || s.expired(entry.expiresAt) {
		return app.AccountState{}, false, nil
	}
	return entry.value, true, nil
}

// SaveAccountState 写入账号撤销快照。
func (s *MemoryRevocationStore) SaveAccountState(_ context.Context, accountID string, state app.AccountState, ttl time.Duration) error {
	if accountID == "" {
		return apperr.BadRequest("account id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[accountID] = memoryEntry[app.AccountState]{
		value:     state,
		expiresAt: s.clock().Add(ttl),
	}
	return nil
}

// RevokeToken 把 jti 写入黑名单。
func (s *MemoryRevocationStore) RevokeToken(_ context.Context, jti string, ttl time.Duration) error {
	if jti == "" {
		return apperr.BadRequest("jti is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[jti] = memoryEntry[struct{}]{expiresAt: s.clock().Add(ttl)}
	return nil
}

// IsTokenRevoked 报告 jti 是否仍在黑名单中。
func (s *MemoryRevocationStore) IsTokenRevoked(_ context.Context, jti string) (bool, error) {
	if jti == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.revoked[jti]
	if !ok || s.expired(entry.expiresAt) {
		return false, nil
	}
	return true, nil
}

// expired 报告条目是否已过期；调用方须持有读锁。
func (s *MemoryRevocationStore) expired(at time.Time) bool {
	return !at.IsZero() && !s.clock().Before(at)
}
