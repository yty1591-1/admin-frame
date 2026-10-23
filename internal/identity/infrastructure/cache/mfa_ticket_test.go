package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

// requireAppCode 断言错误携带指定业务数字码。
func requireAppCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %d, got nil", want)
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error %v is not *apperr.AppError", err)
	}
	if appErr.Code != want {
		t.Fatalf("error code = %d, want %d (%v)", appErr.Code, want, err)
	}
}

// TestMemoryMFATicketStoreConsume 表驱动覆盖进程内票据的一次性消费与过期回收。
func TestMemoryMFATicketStoreConsume(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMFATicketStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }

	if ok, err := store.Consume(ctx, "jti-1", time.Minute); err != nil || !ok {
		t.Fatalf("first consume = %v, %v; want true, nil", ok, err)
	}
	if ok, err := store.Consume(ctx, "jti-1", time.Minute); err != nil || ok {
		t.Fatalf("replay consume = %v, %v; want false, nil", ok, err)
	}

	// 票据过期后同一 JTI 可再次消费（旧记录被回收）。
	now = now.Add(2 * time.Minute)
	if ok, err := store.Consume(ctx, "jti-1", time.Minute); err != nil || !ok {
		t.Fatalf("expired consume = %v, %v; want true, nil", ok, err)
	}

	cases := []struct {
		name string
		jti  string
		ttl  time.Duration
		want bool
		code int
	}{
		{name: "empty jti", jti: "", ttl: time.Minute, want: false, code: apperr.CodeBadRequest},
		{name: "zero ttl", jti: "jti-2", ttl: 0, want: false},
		{name: "negative ttl", jti: "jti-3", ttl: -time.Second, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := store.Consume(ctx, tc.jti, tc.ttl)
			if ok != tc.want {
				t.Fatalf("consume = %v, want %v", ok, tc.want)
			}
			if tc.code != 0 {
				requireAppCode(t, err, tc.code)
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// newRedisTicketStore 启动 miniredis 并构造 Redis 票据存储。
func newRedisTicketStore(t *testing.T) (*RedisMFATicketStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client, err := redis.Open(context.Background(), redis.Config{Addr: mr.Addr(), KeyPrefix: "test:identity"})
	if err != nil {
		t.Fatalf("redis open: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisMFATicketStore(client.Cache()), mr
}

// TestRedisMFATicketStoreConsume 验证 SETNX 的一次性语义、过期与错误分支。
func TestRedisMFATicketStoreConsume(t *testing.T) {
	ctx := context.Background()
	store, mr := newRedisTicketStore(t)

	if ok, err := store.Consume(ctx, "jti-1", time.Minute); err != nil || !ok {
		t.Fatalf("first consume = %v, %v; want true, nil", ok, err)
	}
	if ok, err := store.Consume(ctx, "jti-1", time.Minute); err != nil || ok {
		t.Fatalf("replay consume = %v, %v; want false, nil", ok, err)
	}

	// 键到期后可再次消费，验证 TTL 生效。
	mr.FastForward(2 * time.Minute)
	if ok, err := store.Consume(ctx, "jti-1", time.Minute); err != nil || !ok {
		t.Fatalf("consume after expiry = %v, %v; want true, nil", ok, err)
	}

	if ok, err := store.Consume(ctx, " ", time.Minute); ok {
		t.Fatal("blank jti must not consume")
	} else {
		requireAppCode(t, err, apperr.CodeBadRequest)
	}
	if ok, err := store.Consume(ctx, "jti-2", 0); err != nil || ok {
		t.Fatalf("zero ttl = %v, %v; want false, nil", ok, err)
	}

	// 未配置客户端时必须失败关闭（50300）。
	unconfigured := NewRedisMFATicketStore(nil)
	if ok, err := unconfigured.Consume(ctx, "jti-3", time.Minute); ok {
		t.Fatal("nil client must not consume")
	} else {
		requireAppCode(t, err, apperr.CodeServiceUnavailable)
	}
}
