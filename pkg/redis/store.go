package redis

import (
	"context"
	"errors"
	"time"

	"github.com/admin-frame/admin-frame/pkg/cache"
)

// Store 将本包 Cache 适配为 pkg/cache.Store，供多级缓存组合。
func (c *Cache) Store() cache.Store {
	return redisStore{c: c}
}

type redisStore struct {
	c *Cache
}

// Get 读取 key 对应的原始字节；未命中时返回 cache.ErrMiss。
func (r redisStore) Get(ctx context.Context, key string) ([]byte, error) {
	val, err := r.c.Get(ctx, key)
	if err == nil {
		return val, nil
	}
	if errors.Is(err, ErrMiss) {
		return nil, cache.ErrMiss
	}
	return nil, err
}

// Set 写入 key，ttl 为 0 表示不过期。
func (r redisStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.c.Set(ctx, key, value, ttl)
}

// Delete 批量删除 key。
func (r redisStore) Delete(ctx context.Context, keys ...string) error {
	return r.c.Delete(ctx, keys...)
}

var _ cache.Store = redisStore{}
