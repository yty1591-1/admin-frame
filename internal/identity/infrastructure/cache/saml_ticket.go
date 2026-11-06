package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// SAML 一次性兑换票据键前缀。
const (
	samlTicketKeyPrefix     = "saml:ticket:"
	samlTicketUsedKeyPrefix = "saml:ticket:used:"
	// samlTicketConsumeTTL 是已消费标记的保留时长；仅用于阻止重复兑换。
	samlTicketConsumeTTL = 5 * time.Minute
)

type samlTicketEntry struct {
	value     app.SAMLTicket
	expiresAt time.Time
}

// MemorySAMLTicketStore 是进程内的一次性 ticket 存储。
type MemorySAMLTicketStore struct {
	mu      sync.Mutex
	entries map[string]samlTicketEntry
	now     func() time.Time
}

// 编译期断言。
var _ app.SAMLTicketStore = (*MemorySAMLTicketStore)(nil)

// NewMemorySAMLTicketStore 创建空的 ticket 存储。
func NewMemorySAMLTicketStore(clock app.Clock) *MemorySAMLTicketStore {
	now := systemNow
	if clock != nil {
		now = clock.Now
	}
	return &MemorySAMLTicketStore{entries: make(map[string]samlTicketEntry), now: now}
}

// Put 写入 ticket；同值覆盖。
func (s *MemorySAMLTicketStore) Put(_ context.Context, ticket string, value app.SAMLTicket, ttl time.Duration) error {
	if ttl <= 0 {
		return apperr.BadRequest("saml ticket ttl must be > 0")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[ticket] = samlTicketEntry{value: value, expiresAt: s.now().Add(ttl)}
	return nil
}

// Consume 一次性消费 ticket；不存在或已过期返回 NotFound。
func (s *MemorySAMLTicketStore) Consume(_ context.Context, ticket string) (app.SAMLTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[ticket]
	if !ok {
		return app.SAMLTicket{}, apperr.NotFound("saml ticket not found")
	}
	delete(s.entries, ticket)
	if !s.now().Before(entry.expiresAt) {
		return app.SAMLTicket{}, apperr.NotFound("saml ticket not found")
	}
	return entry.value, nil
}

// RedisSAMLTicketStore 基于 Redis 实现跨副本的一次性 ticket 存储。
type RedisSAMLTicketStore struct {
	client *redis.Cache
}

// 编译期断言。
var _ app.SAMLTicketStore = (*RedisSAMLTicketStore)(nil)

// NewRedisSAMLTicketStore 构造 Redis ticket 存储；client 为 nil 时失败关闭。
func NewRedisSAMLTicketStore(client *redis.Cache) *RedisSAMLTicketStore {
	return &RedisSAMLTicketStore{client: client}
}

// Put 写入 ticket 的 JSON 值。
func (s *RedisSAMLTicketStore) Put(ctx context.Context, ticket string, value app.SAMLTicket, ttl time.Duration) error {
	if s.client == nil {
		return apperr.Unavailable("saml ticket store is not configured")
	}
	if ttl <= 0 {
		return apperr.BadRequest("saml ticket ttl must be > 0")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("identity saml ticket encode: %w", err)
	}
	if err := s.client.Set(ctx, samlTicketKeyPrefix+ticket, encoded, ttl); err != nil {
		return fmt.Errorf("identity saml ticket save: %w", err)
	}
	return nil
}

// Consume 用 SETNX 抢占消费标记后读取并删除原值，保证跨副本仅兑换一次。
func (s *RedisSAMLTicketStore) Consume(ctx context.Context, ticket string) (app.SAMLTicket, error) {
	if s.client == nil {
		return app.SAMLTicket{}, apperr.Unavailable("saml ticket store is not configured")
	}
	consumed, err := s.client.SetNX(ctx, samlTicketUsedKeyPrefix+ticket, []byte("1"), samlTicketConsumeTTL)
	if err != nil {
		return app.SAMLTicket{}, fmt.Errorf("identity saml ticket consume: %w", err)
	}
	if !consumed {
		return app.SAMLTicket{}, apperr.NotFound("saml ticket not found")
	}
	raw, err := s.client.Get(ctx, samlTicketKeyPrefix+ticket)
	if err != nil {
		if errors.Is(err, redis.ErrMiss) {
			return app.SAMLTicket{}, apperr.NotFound("saml ticket not found")
		}
		return app.SAMLTicket{}, fmt.Errorf("identity saml ticket get: %w", err)
	}
	_ = s.client.Delete(ctx, samlTicketKeyPrefix+ticket)
	var value app.SAMLTicket
	if err := json.Unmarshal(raw, &value); err != nil {
		return app.SAMLTicket{}, apperr.NotFound("saml ticket not found")
	}
	return value, nil
}
