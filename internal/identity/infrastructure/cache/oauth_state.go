package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// OAuth state 缓存键前缀。
const (
	oauthStateKeyPrefix     = "oauth:state:"
	oauthStateUsedKeyPrefix = "oauth:state:used:"
	// oauthStateConsumeTTL 是已消费标记的保留时长；仅用于阻止重放。
	oauthStateConsumeTTL = 10 * time.Minute
)

// oauthStateEntry 是进程内 state 条目。
type oauthStateEntry struct {
	value     app.OAuthState
	expiresAt time.Time
}

// MemoryOAuthStateStore 是进程内的一次性 state 存储，仅用于单副本 / 本地 / 单测。
type MemoryOAuthStateStore struct {
	mu     sync.Mutex
	states map[string]oauthStateEntry
	now    func() time.Time
}

// 编译期断言。
var _ app.OAuthStateStore = (*MemoryOAuthStateStore)(nil)

// NewMemoryOAuthStateStore 创建空的 state 存储；clock 为 nil 时使用系统时钟。
func NewMemoryOAuthStateStore(clock app.Clock) *MemoryOAuthStateStore {
	now := systemNow
	if clock != nil {
		now = clock.Now
	}
	return &MemoryOAuthStateStore{
		states: make(map[string]oauthStateEntry),
		now:    now,
	}
}

// Save 写入 state；同值覆盖。
func (s *MemoryOAuthStateStore) Save(_ context.Context, state string, value app.OAuthState, ttl time.Duration) error {
	if err := validateOAuthStateKey(state); err != nil {
		return err
	}
	if ttl <= 0 {
		return apperr.BadRequest("oauth state ttl must be > 0")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[state] = oauthStateEntry{value: value, expiresAt: s.now().Add(ttl)}
	return nil
}

// Consume 一次性消费 state；不存在、已消费或已过期返回 NotFound。
func (s *MemoryOAuthStateStore) Consume(_ context.Context, state string) (app.OAuthState, error) {
	if err := validateOAuthStateKey(state); err != nil {
		return app.OAuthState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.states[state]
	if !ok {
		return app.OAuthState{}, apperr.NotFound("oauth state not found")
	}
	delete(s.states, state)
	if !s.now().Before(entry.expiresAt) {
		return app.OAuthState{}, apperr.NotFound("oauth state not found")
	}
	return entry.value, nil
}

// RedisOAuthStateStore 基于 Redis 实现跨副本的一次性 state 存储。
type RedisOAuthStateStore struct {
	client *redis.Cache
}

// 编译期断言。
var _ app.OAuthStateStore = (*RedisOAuthStateStore)(nil)

// NewRedisOAuthStateStore 构造 Redis state 存储；client 为 nil 时操作失败关闭。
func NewRedisOAuthStateStore(client *redis.Cache) *RedisOAuthStateStore {
	return &RedisOAuthStateStore{client: client}
}

// Save 写入 state 的 JSON 值。
func (s *RedisOAuthStateStore) Save(ctx context.Context, state string, value app.OAuthState, ttl time.Duration) error {
	if err := validateOAuthStateKey(state); err != nil {
		return err
	}
	if s.client == nil {
		return apperr.Unavailable("oauth state store is not configured")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("identity oauth state encode: %w", err)
	}
	if err := s.client.Set(ctx, oauthStateKeyPrefix+state, encoded, ttl); err != nil {
		return fmt.Errorf("identity oauth state save: %w", err)
	}
	return nil
}

// Consume 用 SETNX 抢占消费标记后读取并删除原值，保证跨副本仅消费一次。
func (s *RedisOAuthStateStore) Consume(ctx context.Context, state string) (app.OAuthState, error) {
	if err := validateOAuthStateKey(state); err != nil {
		return app.OAuthState{}, err
	}
	if s.client == nil {
		return app.OAuthState{}, apperr.Unavailable("oauth state store is not configured")
	}
	consumed, err := s.client.SetNX(ctx, oauthStateUsedKeyPrefix+state, []byte("1"), oauthStateConsumeTTL)
	if err != nil {
		return app.OAuthState{}, fmt.Errorf("identity oauth state consume: %w", err)
	}
	if !consumed {
		return app.OAuthState{}, apperr.NotFound("oauth state not found")
	}
	raw, err := s.client.Get(ctx, oauthStateKeyPrefix+state)
	if err != nil {
		if errors.Is(err, redis.ErrMiss) {
			return app.OAuthState{}, apperr.NotFound("oauth state not found")
		}
		return app.OAuthState{}, fmt.Errorf("identity oauth state get: %w", err)
	}
	_ = s.client.Delete(ctx, oauthStateKeyPrefix+state)

	var value app.OAuthState
	if err := json.Unmarshal(raw, &value); err != nil {
		return app.OAuthState{}, apperr.NotFound("oauth state not found")
	}
	return value, nil
}

// validateOAuthStateKey 校验 state 非空。
func validateOAuthStateKey(state string) error {
	if strings.TrimSpace(state) == "" {
		return apperr.BadRequest("oauth state is required")
	}
	return nil
}
