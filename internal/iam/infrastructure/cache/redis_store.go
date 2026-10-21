package cache

import (
	"context"
	"errors"
	"time"

	pkgcache "github.com/admin-frame/admin-frame/pkg/cache"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// RedisStore 把 pkg/redis.Cache 适配为 pkg/cache.Store。
//
// 两个包的 miss 哨兵不同（redis.ErrMiss / cache.ErrMiss），必须在此显式转换，
// 否则快照缓存会把「未命中」当成「读错误」，指标与降级路径都会失真。
type RedisStore struct {
	cache *redis.Cache
}

// NewRedisStore 构造适配器；cache 为 nil 时所有操作返回 ErrInvalidConfig。
func NewRedisStore(c *redis.Cache) *RedisStore {
	return &RedisStore{cache: c}
}

// Get 读取原始字节；未命中返回 pkg/cache.ErrMiss。
func (s *RedisStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.cache == nil {
		return nil, pkgcache.ErrInvalidConfig
	}
	raw, err := s.cache.Get(ctx, key)
	if err != nil {
		if errors.Is(err, redis.ErrMiss) {
			return nil, pkgcache.ErrMiss
		}
		return nil, err
	}
	return raw, nil
}

// Set 写入原始字节。
func (s *RedisStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s == nil || s.cache == nil {
		return pkgcache.ErrInvalidConfig
	}
	return s.cache.Set(ctx, key, value, ttl)
}

// Delete 删除一个或多个 key。
func (s *RedisStore) Delete(ctx context.Context, keys ...string) error {
	if s == nil || s.cache == nil {
		return pkgcache.ErrInvalidConfig
	}
	return s.cache.Delete(ctx, keys...)
}

// 编译期断言。
var _ pkgcache.Store = (*RedisStore)(nil)
