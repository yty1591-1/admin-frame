package cache

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// mfaTicketKeyPrefix 是两步登录票据的一次性消费键。
const mfaTicketKeyPrefix = "authn:mfa_ticket:"

// RedisMFATicketStore 基于 Redis SETNX 实现跨副本的一次性票据消费。
type RedisMFATicketStore struct {
	client *redis.Cache
}

// 编译期断言。
var _ app.MFATicketStore = (*RedisMFATicketStore)(nil)

// NewRedisMFATicketStore 构造 Redis 票据存储；client 为 nil 时操作失败关闭。
func NewRedisMFATicketStore(client *redis.Cache) *RedisMFATicketStore {
	return &RedisMFATicketStore{client: client}
}

// Consume 使用 SETNX 原子消费票据；返回 false 表示票据已被使用。
func (s *RedisMFATicketStore) Consume(ctx context.Context, jti string, ttl time.Duration) (bool, error) {
	if strings.TrimSpace(jti) == "" {
		return false, apperr.BadRequest("mfa ticket id is required")
	}
	if ttl <= 0 {
		return false, nil
	}
	if s.client == nil {
		return false, apperr.Unavailable("mfa ticket store is not configured")
	}
	consumed, err := s.client.SetNX(ctx, mfaTicketKeyPrefix+jti, []byte("1"), ttl)
	if err != nil {
		return false, fmt.Errorf("identity mfa ticket consume: %w", err)
	}
	return consumed, nil
}
