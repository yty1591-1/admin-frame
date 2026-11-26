package ratelimit_test

import (
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

// stubParams 是可变的参数提供者，模拟配置中心热更推送。
type stubParams struct {
	params  ratelimit.Params
	version int64
	ok      bool
}

func (s *stubParams) Params() (ratelimit.Params, int64, bool) {
	return s.params, s.version, s.ok
}

func (s *stubParams) set(params ratelimit.Params) {
	s.params = params
	s.version++
	s.ok = true
}

// TestAdaptiveKeepsTokensAcrossParamChange 守护 ADR-0015 的核心语义：
// 参数变更只调整既有桶，不把桶重置为满桶。
//
// 若实现改成「重建桶」，上调 burst 就会凭空放出 20 个请求，
// 一次热更等于一次额外的突发放行。
func TestAdaptiveKeepsTokensAcrossParamChange(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	source := &stubParams{}
	source.set(ratelimit.Params{RPS: 1, Burst: 10})
	registry, err := ratelimit.NewAdaptive(source, ratelimit.WithAdaptiveClock(clock))
	if err != nil {
		t.Fatalf("NewAdaptive: %v", err)
	}

	for i := 0; i < 10; i++ {
		if res := registry.Allow("tenant-a"); !res.Allowed {
			t.Fatalf("request %d must pass with burst=10", i+1)
		}
	}
	if res := registry.Allow("tenant-a"); res.Allowed {
		t.Fatal("11th request must be limited before refill")
	}

	// 上调容量：已消耗的令牌不退还，因此仍然是拒绝。
	source.set(ratelimit.Params{RPS: 1, Burst: 20})
	if res := registry.Allow("tenant-a"); res.Allowed {
		t.Fatal("raising burst must not refill the bucket")
	}

	// 推进 5s → 按新速率补充 5 个令牌，应放行 5 次后再次拒绝。
	clock.Advance(5 * time.Second)
	for i := 0; i < 5; i++ {
		if res := registry.Allow("tenant-a"); !res.Allowed {
			t.Fatalf("refilled request %d must pass", i+1)
		}
	}
	if res := registry.Allow("tenant-a"); res.Allowed {
		t.Fatal("bucket must be exhausted again after 5 refills")
	}
}

// TestAdaptiveLoweringBurstClamps 验证容量下调时截断当前令牌，不产生超发。
func TestAdaptiveLoweringBurstClamps(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	source := &stubParams{}
	source.set(ratelimit.Params{RPS: 1, Burst: 10})
	registry, err := ratelimit.NewAdaptive(source, ratelimit.WithAdaptiveClock(clock))
	if err != nil {
		t.Fatalf("NewAdaptive: %v", err)
	}

	// 满桶 10 个令牌；下调到 2 后只应放行 2 次。
	source.set(ratelimit.Params{RPS: 1, Burst: 2})
	allowed := 0
	for i := 0; i < 5; i++ {
		if registry.Allow("k").Allowed {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("allowed = %d, want 2 after burst lowered to 2", allowed)
	}
}

// TestAdaptiveIgnoresSameVersion 验证同版本不重复配置。
func TestAdaptiveIgnoresSameVersion(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	source := &stubParams{params: ratelimit.Params{RPS: 1, Burst: 1}, version: 7, ok: true}
	registry, err := ratelimit.NewAdaptive(source, ratelimit.WithAdaptiveClock(clock))
	if err != nil {
		t.Fatalf("NewAdaptive: %v", err)
	}
	if !registry.Allow("k").Allowed {
		t.Fatal("first request must pass")
	}
	// 版本不变：不应触发重配置，桶仍是空的。
	if registry.Allow("k").Allowed {
		t.Fatal("same version must not refill the bucket")
	}
}

// TestAdaptiveFallsBackToLastKnownParams 验证参数源短暂不可用时不误伤流量。
func TestAdaptiveFallsBackToLastKnownParams(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	source := &stubParams{}
	source.set(ratelimit.Params{RPS: 100, Burst: 5})
	registry, err := ratelimit.NewAdaptive(source, ratelimit.WithAdaptiveClock(clock))
	if err != nil {
		t.Fatalf("NewAdaptive: %v", err)
	}
	if !registry.Allow("k").Allowed {
		t.Fatal("warm-up request must pass")
	}

	source.ok = false
	clock.Advance(time.Second)
	// 参数源不可用：沿用上一份参数（rps=100），仍有额度。
	if res := registry.Allow("k"); !res.Allowed {
		t.Fatalf("fallback to last params must allow, got %+v", res)
	}
}

// TestAdaptiveDeniesWithoutAnyParams 验证首次就取不到参数时拒绝而不是放行。
func TestAdaptiveDeniesWithoutAnyParams(t *testing.T) {
	source := &stubParams{ok: false}
	registry, err := ratelimit.NewAdaptive(source)
	if err != nil {
		t.Fatalf("NewAdaptive: %v", err)
	}
	res := registry.Allow("k")
	if res.Allowed {
		t.Fatal("missing params must not fail open")
	}
	if res.RetryAfter <= 0 {
		t.Fatal("denial must carry RetryAfter")
	}
}

// TestAdaptiveSweepsIdleKeys 验证空闲桶按热更后的 IdleTTL 回收。
func TestAdaptiveSweepsIdleKeys(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	source := &stubParams{}
	source.set(ratelimit.Params{RPS: 1, Burst: 1, IdleTTL: time.Minute})
	registry, err := ratelimit.NewAdaptive(source, ratelimit.WithAdaptiveClock(clock))
	if err != nil {
		t.Fatalf("NewAdaptive: %v", err)
	}
	registry.Allow("a")
	registry.Allow("b")
	if registry.Len() != 2 {
		t.Fatalf("len = %d, want 2", registry.Len())
	}

	clock.Advance(2 * time.Minute)
	registry.Allow("c")
	if registry.Len() != 1 {
		t.Fatalf("len = %d, want 1 after idle sweep", registry.Len())
	}
}

// TestTokenBucketConfigureValidates 验证非法参数不改动既有桶。
func TestTokenBucketConfigureValidates(t *testing.T) {
	bucket, err := ratelimit.NewTokenBucket(1, 5)
	if err != nil {
		t.Fatalf("NewTokenBucket: %v", err)
	}
	if err := bucket.Configure(0, 5); err == nil {
		t.Fatal("rps=0 must be rejected")
	}
	if err := bucket.Configure(1, 0); err == nil {
		t.Fatal("burst=0 must be rejected")
	}
	if bucket.Rate() != 1 || bucket.Burst() != 5 {
		t.Fatalf("rejected Configure must not mutate bucket: rate=%v burst=%d", bucket.Rate(), bucket.Burst())
	}
}
