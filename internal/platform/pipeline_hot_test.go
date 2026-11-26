package platform_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

// hotRateLimitSource 是可变的限流参数来源，模拟配置中心推送。
type hotRateLimitSource struct {
	params  ratelimit.Params
	version int64
}

func (s *hotRateLimitSource) Params() (ratelimit.Params, int64, bool) {
	return s.params, s.version, true
}

// TestPipelineRateLimitUsesHotSource 验证入口限流读取运行期参数而不是装配期常量
// （ADR-0015 `rate_limit.*`）。
//
// 静态配置只给 1 个令牌，热更参数给 1000：若实现仍用静态值，第 2 个请求就会 429。
func TestPipelineRateLimitUsesHotSource(t *testing.T) {
	source := &hotRateLimitSource{params: ratelimit.Params{RPS: 1000, Burst: 1000}, version: 1}
	pipeline, err := httpserver.NewPipeline(httpserver.PipelineConfig{
		RateLimit: platformconfig.RateLimitConfig{
			Enabled: true,
			Driver:  platformconfig.RateLimitDriverMemory,
			RPS:     1,
			Burst:   1,
		},
		RateLimitSource: source,
	}, httpserver.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	handler := pipeline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/configs/items", http.NoBody))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 (hot burst must apply)", i+1, rec.Code)
		}
	}
}

// TestPipelineRateLimitStaticWhenNoSource 验证未注入来源时行为与改造前一致。
func TestPipelineRateLimitStaticWhenNoSource(t *testing.T) {
	pipeline, err := httpserver.NewPipeline(httpserver.PipelineConfig{
		RateLimit: platformconfig.RateLimitConfig{
			Enabled: true,
			Driver:  platformconfig.RateLimitDriverMemory,
			RPS:     1,
			Burst:   1,
		},
	}, httpserver.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	handler := pipeline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/configs/items", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/configs/items", http.NoBody))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429 (static burst=1)", rec.Code)
	}
}

// TestPipelineRateLimitTightenAppliesToExistingBucket 验证热更收紧立即生效，
// 且不会把已建立的桶重置为满桶。
func TestPipelineRateLimitTightenAppliesToExistingBucket(t *testing.T) {
	source := &hotRateLimitSource{params: ratelimit.Params{RPS: 1, Burst: 2}, version: 1}
	pipeline, err := httpserver.NewPipeline(httpserver.PipelineConfig{
		RateLimit: platformconfig.RateLimitConfig{
			Enabled: true,
			Driver:  platformconfig.RateLimitDriverMemory,
			RPS:     10,
			Burst:   10,
		},
		RateLimitSource: source,
	}, httpserver.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	handler := pipeline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	call := func() int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/configs/items", http.NoBody))
		return rec.Code
	}
	// 用掉初始 2 个令牌。
	if code := call(); code != http.StatusOK {
		t.Fatalf("first status = %d", code)
	}
	if code := call(); code != http.StatusOK {
		t.Fatalf("second status = %d", code)
	}
	// 热更下调到 burst=1：已消耗的令牌不退还，下一个请求仍应被拒。
	source.params = ratelimit.Params{RPS: 1, Burst: 1}
	source.version = 2
	if code := call(); code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 after tightening without refill", code)
	}
}

// TestPipelineIdempotencyTTLSourceFallsBack 验证 TTL 来源返回非法值时
// 回落到静态配置（避免坏发布让幂等记录永不过期）。
func TestPipelineIdempotencyTTLSourceFallsBack(t *testing.T) {
	pipeline, err := httpserver.NewPipeline(httpserver.PipelineConfig{
		Idempotency: platformconfig.IdempotencyConfig{
			Enabled: true,
			Driver:  platformconfig.IdempotencyDriverMemory,
			Header:  "Idempotency-Key",
			TTL:     time.Minute,
		},
		IdempotencyTTL: func() time.Duration { return 0 },
	}, httpserver.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	calls := 0
	handler := pipeline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
	}))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/admin/v1/iam/roles", http.NoBody)
		req.Header.Set("Idempotency-Key", "key-fallback-1")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1 (fallback TTL keeps the record alive)", calls)
	}
}
