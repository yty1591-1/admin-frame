package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/contextx"
	"github.com/admin-frame/admin-frame/pkg/middleware"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
	"github.com/admin-frame/admin-frame/pkg/tracing"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestChainOrder(t *testing.T) {
	var order []string
	mw := func(name string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+":in")
				next.ServeHTTP(w, r)
				order = append(order, name+":out")
			})
		}
	}
	h := middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "h")
	}), mw("a"), mw("b"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	want := "a:in b:in h b:out a:out"
	got := strings.Join(order, " ")
	if got != want {
		t.Fatalf("got=%q", got)
	}
}

func TestRequestIDIncoming(t *testing.T) {
	var got string
	h := middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = contextx.RequestID(r.Context())
	}), middleware.RequestID())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client-id")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got != "client-id" || rr.Header().Get("X-Request-ID") != "client-id" {
		t.Fatalf("got=%q header=%q", got, rr.Header().Get("X-Request-ID"))
	}
}

func TestRequestIDFromOTel(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("t").Start(context.Background(), "op")
	defer span.End()
	tid := tracing.TraceID(ctx)
	if tid == "" {
		t.Fatal("empty tid")
	}

	var got string
	h := middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = contextx.RequestID(r.Context())
	}), middleware.RequestID())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got != tid {
		t.Fatalf("got=%q want=%q", got, tid)
	}
}

func TestRecover(t *testing.T) {
	var panicked any
	h := middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}), middleware.Recover(middleware.RecoverConfig{
		OnPanic: func(w http.ResponseWriter, r *http.Request, recovered any) {
			panicked = recovered
			w.WriteHeader(http.StatusInternalServerError)
		},
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if panicked != "boom" || rr.Code != 500 {
		t.Fatalf("panic=%v code=%d", panicked, rr.Code)
	}
}

func TestTimeoutSoft(t *testing.T) {
	h := middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			w.WriteHeader(http.StatusGatewayTimeout)
		case <-time.After(50 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		}
	}), middleware.Timeout(5*time.Millisecond))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	h := middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), middleware.CORS(middleware.CORSConfig{
		AllowOrigins: []string{"https://app.example.com"},
		MaxAge:       time.Hour,
	}))
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("code=%d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("acao=%q", rr.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestRateLimit(t *testing.T) {
	lim, err := ratelimit.NewTokenBucket(0.0001, 1) // 几乎不补充，突发 1
	if err != nil {
		t.Fatal(err)
	}
	var limited int
	h := middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), middleware.RateLimit(middleware.RateLimitConfig{
		Limiter: lim,
		OnLimited: func(w http.ResponseWriter, r *http.Request, result ratelimit.Result) {
			limited++
			w.WriteHeader(http.StatusTooManyRequests)
		},
	}))
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, httptest.NewRequest(http.MethodGet, "/", nil))
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr1.Code != 200 || rr2.Code != 429 || limited != 1 {
		t.Fatalf("c1=%d c2=%d limited=%d", rr1.Code, rr2.Code, limited)
	}
}
