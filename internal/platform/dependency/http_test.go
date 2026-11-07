package dependency_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/dependency"
	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	"github.com/admin-frame/admin-frame/pkg/httpclient"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

func TestHTTPRetriesAndObserves(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	obs := &recordObserver{}
	registry := dependency.NewRegistry(obs)
	caller, err := registry.HTTP(dependency.Spec{
		Name:    "downstream:http",
		Timeout: time.Second,
		Retry:   retry.NewPolicy(retry.WithMaxAttempts(2), retry.WithBackoff(time.Millisecond, 5*time.Millisecond)),
	}, httpclient.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer caller.CloseIdleConnections()

	req, err := http.NewRequest(http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := caller.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if hits != 2 {
		t.Fatalf("hits=%d want 2", hits)
	}
	want := []string{"downstream:http=error", "downstream:http=retry", "downstream:http=ok"}
	if len(obs.calls) != len(want) {
		t.Fatalf("calls=%v want %v", obs.calls, want)
	}
	for i := range want {
		if obs.calls[i] != want[i] {
			t.Fatalf("calls=%v want %v", obs.calls, want)
		}
	}
}

func TestHTTPBreakerOpenFailsFast(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	obs := &recordObserver{}
	registry := dependency.NewRegistry(obs)
	caller, err := registry.HTTP(dependency.Spec{
		Name:    "downstream:open",
		Timeout: time.Second,
		Breaker: &dependency.BreakerSpec{ConsecutiveFailures: 1, OpenTimeout: time.Minute},
	}, httpclient.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer caller.CloseIdleConnections()

	send := func() error {
		req, err := http.NewRequest(http.MethodGet, srv.URL, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := caller.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		return err
	}
	if err := send(); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := send(); !errors.Is(err, circuitbreaker.ErrOpen) {
		t.Fatalf("second call err=%v want ErrOpen", err)
	}
	if hits != 1 {
		t.Fatalf("hits=%d want 1 (breaker must short-circuit)", hits)
	}
	if got := obs.calls[len(obs.calls)-1]; got != "downstream:open=breaker_open" {
		t.Fatalf("last call=%q want breaker_open", got)
	}
}

func TestHTTPCallerClientAccessor(t *testing.T) {
	registry := dependency.NewRegistry(nil)
	caller, err := registry.HTTP(dependency.Spec{
		Name:    "downstream:client",
		Timeout: time.Second,
	}, httpclient.Config{DisableRetry: true})
	if err != nil {
		t.Fatal(err)
	}
	defer caller.CloseIdleConnections()

	client := caller.Client()
	if client == nil {
		t.Fatal("Client() = nil want injected client")
	}
	if client.HTTP() != caller.HTTP() {
		t.Fatal("Client().HTTP() must equal caller.HTTP()")
	}

	var nilCaller *dependency.HTTPCaller
	if nilCaller.Client() != nil {
		t.Fatal("nil HTTPCaller.Client() must be nil")
	}
}

func TestHTTPNonIdempotentNotRetried(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	registry := dependency.NewRegistry(nil)
	caller, err := registry.HTTP(dependency.Spec{
		Name:    "downstream:post",
		Timeout: time.Second,
		Retry:   retry.NewPolicy(retry.WithMaxAttempts(3)),
	}, httpclient.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer caller.CloseIdleConnections()

	req, err := http.NewRequest(http.MethodPost, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := caller.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if hits != 1 {
		t.Fatalf("hits=%d want 1 for non-idempotent", hits)
	}
}
