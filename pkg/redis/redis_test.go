package redis_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/admin-frame/admin-frame/pkg/cache"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

func startMini(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)

	c, err := redis.Open(context.Background(), redis.Config{
		Addr:      mr.Addr(),
		KeyPrefix: "test:admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return mr, c
}

func TestOpenValidate(t *testing.T) {
	if _, err := redis.Open(context.Background(), redis.Config{}); !errors.Is(err, redis.ErrInvalidConfig) {
		t.Fatalf("err=%v", err)
	}
}

func TestPingCheckerClose(t *testing.T) {
	_, c := startMini(t)
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Checker()(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(ctx); !errors.Is(err, redis.ErrClosed) {
		t.Fatalf("err=%v", err)
	}
}

func TestKeyHelpers(t *testing.T) {
	got := redis.Key("prod", "biz", "m1", "order", "42")
	if got != "prod:biz:m1:order:42" {
		t.Fatalf("got=%s", got)
	}
	if redis.JoinPrefix("prod:admin", "order:1") != "prod:admin:order:1" {
		t.Fatal("JoinPrefix")
	}
	if redis.ValidKeySegment("a b") {
		t.Fatal("space should be invalid")
	}
}

func TestCacheRoundTripAndMiss(t *testing.T) {
	_, c := startMini(t)
	ctx := context.Background()
	cacheStore := c.Cache()

	if _, err := cacheStore.Get(ctx, "k1"); !errors.Is(err, redis.ErrMiss) {
		t.Fatalf("err=%v", err)
	}
	if err := cacheStore.Set(ctx, "k1", []byte("v1"), time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := cacheStore.Get(ctx, "k1")
	if err != nil || string(got) != "v1" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if err := cacheStore.Set(ctx, "k2", []byte("x"), 0); !errors.Is(err, redis.ErrInvalidConfig) {
		t.Fatalf("ttl err=%v", err)
	}
	if err := cacheStore.Delete(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.Get(ctx, "k1"); !errors.Is(err, redis.ErrMiss) {
		t.Fatalf("err=%v", err)
	}
}

func TestCacheGetOrLoadCoalesce(t *testing.T) {
	_, c := startMini(t)
	ctx := context.Background()
	cacheStore := c.Cache()

	var loads atomic.Int32
	var wg sync.WaitGroup
	const n = 20
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			val, err := cacheStore.GetOrLoad(ctx, "hot", time.Minute, func(_ context.Context) ([]byte, error) {
				loads.Add(1)
				time.Sleep(20 * time.Millisecond)
				return []byte("loaded"), nil
			})
			if err != nil || string(val) != "loaded" {
				t.Errorf("val=%q err=%v", val, err)
			}
		}()
	}
	wg.Wait()
	if loads.Load() != 1 {
		t.Fatalf("loads=%d want 1", loads.Load())
	}
}

func TestLockAcquireUnlockRenew(t *testing.T) {
	_, c := startMini(t)
	ctx := context.Background()
	locker := c.Lock()

	lock, err := locker.Acquire(ctx, "order:1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locker.Acquire(ctx, "order:1", time.Second); !errors.Is(err, redis.ErrNotObtained) {
		t.Fatalf("err=%v", err)
	}
	if err := lock.Renew(ctx, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(ctx); !errors.Is(err, redis.ErrLockNotHeld) {
		t.Fatalf("err=%v", err)
	}

	lock2, err := locker.Acquire(ctx, "order:1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = lock2.Unlock(ctx)
}

func TestLockTTLExpiry(t *testing.T) {
	mr, c := startMini(t)
	ctx := context.Background()
	lock, err := c.Lock().Acquire(ctx, "short", 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	mr.FastForward(100 * time.Millisecond)
	if _, err := c.Lock().Acquire(ctx, "short", time.Second); err != nil {
		t.Fatalf("should obtain after expiry: %v", err)
	}
	_ = lock // expired
}

func TestPrefixApplied(t *testing.T) {
	mr, c := startMini(t)
	ctx := context.Background()
	if err := c.Cache().Set(ctx, "x", []byte("1"), time.Minute); err != nil {
		t.Fatal(err)
	}
	keys := mr.Keys()
	found := false
	for _, k := range keys {
		if k == "test:admin:cache:x" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("keys=%v", keys)
	}
}

func TestCacheIncr(t *testing.T) {
	mr, c := startMini(t)
	ctx := context.Background()
	cacheStore := c.Cache()

	for want := int64(1); want <= 3; want++ {
		got, err := cacheStore.Incr(ctx, "cnt", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("got=%d want=%d", got, want)
		}
	}
	if ttl := mr.TTL("test:admin:cache:cnt"); ttl != time.Minute {
		t.Fatalf("ttl=%v want 1m", ttl)
	}
	// 已存在的 key 不重置 TTL。
	mr.FastForward(30 * time.Second)
	if _, err := cacheStore.Incr(ctx, "cnt", time.Minute); err != nil {
		t.Fatal(err)
	}
	if ttl := mr.TTL("test:admin:cache:cnt"); ttl != 30*time.Second {
		t.Fatalf("ttl=%v want 30s", ttl)
	}
	if _, err := cacheStore.Incr(ctx, "cnt", 0); !errors.Is(err, redis.ErrInvalidConfig) {
		t.Fatalf("ttl err=%v", err)
	}
	if _, err := cacheStore.Incr(ctx, "", time.Minute); !errors.Is(err, redis.ErrInvalidConfig) {
		t.Fatalf("key err=%v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.Incr(ctx, "cnt", time.Minute); !errors.Is(err, redis.ErrClosed) {
		t.Fatalf("closed err=%v", err)
	}
}

func TestCacheStoreAdapter(t *testing.T) {
	_, c := startMini(t)
	ctx := context.Background()
	store := c.Cache().Store()
	if err := store.Set(ctx, "s1", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "s1")
	if err != nil || string(got) != "v" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := store.Get(ctx, "missing"); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("err=%v", err)
	}
}
