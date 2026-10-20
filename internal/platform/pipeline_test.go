package platform_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// discardLogger 避免测试期间访问日志污染输出。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// TestPipelineOrder 验证 RequestID 位于 BodyLimit / RateLimit 外层：被拒绝的请求
// 仍带 X-Request-ID 与信封 trace_id（AC-PLAT-07-1 的可观测断言）。
func TestPipelineOrder(t *testing.T) {
	pipeline, err := platform.NewPipeline(platform.PipelineConfig{BodyLimitBytes: 4}, platform.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	h := pipeline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("0123456789"))
	req.Header.Set("X-Request-ID", "outer-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") != "outer-123" {
		t.Fatalf("RequestID must run before BodyLimit: header=%q", rec.Header().Get("X-Request-ID"))
	}
	if !strings.Contains(rec.Body.String(), "outer-123") {
		t.Fatalf("body must carry trace_id from RequestID: %s", rec.Body.String())
	}
}

// TestPipelineRateLimitEnvelope 验证限流返回 429 + 42900 稳定业务码（REL-05）。
func TestPipelineRateLimitEnvelope(t *testing.T) {
	pipeline, err := platform.NewPipeline(platform.PipelineConfig{
		RateLimit: platform.RateLimitConfig{
			Enabled: true,
			RPS:     0.001,
			Burst:   1,
			IdleTTL: time.Minute,
		},
	}, platform.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	h := pipeline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/admin/v1/orders", http.NoBody))
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d", first.Code)
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/admin/v1/orders", http.NoBody))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d want 429", second.Code)
	}
	if !strings.Contains(second.Body.String(), "42900") {
		t.Fatalf("body=%s want code 42900", second.Body.String())
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

// TestPipelineBodyLimit 验证超限请求体在进入 Handler 前被拒绝。
func TestPipelineBodyLimit(t *testing.T) {
	pipeline, err := platform.NewPipeline(platform.PipelineConfig{BodyLimitBytes: 8}, platform.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	h := pipeline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/orders", strings.NewReader("0123456789"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called {
		t.Fatal("handler must not be called for oversized body")
	}
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "40000") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestPipelineTimeoutPropagatesDeadline 验证 http.timeout 注入的 deadline 可被 Handler 感知。
func TestPipelineTimeoutPropagatesDeadline(t *testing.T) {
	pipeline, err := platform.NewPipeline(platform.PipelineConfig{Timeout: 20 * time.Millisecond}, platform.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	h := pipeline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			w.WriteHeader(http.StatusGatewayTimeout)
		case <-time.After(time.Second):
			w.WriteHeader(http.StatusOK)
		}
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d want 504", rec.Code)
	}
}

// TestPipelineRecoverEnvelope 验证 panic 被最外层 Recover 转为 500 信封。
func TestPipelineRecoverEnvelope(t *testing.T) {
	pipeline, err := platform.NewPipeline(platform.PipelineConfig{}, platform.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	h := pipeline(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "50000") {
		t.Fatalf("body=%s want code 50000", rec.Body.String())
	}
}

// TestPipelineRequestID 验证链路回写 X-Request-ID 并透传到响应信封。
func TestPipelineRequestID(t *testing.T) {
	pipeline, err := platform.NewPipeline(platform.PipelineConfig{}, platform.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	h := pipeline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(httpx.TraceIDFromContext(r.Context())))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Request-ID", "req-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") != "req-123" {
		t.Fatalf("header=%q", rec.Header().Get("X-Request-ID"))
	}
	if rec.Body.String() != "req-123" {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// TestPipelineCORSPreflight 验证仅显式允许的 Origin 获得 CORS 响应头。
func TestPipelineCORSPreflight(t *testing.T) {
	pipeline, err := platform.NewPipeline(platform.PipelineConfig{
		CORS: platform.CORSConfig{AllowOrigins: []string{"https://admin.example.com"}},
	}, platform.PipelineDeps{Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	h := pipeline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/admin/v1/orders", http.NoBody)
	req.Header.Set("Origin", "https://admin.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://admin.example.com" {
		t.Fatalf("missing CORS header: %v", rec.Header())
	}

	evil := httptest.NewRequest(http.MethodOptions, "/admin/v1/orders", http.NoBody)
	evil.Header.Set("Origin", "https://evil.example.com")
	evil.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, evil)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unexpected CORS allow for unknown origin")
	}
}

// TestPipelineInvalidRateLimit 验证非法限流参数在装配期失败。
func TestPipelineInvalidRateLimit(t *testing.T) {
	if _, err := platform.NewPipeline(platform.PipelineConfig{
		RateLimit: platform.RateLimitConfig{Enabled: true, RPS: 0, Burst: 0},
	}, platform.PipelineDeps{}); err == nil {
		t.Fatal("expected error for invalid rate limit")
	}
}
