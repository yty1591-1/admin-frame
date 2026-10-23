package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/admin-frame/admin-frame/pkg/ratelimit"
	"github.com/admin-frame/admin-frame/pkg/redis"
)

func newRedisKeyed(t *testing.T, cfg ratelimit.RedisKeyedConfig) (*ratelimit.RedisKeyed, *miniredis.Miniredis) {
	t.Helper()
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(server.Close)

	client, err := redis.Open(context.Background(), redis.Config{Addr: server.Addr(), KeyPrefix: "test:rl"})
	if err != nil {
		t.Fatalf("redis.Open: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	keyed, err := ratelimit.NewRedisKeyed(client, cfg)
	if err != nil {
		t.Fatalf("NewRedisKeyed: %v", err)
	}
	return keyed, server
}

// TestNewRedisKeyedValidates 验证缺少客户端或非法参数在装配期失败。
func TestNewRedisKeyedValidates(t *testing.T) {
	if _, err := ratelimit.NewRedisKeyed(nil, ratelimit.RedisKeyedConfig{RPS: 1, Burst: 1}); err == nil {
		t.Fatal("expected error for nil redis client")
	}
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer server.Close()
	client, err := redis.Open(context.Background(), redis.Config{Addr: server.Addr()})
	if err != nil {
		t.Fatalf("redis.Open: %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := ratelimit.NewRedisKeyed(client, ratelimit.RedisKeyedConfig{RPS: 0, Burst: 1}); err == nil {
		t.Fatal("expected error for rps <= 0")
	}
	if _, err := ratelimit.NewRedisKeyed(client, ratelimit.RedisKeyedConfig{
		RPS: 1, Burst: 1, FailureMode: "unknown",
	}); err == nil {
		t.Fatal("expected error for unsupported failure mode")
	}
}

// TestRedisKeyedSharesQuota 验证两副本共享同一配额，超限统一拒绝（AC-ENT-02-3）。
func TestRedisKeyedSharesQuota(t *testing.T) {
	cfg := ratelimit.RedisKeyedConfig{RPS: 0.001, Burst: 2, IdleTTL: time.Minute}
	first, server := newRedisKeyed(t, cfg)
	second, _ := newRedisKeyedOn(t, server, cfg)

	if result := first.Allow("channel"); !result.Allowed {
		t.Fatal("first request must be allowed")
	}
	if result := second.Allow("channel"); !result.Allowed {
		t.Fatalf("second request must be allowed: %+v", result)
	}
	if result := first.Allow("channel"); result.Allowed {
		t.Fatalf("third request must be rejected: %+v", result)
	}
	if result := second.Allow("channel"); result.Allowed || result.RetryAfter <= 0 {
		t.Fatalf("shared quota must reject on other replica: %+v", result)
	}
}

// newRedisKeyedOn 在既有 miniredis 上构造第二个副本客户端。
func newRedisKeyedOn(t *testing.T, server *miniredis.Miniredis, cfg ratelimit.RedisKeyedConfig) (*ratelimit.RedisKeyed, *redis.Client) {
	t.Helper()
	client, err := redis.Open(context.Background(), redis.Config{Addr: server.Addr(), KeyPrefix: "test:rl"})
	if err != nil {
		t.Fatalf("redis.Open: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	keyed, err := ratelimit.NewRedisKeyed(client, cfg)
	if err != nil {
		t.Fatalf("NewRedisKeyed: %v", err)
	}
	return keyed, client
}

// TestRedisKeyedFailureMode 验证 Redis 故障时按 failure_mode 放行或拒绝。
func TestRedisKeyedFailureMode(t *testing.T) {
	open, openServer := newRedisKeyed(t, ratelimit.RedisKeyedConfig{
		RPS: 1, Burst: 1, FailureMode: ratelimit.RedisFailureOpen,
	})
	openServer.Close()
	if result := open.Allow("channel"); !result.Allowed {
		t.Fatalf("fail_open must allow when redis is down: %+v", result)
	}

	closed, closedServer := newRedisKeyed(t, ratelimit.RedisKeyedConfig{
		RPS: 1, Burst: 1, FailureMode: ratelimit.RedisFailureClosed,
	})
	closedServer.Close()
	if result := closed.Allow("channel"); result.Allowed || result.RetryAfter <= 0 {
		t.Fatalf("fail_closed must reject when redis is down: %+v", result)
	}
}
