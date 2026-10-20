package httpclient_test

import (
	"context"
	stderrors "errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/admin-frame/admin-frame/pkg/httpclient"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

func TestNewRequiresTimeoutAfterDefaults(t *testing.T) {
	c, err := httpclient.New(httpclient.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if c.HTTP().Timeout <= 0 {
		t.Fatal("timeout must be set")
	}
}

func TestDoGETSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing UA")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c, err := httpclient.New(httpclient.Config{
		Timeout:        2 * time.Second,
		DisableTracing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" || resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
}

func TestRetryIdempotentOn503(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c, err := httpclient.New(httpclient.Config{
		Timeout:        5 * time.Second,
		DisableTracing: true,
		Retry:          retry.NewPolicy(retry.WithMaxAttempts(5), retry.WithBackoff(time.Millisecond, 10*time.Millisecond)),
	})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if hits.Load() != 3 {
		t.Fatalf("hits=%d", hits.Load())
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestNoRetryPOSTByDefault(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c, err := httpclient.New(httpclient.Config{
		Timeout:        2 * time.Second,
		DisableTracing: true,
		Retry:          retry.NewPolicy(retry.WithMaxAttempts(5), retry.WithBackoff(time.Millisecond, time.Millisecond)),
	})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, http.NoBody)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if hits.Load() != 1 {
		t.Fatalf("hits=%d want 1", hits.Load())
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestTracingInjectsTraceparent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	var gotTP, gotRID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTP = r.Header.Get("traceparent")
		gotRID = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := httpclient.New(httpclient.Config{
		Timeout:    2 * time.Second,
		Tracer:     tp.Tracer("test"),
		Propagator: propagation.TraceContext{},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, span := tp.Tracer("test").Start(context.Background(), "parent")
	defer span.End()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if gotTP == "" {
		t.Fatal("expected traceparent")
	}
	if gotRID == "" {
		t.Fatal("expected X-Request-ID from TraceID")
	}
}

func TestCheckStatus(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusNotFound, Request: httptest.NewRequest(http.MethodGet, "http://x", http.NoBody)}
	err := httpclient.CheckStatus(resp)
	var se *httpclient.StatusError
	if !stderrors.As(err, &se) || se.StatusCode != http.StatusNotFound {
		t.Fatalf("err=%v", err)
	}
	ok := &http.Response{StatusCode: http.StatusNoContent}
	if err := httpclient.CheckStatus(ok); err != nil {
		t.Fatal(err)
	}
}
