package tracing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/admin-frame/admin-frame/pkg/tracing"
)

func TestHTTPMiddlewareCreatesServerSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	sdkTP := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = sdkTP.Shutdown(context.Background()) })

	tp, err := tracing.Init(context.Background(), tracing.Options{
		Service:  "admin-api",
		Exporter: tracing.ExporterNoop,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = tp.Shutdown(context.Background())

	// 使用带 recorder 的 provider 作为中间件 Tracer
	mw := tracing.HTTPMiddleware(tracing.WithTracer(sdkTP.Tracer("http.server")))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		if tracing.TraceID(r.Context()) == "" {
			t.Error("missing TraceID in handler")
		}
		w.WriteHeader(http.StatusOK)
	})
	h := mw(mux)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/v1/orders/42", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans=%d", len(spans))
	}
	if spans[0].Name() != "GET /admin/v1/orders/{id}" {
		t.Fatalf("name=%q", spans[0].Name())
	}
}

func TestHTTPMiddlewareSkipsProbes(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	sdkTP := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = sdkTP.Shutdown(context.Background()) })

	mw := tracing.HTTPMiddleware(tracing.WithTracer(sdkTP.Tracer("http.server")))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody))
	if len(sr.Ended()) != 0 {
		t.Fatalf("probe should not create span")
	}
}

func TestHTTPMiddlewareExtractsTraceparent(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	sdkTP := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = sdkTP.Shutdown(context.Background()) })

	prop := propagation.TraceContext{}
	mw := tracing.HTTPMiddleware(
		tracing.WithTracer(sdkTP.Tracer("http.server")),
		tracing.WithPropagator(prop),
	)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api", http.NoBody)
	// 固定合法 traceparent：version-traceid-spanid-flags
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	h.ServeHTTP(httptest.NewRecorder(), req)
	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans=%d", len(spans))
	}
	got := spans[0].SpanContext().TraceID().String()
	if got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace_id=%s", got)
	}
}
