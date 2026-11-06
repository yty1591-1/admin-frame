package cache

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// tunableClock 是可推进的测试时钟。
type tunableClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now 返回当前时间。
func (c *tunableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 向前推进时长。
func (c *tunableClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestMemorySAMLReplayStoreDeduplicates 校验同一断言在窗口内只能占用一次。
func TestMemorySAMLReplayStoreDeduplicates(t *testing.T) {
	t.Parallel()
	clock := &tunableClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	store := NewMemorySAMLReplayStore(clock)
	ctx := context.Background()
	if err := store.Reserve(ctx, "test", "hash-1", 10*time.Minute); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	err := store.Reserve(ctx, "test", "hash-1", 10*time.Minute)
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	// 窗口过期后允许再次占用。
	clock.Advance(11 * time.Minute)
	if err := store.Reserve(ctx, "test", "hash-1", 10*time.Minute); err != nil {
		t.Fatalf("reserve after expiry: %v", err)
	}
}

// TestMemorySAMLReplayStoreValidatesTTL 校验非正 TTL 直接拒绝。
func TestMemorySAMLReplayStoreValidatesTTL(t *testing.T) {
	t.Parallel()
	store := NewMemorySAMLReplayStore(nil)
	if err := store.Reserve(context.Background(), "test", "hash-1", 0); err == nil {
		t.Fatalf("expected ttl error")
	}
}

// TestRedisSAMLReplayStoreUnavailableFailsClosed 校验无 Redis 时失败关闭。
func TestRedisSAMLReplayStoreUnavailableFailsClosed(t *testing.T) {
	t.Parallel()
	store := NewRedisSAMLReplayStore(nil)
	err := store.Reserve(context.Background(), "test", "hash-1", time.Minute)
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

// 确保测试时钟实现 app.Clock。
var _ app.Clock = (*tunableClock)(nil)
