package tracing_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/admin-frame/admin-frame/pkg/tracing"
)

// copyRequest 模拟 middleware.Timeout：用 WithContext 复制请求后再往下传。
//
// 复制的关键是：ServeMux 把 Pattern 写在「传给它的那份请求」上，
// 一旦中间件复制过，外层就取不到 Pattern —— 这正是线上 span 名退化成
// "GET unknown" 的原因。
func copyRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(r.Context()))
	})
}

func newRouteMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/audit/events", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func serveAndGetServerSpan(t *testing.T, handler http.Handler) sdktrace.ReadOnlySpan {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	wrapped := tracing.HTTPMiddleware(tracing.WithTracer(tp.Tracer("http.server")))(handler)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/admin/v1/audit/events?from=1", http.NoBody)
	wrapped.ServeHTTP(httptest.NewRecorder(), req)

	for _, span := range recorder.Ended() {
		if span.SpanKind() == trace.SpanKindServer {
			return span
		}
	}
	t.Fatalf("未产出 server span")
	return nil
}

// TestRoutePatternMiddlewareNamesSpan 锁定修复：最内层的路由中间件把 Pattern
// 写回 span，即使外层中间件复制过请求，span 名仍是「GET /admin/v1/audit/events」。
func TestRoutePatternMiddlewareNamesSpan(t *testing.T) {
	span := serveAndGetServerSpan(t, copyRequest(tracing.RoutePatternMiddleware()(newRouteMux())))

	if want := "GET /admin/v1/audit/events"; span.Name() != want {
		t.Fatalf("span 名 = %q, want %q", span.Name(), want)
	}
	if got := attrOf(span, "http.route"); got != "/admin/v1/audit/events" {
		t.Fatalf("http.route = %q, want /admin/v1/audit/events", got)
	}
}

// TestRoutePatternMiddlewareIsRequired 是反例：不加最内层中间件时，span 名退化，
// 证明这不是「随便加加」——按路由检索/聚合会整体失效。
func TestRoutePatternMiddlewareIsRequired(t *testing.T) {
	span := serveAndGetServerSpan(t, copyRequest(newRouteMux()))
	if want := "GET unknown"; span.Name() != want {
		t.Fatalf("未加中间件时 span 名 = %q, want %q（若已修复请同步更新本反例）", span.Name(), want)
	}
}

func attrOf(span sdktrace.ReadOnlySpan, key string) string {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}
