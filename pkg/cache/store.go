package cache

import (
	"context"
	"time"
)

// Store 是后端无关的字节 KV 缓存接口（本地、Redis、Multi 均实现）。
//
// Get 未命中须返回 ErrMiss（可用 errors.Is）；不得返回裸 nil error 表示缺失。
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
}

// Loader 在未命中时从真相源加载（通常为 DB）。
type Loader func(ctx context.Context) ([]byte, error)

// FuncStore 用函数适配任意后端，便于测试或薄包装。
type FuncStore struct {
	GetFunc    func(ctx context.Context, key string) ([]byte, error)
	SetFunc    func(ctx context.Context, key string, value []byte, ttl time.Duration) error
	DeleteFunc func(ctx context.Context, keys ...string) error
}

// Get 实现 Store。
func (f FuncStore) Get(ctx context.Context, key string) ([]byte, error) {
	if f.GetFunc == nil {
		return nil, ErrMiss
	}
	return f.GetFunc(ctx, key)
}

// Set 实现 Store。
func (f FuncStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if f.SetFunc == nil {
		return ErrInvalidConfig
	}
	return f.SetFunc(ctx, key, value, ttl)
}

// Delete 实现 Store。
func (f FuncStore) Delete(ctx context.Context, keys ...string) error {
	if f.DeleteFunc == nil {
		return nil
	}
	return f.DeleteFunc(ctx, keys...)
}
