package idempotency_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/idempotency"
)

// recordingStore 记录中间件下发的 TTL，用于断言热更 TTL 的传递。
type recordingStore struct {
	beginTTL    []time.Duration
	completeTTL []time.Duration
}

func (s *recordingStore) Begin(_ context.Context, _, _ string, ttl time.Duration) (idempotency.Record, bool, error) {
	s.beginTTL = append(s.beginTTL, ttl)
	return idempotency.Record{}, true, nil
}

func (s *recordingStore) Complete(_ context.Context, _, _ string, _ int, _ http.Header, _ []byte,
	ttl time.Duration) error {
	s.completeTTL = append(s.completeTTL, ttl)
	return nil
}

func (s *recordingStore) Release(_ context.Context, _, _ string) error { return nil }

func newIdempotentScenario(t *testing.T, cfg idempotency.Config) http.Handler {
	t.Helper()
	handler := idempotency.Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	return handler
}

func sendWrite(h http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/iam/roles", strings.NewReader(`{"name":"ops"}`))
	req.Header.Set(idempotency.Header, "key-hot-ttl")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestTTLSourceOverridesStaticTTL 验证热更 TTL 覆盖静态配置，且同一请求内
// Begin 与 Complete 使用同一个值（避免「创建」和「回写」落在不同过期时间）。
func TestTTLSourceOverridesStaticTTL(t *testing.T) {
	store := &recordingStore{}
	hot := time.Minute
	h := newIdempotentScenario(t, idempotency.Config{
		Store:     store,
		TTL:       24 * time.Hour,
		TTLSource: func() time.Duration { return hot },
	})

	sendWrite(h)
	hot = 5 * time.Minute
	sendWrite(h)

	if len(store.beginTTL) != 2 || len(store.completeTTL) != 2 {
		t.Fatalf("store calls: begin=%v complete=%v", store.beginTTL, store.completeTTL)
	}
	if store.beginTTL[0] != time.Minute || store.completeTTL[0] != time.Minute {
		t.Fatalf("first request ttl mismatch: begin=%v complete=%v", store.beginTTL[0], store.completeTTL[0])
	}
	if store.beginTTL[1] != 5*time.Minute || store.completeTTL[1] != 5*time.Minute {
		t.Fatalf("hot ttl mismatch: begin=%v complete=%v", store.beginTTL[1], store.completeTTL[1])
	}
}

// TestTTLSourceInvalidFallsBack 验证坏发布（TTL≤0）回落到静态配置，
// 而不是让记录永不过期或立即失效。
func TestTTLSourceInvalidFallsBack(t *testing.T) {
	store := &recordingStore{}
	h := newIdempotentScenario(t, idempotency.Config{
		Store:     store,
		TTL:       time.Hour,
		TTLSource: func() time.Duration { return 0 },
	})

	sendWrite(h)
	if len(store.beginTTL) != 1 {
		t.Fatalf("begin calls = %d, want 1", len(store.beginTTL))
	}
	if store.beginTTL[0] != time.Hour {
		t.Fatalf("ttl = %v, want fallback %v", store.beginTTL[0], time.Hour)
	}
}
