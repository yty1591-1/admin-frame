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

// failureKeyPrefix 登录失败窗口计数键：authn:fail:<channel>:login_fail:<account_ref>。
const failureKeyPrefix = "authn:fail:"

// RedisLoginFailureCounter 基于 Redis INCR + EXPIRE 实现跨副本共享的失败计数。
//
// 计数与过期在同一 Lua 脚本内完成（见 pkg/redis.Cache.Incr），多副本并发不会漏判；
// 键在窗口结束后自然过期，登录成功时由守卫主动删除。
type RedisLoginFailureCounter struct {
	cache *redis.Cache
}

// 编译期断言：实现必须满足应用层计数端口。
var _ app.LoginFailureCounter = (*RedisLoginFailureCounter)(nil)

// NewRedisLoginFailureCounter 构造 Redis 失败计数器；cache 为 nil 时每次调用都失败并由守卫回退数据库。
func NewRedisLoginFailureCounter(c *redis.Cache) *RedisLoginFailureCounter {
	return &RedisLoginFailureCounter{cache: c}
}

// Increment 在窗口内自增一次失败并返回最新计数。
func (c *RedisLoginFailureCounter) Increment(ctx context.Context, key string, window time.Duration) (int64, error) {
	if strings.TrimSpace(key) == "" {
		return 0, apperr.BadRequest("login failure key is required")
	}
	if window <= 0 {
		return 0, apperr.BadRequest("login failure window must be positive")
	}
	if c == nil || c.cache == nil {
		return 0, fmt.Errorf("identity login failure counter: cache is not configured")
	}
	count, err := c.cache.Incr(ctx, failureKeyPrefix+key, window)
	if err != nil {
		return 0, fmt.Errorf("identity login failure counter incr: %w", err)
	}
	return count, nil
}

// Reset 清除窗口计数；不存在也返回成功。
func (c *RedisLoginFailureCounter) Reset(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return apperr.BadRequest("login failure key is required")
	}
	if c == nil || c.cache == nil {
		return fmt.Errorf("identity login failure counter: cache is not configured")
	}
	if err := c.cache.Delete(ctx, failureKeyPrefix+key); err != nil {
		return fmt.Errorf("identity login failure counter reset: %w", err)
	}
	return nil
}
