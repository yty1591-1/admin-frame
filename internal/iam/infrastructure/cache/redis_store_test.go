package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	pkgcache "github.com/admin-frame/admin-frame/pkg/cache"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// TestRedisStore 验证适配器的读写删闭环与 miss 哨兵转换。
func TestRedisStore(t *testing.T) {
	server := miniredis.RunT(t)
	client, err := redis.Open(context.Background(), redis.Config{Addr: server.Addr()})
	if err != nil {
		t.Fatalf("open redis: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store := iampcache.NewRedisStore(client.Cache())
	ctx := context.Background()
	key := "iam:cache:snapshot:1"

	if _, err := store.Get(ctx, key); !errors.Is(err, pkgcache.ErrMiss) {
		t.Fatalf("miss = %v, want pkg cache miss", err)
	}
	if err := store.Set(ctx, key, []byte("v1"), time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got, err := store.Get(ctx, key); err != nil || string(got) != "v1" {
		t.Fatalf("get = %q, %v, want v1", got, err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, key); !errors.Is(err, pkgcache.ErrMiss) {
		t.Fatalf("miss after delete = %v, want pkg cache miss", err)
	}

	// 非 miss 错误（连接已关闭）必须原样上抛，不能被误判为未命中。
	_ = client.Close()
	if _, err := store.Get(ctx, key); err == nil || errors.Is(err, pkgcache.ErrMiss) {
		t.Fatalf("closed client error = %v, want non-miss error", err)
	}
}

// TestRedisStoreInvalidConfig 验证 nil 适配器 / nil 底层缓存返回 ErrInvalidConfig。
func TestRedisStoreInvalidConfig(t *testing.T) {
	ctx := context.Background()
	var nilStore *iampcache.RedisStore
	if _, err := nilStore.Get(ctx, "k"); !errors.Is(err, pkgcache.ErrInvalidConfig) {
		t.Fatalf("nil get = %v, want invalid config", err)
	}
	if err := nilStore.Set(ctx, "k", nil, time.Minute); !errors.Is(err, pkgcache.ErrInvalidConfig) {
		t.Fatalf("nil set = %v, want invalid config", err)
	}
	if err := nilStore.Delete(ctx, "k"); !errors.Is(err, pkgcache.ErrInvalidConfig) {
		t.Fatalf("nil delete = %v, want invalid config", err)
	}

	store := iampcache.NewRedisStore(nil)
	if _, err := store.Get(ctx, "k"); !errors.Is(err, pkgcache.ErrInvalidConfig) {
		t.Fatalf("empty get = %v, want invalid config", err)
	}
	if err := store.Set(ctx, "k", nil, time.Minute); !errors.Is(err, pkgcache.ErrInvalidConfig) {
		t.Fatalf("empty set = %v, want invalid config", err)
	}
	if err := store.Delete(ctx, "k"); !errors.Is(err, pkgcache.ErrInvalidConfig) {
		t.Fatalf("empty delete = %v, want invalid config", err)
	}
}
