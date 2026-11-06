package cache

import (
	"context"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// SAML 断言重放去重键前缀。
const samlReplayKeyPrefix = "saml:replay:"

// MemorySAMLReplayStore 是进程内的断言 ID 去重存储，仅用于本地 / 单副本。
type MemorySAMLReplayStore struct {
	mu      sync.Mutex
	entries map[string]time.Time
	now     func() time.Time
}

// 编译期断言。
var _ app.SAMLReplayStore = (*MemorySAMLReplayStore)(nil)

// NewMemorySAMLReplayStore 创建空的去重存储。
func NewMemorySAMLReplayStore(clock app.Clock) *MemorySAMLReplayStore {
	now := systemNow
	if clock != nil {
		now = clock.Now
	}
	return &MemorySAMLReplayStore{entries: make(map[string]time.Time), now: now}
}

// Reserve 原子占用断言哈希；已存在返回 Conflict。
func (s *MemorySAMLReplayStore) Reserve(_ context.Context, provider, assertionHash string, ttl time.Duration) error {
	if ttl <= 0 {
		return apperr.BadRequest("saml replay ttl must be > 0")
	}
	key := provider + ":" + assertionHash
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for stored, expiresAt := range s.entries {
		if !now.Before(expiresAt) {
			delete(s.entries, stored)
		}
	}
	if expiresAt, ok := s.entries[key]; ok && now.Before(expiresAt) {
		return apperr.Conflict("saml assertion replayed")
	}
	s.entries[key] = now.Add(ttl)
	return nil
}

// RedisSAMLReplayStore 基于 Redis SETNX 实现跨副本去重。
type RedisSAMLReplayStore struct {
	client *redis.Cache
}

// 编译期断言。
var _ app.SAMLReplayStore = (*RedisSAMLReplayStore)(nil)

// NewRedisSAMLReplayStore 构造 Redis 去重存储；client 为 nil 时失败关闭。
func NewRedisSAMLReplayStore(client *redis.Cache) *RedisSAMLReplayStore {
	return &RedisSAMLReplayStore{client: client}
}

// Reserve 用 SETNX + TTL 原子占用断言哈希；未抢占到表示重放。
func (s *RedisSAMLReplayStore) Reserve(ctx context.Context, provider, assertionHash string, ttl time.Duration) error {
	if s.client == nil {
		return apperr.Unavailable("saml replay store is not configured")
	}
	if ttl <= 0 {
		return apperr.BadRequest("saml replay ttl must be > 0")
	}
	reserved, err := s.client.SetNX(ctx, samlReplayKeyPrefix+provider+":"+assertionHash, []byte("1"), ttl)
	if err != nil {
		return err
	}
	if !reserved {
		return apperr.Conflict("saml assertion replayed")
	}
	return nil
}
