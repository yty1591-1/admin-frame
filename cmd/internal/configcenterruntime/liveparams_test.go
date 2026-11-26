package configcenterruntime

import (
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/platform"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/hotconfig"
)

// newViewFrom 按白名单声明构建视图，模拟一次快照刷新。
func newViewFrom(t *testing.T, values map[string]string, version int64) *hotconfig.View {
	t.Helper()
	specs, err := spec.HotSpecs()
	if err != nil {
		t.Fatalf("HotSpecs: %v", err)
	}
	view, err := hotconfig.Build(specs, values, version, version, time.Now())
	if err != nil {
		t.Fatalf("hotconfig.Build: %v", err)
	}
	return view
}

func staticRateLimit() platformconfig.RateLimitConfig {
	return platformconfig.RateLimitConfig{RPS: 10, Burst: 20, IdleTTL: 30 * time.Second}
}

// TestRateLimitParamsPreferHotView 验证热更值优先于配置文件静态值。
func TestRateLimitParamsPreferHotView(t *testing.T) {
	runtime := &Runtime{Hot: hotconfig.NewHolder(newViewFrom(t, map[string]string{
		"rate_limit.rps":                "120.5",
		"rate_limit.burst":              "50",
		"rate_limit.idle_ttl":           "45s",
		"rate_limit.degrade_on_failure": "true",
	}, 1))}

	source := runtime.RateLimitParams(staticRateLimit())
	params, version, ok := source.Params()
	if !ok {
		t.Fatal("provider must always report ok (static fallback is always available)")
	}
	if params.RPS != 120.5 || params.Burst != 50 {
		t.Fatalf("params = %+v, want rps 120.5 burst 50", params)
	}
	if params.IdleTTL != 45*time.Second || !params.DegradeOnFailure {
		t.Fatalf("params = %+v, want idle_ttl 45s + degrade on", params)
	}
	if version == 0 {
		t.Fatal("version must be non-zero once params are known")
	}
}

// TestRateLimitParamsFallbackToStatic 验证启动期视图为空时回落到静态配置。
func TestRateLimitParamsFallbackToStatic(t *testing.T) {
	runtime := &Runtime{Hot: hotconfig.NewHolder(newViewFrom(t, nil, 0))}
	params, _, _ := runtime.RateLimitParams(staticRateLimit()).Params()
	if params.RPS != 10 || params.Burst != 20 || params.IdleTTL != 30*time.Second {
		t.Fatalf("params = %+v, want static fallback", params)
	}
}

// TestRateLimitVersionIgnoresUnrelatedKeys 验证只有 rate_limit.* 变化才推进版本，
// 改 log.level 不该让限流器做无谓的参数下发。
func TestRateLimitVersionIgnoresUnrelatedKeys(t *testing.T) {
	static := staticRateLimit()
	first := &Runtime{Hot: hotconfig.NewHolder(newViewFrom(t, map[string]string{
		"rate_limit.rps": "120", "log.level": "info",
	}, 1))}
	second := &Runtime{Hot: hotconfig.NewHolder(newViewFrom(t, map[string]string{
		"rate_limit.rps": "120", "log.level": "debug",
	}, 2))}

	_, v1, _ := first.RateLimitParams(static).Params()
	_, v2, _ := second.RateLimitParams(static).Params()
	if v1 != v2 {
		t.Fatalf("version changed (%d → %d) although rate_limit params are identical", v1, v2)
	}

	third := &Runtime{Hot: hotconfig.NewHolder(newViewFrom(t, map[string]string{
		"rate_limit.rps": "121",
	}, 3))}
	_, v3, _ := third.RateLimitParams(static).Params()
	if v3 == v1 {
		t.Fatal("version must change when rps changes")
	}
}

// TestRateLimitParamsNilWhenRuntimeUnavailable 验证未启用热更时不覆盖入口限流。
func TestRateLimitParamsNilWhenRuntimeUnavailable(t *testing.T) {
	if source := (&Runtime{}).RateLimitParams(staticRateLimit()); source != nil {
		t.Fatalf("provider = %v, want nil when hot view is absent", source)
	}
	if source := (*Runtime)(nil).RateLimitParams(staticRateLimit()); source != nil {
		t.Fatalf("provider = %v, want nil for nil runtime", source)
	}
}

// TestHotReadersTrackViewReplacement 验证读取函数每次都取当前视图，而非装配期快照。
func TestHotReadersTrackViewReplacement(t *testing.T) {
	holder := hotconfig.NewHolder(newViewFrom(t, map[string]string{
		"idempotency.ttl": "10m",
	}, 1))
	runtime := &Runtime{Hot: holder}

	ttl := runtime.HotDuration("idempotency.ttl", time.Hour)
	if got := ttl(); got != 10*time.Minute {
		t.Fatalf("ttl = %v, want 10m", got)
	}

	holder.Store(newViewFrom(t, map[string]string{"idempotency.ttl": "2m"}, 2))
	if got := ttl(); got != 2*time.Minute {
		t.Fatalf("ttl after refresh = %v, want 2m", got)
	}

	// 缺键回落 fallback。
	if got := runtime.HotDuration("rate_limit.rps", 0)(); got != 0 {
		t.Fatalf("missing key must fall back, got %v", got)
	}
	if got := runtime.HotInt("async.outbox.batch_size", 7)(); got != 7 {
		t.Fatalf("missing int key must fall back, got %d", got)
	}
	if got := runtime.HotString("async.consumer.retry_backoff", "exponential")(); got != "exponential" {
		t.Fatalf("missing string key must fall back, got %q", got)
	}
	if got := runtime.HotBool("rate_limit.degrade_on_failure", true)(); !got {
		t.Fatal("missing bool key must fall back")
	}
}

// TestApplyRuntimeParamsWiresRateLimitAndIdempotency 验证装配期接线。
func TestApplyRuntimeParamsWiresRateLimitAndIdempotency(t *testing.T) {
	runtime := &Runtime{Hot: hotconfig.NewHolder(newViewFrom(t, map[string]string{
		"rate_limit.burst": "77",
		"idempotency.ttl":  "3m",
	}, 1))}
	deps := &platform.Deps{Config: &platformconfig.Config{
		RateLimit:   staticRateLimit(),
		Idempotency: platformconfig.IdempotencyConfig{TTL: time.Hour},
	}}

	runtime.ApplyRuntimeParams(deps)
	if deps.RateLimitParams == nil {
		t.Fatal("RateLimitParams must be wired")
	}
	if deps.IdempotencyTTL == nil {
		t.Fatal("IdempotencyTTL must be wired")
	}
	params, _, _ := deps.RateLimitParams.Params()
	if params.Burst != 77 {
		t.Fatalf("burst = %d, want 77", params.Burst)
	}
	if got := deps.IdempotencyTTL(); got != 3*time.Minute {
		t.Fatalf("idempotency ttl = %v, want 3m", got)
	}
}

// TestApplyRuntimeParamsNoopWhenDisabled 验证未启用热更时不改动任何 Deps 字段。
func TestApplyRuntimeParamsNoopWhenDisabled(t *testing.T) {
	deps := &platform.Deps{Config: &platformconfig.Config{RateLimit: staticRateLimit()}}
	(&Runtime{}).ApplyRuntimeParams(deps)
	if deps.RateLimitParams != nil || deps.IdempotencyTTL != nil {
		t.Fatal("disabled hot view must not wire runtime params")
	}
}
