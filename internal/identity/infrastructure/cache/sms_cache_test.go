package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// testClock 是可推进的测试时钟。
type testClock struct{ now time.Time }

// Now 返回当前测试时间。
func (c *testClock) Now() time.Time { return c.now }

// newRedisCache 启动 miniredis 并返回缓存客户端。
func newRedisCache(t *testing.T) (*redis.Cache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client, err := redis.Open(context.Background(), redis.Config{Addr: mr.Addr(), KeyPrefix: "test:sms"})
	if err != nil {
		t.Fatalf("redis open: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client.Cache(), mr
}

func TestSMSCodeStore(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name  string
		build func(t *testing.T, clock *testClock) (app.SMSCodeStore, func(time.Duration))
	}{
		{
			name: "memory",
			build: func(_ *testing.T, clock *testClock) (app.SMSCodeStore, func(time.Duration)) {
				return NewMemorySMSCodeStore(clock), clock.advance
			},
		},
		{
			name: "redis",
			build: func(t *testing.T, _ *testClock) (app.SMSCodeStore, func(time.Duration)) {
				client, mr := newRedisCache(t)
				return NewRedisSMSCodeStore(client), mr.FastForward
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &testClock{now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
			store, advance := tc.build(t, clock)

			if _, err := store.Get(ctx, "hash-1"); !errors.Is(err, apperr.ErrNotFound) {
				t.Fatalf("missing get err = %v, want NotFound", err)
			}
			if err := store.Save(ctx, "hash-1", "code-hash", time.Minute); err != nil {
				t.Fatalf("save: %v", err)
			}
			got, err := store.Get(ctx, "hash-1")
			if err != nil || got != "code-hash" {
				t.Fatalf("get = %q, %v", got, err)
			}

			for want := 1; want <= 3; want++ {
				attempts, err := store.IncrAttempts(ctx, "hash-1", time.Minute)
				if err != nil || attempts != want {
					t.Fatalf("attempts = %d, %v; want %d", attempts, err, want)
				}
			}
			if err := store.ResetAttempts(ctx, "hash-1"); err != nil {
				t.Fatalf("reset: %v", err)
			}
			if attempts, err := store.IncrAttempts(ctx, "hash-1", time.Minute); err != nil || attempts != 1 {
				t.Fatalf("attempts after reset = %d, %v; want 1", attempts, err)
			}

			if err := store.Delete(ctx, "hash-1"); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if _, err := store.Get(ctx, "hash-1"); !errors.Is(err, apperr.ErrNotFound) {
				t.Fatalf("get after delete err = %v, want NotFound", err)
			}

			if err := store.Save(ctx, "hash-2", "code-hash", time.Minute); err != nil {
				t.Fatalf("save expiring: %v", err)
			}
			advance(2 * time.Minute)
			if _, err := store.Get(ctx, "hash-2"); !errors.Is(err, apperr.ErrNotFound) {
				t.Fatalf("expired get err = %v, want NotFound", err)
			}

			if err := store.Save(ctx, " ", "code-hash", time.Minute); err == nil {
				t.Fatal("blank key must be rejected")
			}
			if err := store.Save(ctx, "hash-3", " ", time.Minute); err == nil {
				t.Fatal("blank code hash must be rejected")
			}
			if err := store.Save(ctx, "hash-3", "code-hash", 0); err == nil {
				t.Fatal("zero ttl must be rejected")
			}
		})
	}
}

// TestSMSDailyQuotaTimezone 验证日配额按配置时区分桶（BIZ-05）。
func TestSMSDailyQuotaTimezone(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	ctx := context.Background()
	// 2026-01-01 15:30 UTC = 上海 23:30；一小时后 = 上海次日 00:30。
	first := time.Date(2026, 1, 1, 15, 30, 0, 0, time.UTC)
	clock := &testClock{now: first}

	local := NewMemorySMSRateLimiterWithLocation(clock, shanghai)
	if ok, err := local.AllowDaily(ctx, "phone-1", 1); err != nil || !ok {
		t.Fatalf("first local daily = %v, err=%v", ok, err)
	}
	utc := NewMemorySMSRateLimiterWithLocation(clock, time.UTC)
	if ok, err := utc.AllowDaily(ctx, "phone-1", 1); err != nil || !ok {
		t.Fatalf("first utc daily = %v, err=%v", ok, err)
	}

	clock.now = first.Add(time.Hour)
	// 上海已跨日：允许第二次；UTC 仍同日：拒绝。
	if ok, err := local.AllowDaily(ctx, "phone-1", 1); err != nil || !ok {
		t.Fatalf("second local daily = %v, err=%v, want allowed after local midnight", ok, err)
	}
	if ok, _ := utc.AllowDaily(ctx, "phone-1", 1); ok {
		t.Fatal("utc bucket must reject second send before UTC midnight")
	}
}

func TestSMSRateLimiter(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name  string
		build func(t *testing.T, clock *testClock) (app.SMSRateLimiter, func(time.Duration))
	}{
		{
			name: "memory",
			build: func(_ *testing.T, clock *testClock) (app.SMSRateLimiter, func(time.Duration)) {
				return NewMemorySMSRateLimiter(clock), clock.advance
			},
		},
		{
			name: "redis",
			build: func(t *testing.T, clock *testClock) (app.SMSRateLimiter, func(time.Duration)) {
				client, mr := newRedisCache(t)
				return NewRedisSMSRateLimiter(client, clock), mr.FastForward
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &testClock{now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
			limiter, advance := tc.build(t, clock)

			allowed, retryAfter, err := limiter.AllowResend(ctx, "phone-1", 60*time.Second)
			if err != nil || !allowed || retryAfter != 0 {
				t.Fatalf("first resend = %v, %v, %v", allowed, retryAfter, err)
			}
			allowed, retryAfter, err = limiter.AllowResend(ctx, "phone-1", 60*time.Second)
			if err != nil || allowed || retryAfter <= 0 {
				t.Fatalf("second resend = %v, %v, %v", allowed, retryAfter, err)
			}
			advance(61 * time.Second)
			if allowed, _, err = limiter.AllowResend(ctx, "phone-1", 60*time.Second); err != nil || !allowed {
				t.Fatalf("resend after interval = %v, %v", allowed, err)
			}

			for i := 0; i < 2; i++ {
				if ok, err := limiter.AllowDaily(ctx, "phone-2", 2); err != nil || !ok {
					t.Fatalf("daily attempt %d = %v, %v", i, ok, err)
				}
			}
			if ok, err := limiter.AllowDaily(ctx, "phone-2", 2); err != nil || ok {
				t.Fatalf("daily over limit = %v, %v", ok, err)
			}

			if ok, err := limiter.AllowHourlyIP(ctx, "ip-1", 1); err != nil || !ok {
				t.Fatalf("hourly ip first = %v, %v", ok, err)
			}
			if ok, err := limiter.AllowHourlyIP(ctx, "ip-1", 1); err != nil || ok {
				t.Fatalf("hourly ip over limit = %v, %v", ok, err)
			}

			if _, _, err := limiter.AllowResend(ctx, " ", time.Second); err == nil {
				t.Fatal("blank key must be rejected")
			}
			if _, err := limiter.AllowDaily(ctx, "phone-3", 0); err == nil {
				t.Fatal("zero limit must be rejected")
			}

			unconfigured := NewRedisSMSRateLimiter(nil, clock)
			if _, _, err := unconfigured.AllowResend(ctx, "phone-4", time.Second); err == nil {
				t.Fatal("nil client must fail closed")
			}
		})
	}
}

func TestOAuthStateStore(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name  string
		build func(t *testing.T, clock *testClock) (app.OAuthStateStore, func(time.Duration))
	}{
		{
			name: "memory",
			build: func(_ *testing.T, clock *testClock) (app.OAuthStateStore, func(time.Duration)) {
				return NewMemoryOAuthStateStore(clock), clock.advance
			},
		},
		{
			name: "redis",
			build: func(t *testing.T, _ *testClock) (app.OAuthStateStore, func(time.Duration)) {
				client, mr := newRedisCache(t)
				return NewRedisOAuthStateStore(client), mr.FastForward
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &testClock{now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
			store, advance := tc.build(t, clock)
			value := app.OAuthState{Provider: "test", Verifier: "verifier-1"}

			if err := store.Save(ctx, "state-1", value, 10*time.Minute); err != nil {
				t.Fatalf("save: %v", err)
			}
			got, err := store.Consume(ctx, "state-1")
			if err != nil || got != value {
				t.Fatalf("consume = %+v, %v", got, err)
			}
			if _, err := store.Consume(ctx, "state-1"); !errors.Is(err, apperr.ErrNotFound) {
				t.Fatalf("replay err = %v, want NotFound", err)
			}

			if err := store.Save(ctx, "state-2", value, 10*time.Minute); err != nil {
				t.Fatalf("save expiring: %v", err)
			}
			advance(11 * time.Minute)
			if _, err := store.Consume(ctx, "state-2"); !errors.Is(err, apperr.ErrNotFound) {
				t.Fatalf("expired err = %v, want NotFound", err)
			}

			if err := store.Save(ctx, " ", value, time.Minute); err == nil {
				t.Fatal("blank state must be rejected")
			}
			if err := store.Save(ctx, "state-3", value, 0); err == nil {
				t.Fatal("zero ttl must be rejected")
			}

			unconfigured := NewRedisOAuthStateStore(nil)
			if _, err := unconfigured.Consume(ctx, "state-4"); err == nil {
				t.Fatal("nil client must fail closed")
			}
		})
	}
}

// advance 是 testClock 的推进方法。
func (c *testClock) advance(d time.Duration) { c.now = c.now.Add(d) }
