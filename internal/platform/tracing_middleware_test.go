package platform_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

func TestRequestIDReusesOTelTraceID(t *testing.T) {
	tp, err := tracing.Init(context.Background(), tracing.Options{
		Service:  "admin-api",
		Exporter: tracing.ExporterNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var got string
	h := platform.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = platform.TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}),
		tracing.HTTPMiddleware(tracing.WithTracer(tp.Tracer("http.server"))),
		platform.RequestID(),
	)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", http.NoBody))
	if got == "" || len(got) != 32 {
		t.Fatalf("trace_id=%q", got)
	}
	if rr.Header().Get("X-Request-ID") != got {
		t.Fatalf("header=%q ctx=%q", rr.Header().Get("X-Request-ID"), got)
	}
}

func TestRequestIDKeepsIncomingHeader(t *testing.T) {
	tp, err := tracing.Init(context.Background(), tracing.Options{
		Service:  "admin-api",
		Exporter: tracing.ExporterNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var got string
	h := platform.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = platform.TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}),
		tracing.HTTPMiddleware(tracing.WithTracer(tp.Tracer("http.server"))),
		platform.RequestID(),
	)

	req := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
	req.Header.Set("X-Request-ID", "client-corr-id")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "client-corr-id" {
		t.Fatalf("got=%q", got)
	}
}
