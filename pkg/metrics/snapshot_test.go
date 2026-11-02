package metrics_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/metrics"
)

// TestSnapshotCacheCachesWithinTTL 断言 TTL 内复用同一份快照，不重复采集。
func TestSnapshotCacheCachesWithinTTL(t *testing.T) {
	var calls int32
	cache := metrics.NewSnapshotCache[int](time.Minute, func(context.Context) (int, error) {
		return int(atomic.AddInt32(&calls, 1)), nil
	})
	for i := 0; i < 5; i++ {
		value, fresh, err := cache.Get(context.Background())
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !fresh {
			t.Fatal("expected fresh snapshot")
		}
		if value != 1 {
			t.Fatalf("value=%d, want 1", value)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("load calls=%d, want 1", got)
	}
}

// TestSnapshotCacheReloadsAfterTTL 断言 TTL 过期后重新采集。
func TestSnapshotCacheReloadsAfterTTL(t *testing.T) {
	var calls int32
	cache := metrics.NewSnapshotCache[int](20*time.Millisecond, func(context.Context) (int, error) {
		return int(atomic.AddInt32(&calls, 1)), nil
	})
	if _, _, err := cache.Get(context.Background()); err != nil {
		t.Fatalf("get: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, _, err := cache.Get(context.Background()); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("load calls=%d, want 2", got)
	}
}

// TestSnapshotCacheKeepsLastGoodValueOnError 断言采集失败时沿用上一次成功快照，
// 而不是把积压显示成 0（ENT-06 T6：DB 池打满时「采不到」不等于「没有积压」）。
func TestSnapshotCacheKeepsLastGoodValueOnError(t *testing.T) {
	var fail atomic.Bool
	cache := metrics.NewSnapshotCache[int](time.Minute, func(context.Context) (int, error) {
		if fail.Load() {
			return 0, errors.New("db unavailable")
		}
		return 42, nil
	})
	value, fresh, err := cache.Get(context.Background())
	if err != nil || !fresh || value != 42 {
		t.Fatalf("first get = (%d, %v, %v)", value, fresh, err)
	}
	fail.Store(true)
	// TTL 内命中缓存（不重新采集），仍返回成功快照。
	value, fresh, err = cache.Get(context.Background())
	if err != nil || !fresh || value != 42 {
		t.Fatalf("cached get = (%d, %v, %v)", value, fresh, err)
	}
}

// TestSnapshotCacheThrottlesFailedLoad 断言采集失败后按 TTL 节流，
// 避免每个 Gauge 都去撞一次数据库超时（这正是 /metrics 超时的成因）。
func TestSnapshotCacheThrottlesFailedLoad(t *testing.T) {
	var calls int32
	cache := metrics.NewSnapshotCache[int](time.Minute, func(context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, errors.New("db unavailable")
	})
	for i := 0; i < 4; i++ {
		if _, fresh, err := cache.Get(context.Background()); err == nil || fresh {
			t.Fatalf("expected error and stale flag, got fresh=%v err=%v", fresh, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("load calls=%d, want 1 (failure must be throttled by TTL)", got)
	}
}

// TestSnapshotCacheWithoutLoader 断言缺少采集函数时返回错误而不是 panic。
func TestSnapshotCacheWithoutLoader(t *testing.T) {
	cache := metrics.NewSnapshotCache[int](time.Minute, nil)
	if _, _, err := cache.Get(context.Background()); err == nil {
		t.Fatal("expected error for missing loader")
	}
}
