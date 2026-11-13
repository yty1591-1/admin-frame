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

// TestRedisKeyedDegradesAfterConsecutiveFailures 验证 P1-4：连续故障后熔断降级。
func TestRedisKeyedDegradesAfterConsecutiveFailures(t *testing.T) {
	keyed, server := newRedisKeyed(t, ratelimit.RedisKeyedConfig{
		RPS:              1,
		Burst:            1,
		FailureMode:      ratelimit.RedisFailureClosed,
		DegradeOnFailure: true,
		DegradeAfter:     3,
		DegradeCooldown:  time.Minute,
	})
	server.Close()

	// 前两次仍失败关闭，第三次起进入降级窗口：放行并带 Degraded 标记。
	for i := 1; i <= 5; i++ {
		result := keyed.Allow("channel")
		if i < 3 {
			if result.Allowed {
				t.Fatalf("iteration %d must stay fail_closed before threshold: %+v", i, result)
			}
			continue
		}
		if !result.Allowed || !result.Degraded {
			t.Fatalf("iteration %d must degrade and allow: %+v", i, result)
		}
	}
}

// TestRedisKeyedConfigureAppliesToNextRequest 验证热更参数对下一个请求生效，
// 且不清空已存在的键（令牌延续，不重置为满桶）。
//
// 断言用 RetryAfter 反推生效速率：同一个「缺 1 个令牌」的请求，rps=1 时约 1s，
// rps=100 时约 10ms。若实现改了参数但重置了桶，第二个请求会直接放行。
func TestRedisKeyedConfigureAppliesToNextRequest(t *testing.T) {
	keyed, _ := newRedisKeyed(t, ratelimit.RedisKeyedConfig{RPS: 1, Burst: 1, IdleTTL: time.Minute})

	if !keyed.Allow("k").Allowed {
		t.Fatal("first request must pass")
	}
	limited := keyed.Allow("k")
	if limited.Allowed {
		t.Fatal("second request must be limited at rps=1 burst=1")
	}
	if limited.RetryAfter < 900*time.Millisecond {
		t.Fatalf("retry after = %v, want ~1s at rps=1", limited.RetryAfter)
	}

	// 热更：rps 提升到 100，burst 100。
	if err := keyed.Configure(ratelimit.Params{RPS: 100, Burst: 100}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	heated := keyed.Allow("k")
	if heated.Allowed {
		t.Fatal("hot change must not refill the bucket")
	}
	if heated.RetryAfter > 100*time.Millisecond {
		t.Fatalf("retry after = %v, want ~10ms at rps=100 (hot rps must apply)", heated.RetryAfter)
	}
}

// TestRedisKeyedConfigureRejectsInvalidParams 验证非法热更值被拒绝且不改动既有参数。
func TestRedisKeyedConfigureRejectsInvalidParams(t *testing.T) {
	keyed, _ := newRedisKeyed(t, ratelimit.RedisKeyedConfig{RPS: 10, Burst: 5})
	if err := keyed.Configure(ratelimit.Params{RPS: 0, Burst: 5}); err == nil {
		t.Fatal("rps=0 must be rejected")
	}
	if err := keyed.Configure(ratelimit.Params{RPS: 10, Burst: 0}); err == nil {
		t.Fatal("burst=0 must be rejected")
	}
	// 参数未变：原配置仍生效。
	if !keyed.Allow("k").Allowed {
		t.Fatal("rejected Configure must not break the limiter")
	}
}
