package idempotency_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/idempotency"
)

func TestValidateKey(t *testing.T) {
	if err := idempotency.ValidateKey(""); !errors.IsCode(err, errors.CodeInvalidArgument) {
		t.Fatalf("empty: %v", err)
	}
	if err := idempotency.ValidateKey("ok-key_1"); err != nil {
		t.Fatal(err)
	}
	if err := idempotency.ValidateKey("bad key"); err == nil {
		t.Fatal("expected space rejected")
	}
	long := make([]byte, idempotency.MaxKeyLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := idempotency.ValidateKey(string(long)); err == nil {
		t.Fatal("expected too long")
	}
}

func TestFingerprintDiffersOnBody(t *testing.T) {
	a := idempotency.Fingerprint(http.MethodPost, "/c/v1/orders", "", []byte(`{"a":1}`))
	b := idempotency.Fingerprint(http.MethodPost, "/c/v1/orders", "", []byte(`{"a":2}`))
	if a == b {
		t.Fatal("fingerprints should differ")
	}
}

func TestMemoryStoreReplayAndConflict(t *testing.T) {
	store := idempotency.NewMemoryStore()
	ctx := context.Background()
	const key, fp = "k1", "fp1"

	rec, created, err := store.Begin(ctx, key, fp, time.Hour)
	if err != nil || !created || rec.State != idempotency.StateInProgress {
		t.Fatalf("begin1 created=%v rec=%+v err=%v", created, rec, err)
	}
	if err := store.Complete(ctx, key, fp, http.StatusCreated, http.Header{"Content-Type": {"application/json"}}, []byte(`{"id":1}`), time.Hour); err != nil {
		t.Fatal(err)
	}

	rec, created, err = store.Begin(ctx, key, fp, time.Hour)
	if err != nil || created || rec.StatusCode != http.StatusCreated || string(rec.Body) != `{"id":1}` {
		t.Fatalf("replay created=%v rec=%+v err=%v", created, rec, err)
	}

	_, _, err = store.Begin(ctx, key, "other-fp", time.Hour)
	if !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("conflict err=%v", err)
	}
}

func TestMemoryStoreInProgressAndRelease(t *testing.T) {
	store := idempotency.NewMemoryStore()
	ctx := context.Background()
	_, created, err := store.Begin(ctx, "k", "fp", time.Hour)
	if err != nil || !created {
		t.Fatal(err)
	}
	_, _, err = store.Begin(ctx, "k", "fp", time.Hour)
	if !errors.Is(err, idempotency.ErrInProgress) {
		t.Fatalf("err=%v", err)
	}
	if err := store.Release(ctx, "k", "fp"); err != nil {
		t.Fatal(err)
	}
	_, created, err = store.Begin(ctx, "k", "fp", time.Hour)
	if err != nil || !created {
		t.Fatalf("after release created=%v err=%v", created, err)
	}
}

func TestMemoryStoreTTLExpiry(t *testing.T) {
	store := idempotency.NewMemoryStore()
	ctx := context.Background()
	_, _, err := store.Begin(ctx, "k", "fp", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, "k", "fp", 200, nil, []byte("ok"), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	_, created, err := store.Begin(ctx, "k", "fp", time.Hour)
	if err != nil || !created {
		t.Fatalf("expected re-begin after ttl created=%v err=%v", created, err)
	}
}

func TestGuardRun(t *testing.T) {
	g := &idempotency.Guard{Store: idempotency.NewMemoryStore()}
	var calls atomic.Int32
	fn := func(context.Context) (idempotency.Result, error) {
		calls.Add(1)
		return idempotency.Result{StatusCode: 200, Body: []byte("x")}, nil
	}
	res, err := g.Run(context.Background(), "k", "fp", fn)
	if err != nil || string(res.Body) != "x" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	res, err = g.Run(context.Background(), "k", "fp", fn)
	if err != nil || string(res.Body) != "x" || calls.Load() != 1 {
		t.Fatalf("replay calls=%d res=%+v err=%v", calls.Load(), res, err)
	}
}

func TestGuardReleasesOnError(t *testing.T) {
	g := &idempotency.Guard{Store: idempotency.NewMemoryStore()}
	_, err := g.Run(context.Background(), "k", "fp", func(context.Context) (idempotency.Result, error) {
		return idempotency.Result{}, errors.Internal("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var calls int
	_, err = g.Run(context.Background(), "k", "fp", func(context.Context) (idempotency.Result, error) {
		calls++
		return idempotency.Result{Body: []byte("ok")}, nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestMiddlewareReplayAndConflict(t *testing.T) {
	store := idempotency.NewMemoryStore()
	var hits atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		key, ok := idempotency.KeyFrom(r.Context())
		if !ok || key != "demo-1" {
			t.Errorf("key=%q ok=%v", key, ok)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	h := idempotency.Middleware(idempotency.Config{
		Store:    store,
		Required: true,
		Scope:    func(*http.Request) string { return "user-1" },
	})(inner)

	req := func(body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/c/v1/orders", bytes.NewBufferString(body))
		r.Header.Set(idempotency.Header, "demo-1")
		return r
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req(`{"amount":1}`))
	if rr.Code != http.StatusCreated || rr.Body.String() != `{"ok":true}` {
		t.Fatalf("first status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req(`{"amount":1}`))
	if rr2.Code != http.StatusCreated || rr2.Body.String() != `{"ok":true}` || hits.Load() != 1 {
		t.Fatalf("replay status=%d body=%s hits=%d", rr2.Code, rr2.Body.String(), hits.Load())
	}

	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, req(`{"amount":2}`))
	if rr3.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", rr3.Code, rr3.Body.String())
	}
}

func TestMiddlewareMissingKey(t *testing.T) {
	h := idempotency.Middleware(idempotency.Config{
		Store:    idempotency.NewMemoryStore(),
		Required: true,
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("should not run")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", http.NoBody))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestMiddlewareSkipsGET(t *testing.T) {
	var ran bool
	h := idempotency.Middleware(idempotency.Config{
		Store:    idempotency.NewMemoryStore(),
		Required: true,
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ran = true
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if !ran || rr.Code != http.StatusOK {
		t.Fatalf("ran=%v status=%d", ran, rr.Code)
	}
}

func TestMiddlewareReleasesOn5xx(t *testing.T) {
	store := idempotency.NewMemoryStore()
	var hits atomic.Int32
	h := idempotency.Middleware(idempotency.Config{Store: store, Required: true})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			http.Error(w, "boom", http.StatusInternalServerError)
		}),
	)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
	req.Header.Set(idempotency.Header, "k")
	h.ServeHTTP(httptest.NewRecorder(), req)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if hits.Load() != 2 {
		t.Fatalf("hits=%d want 2 after 5xx release", hits.Load())
	}
}

func TestMiddlewareConcurrentInProgress(t *testing.T) {
	store := idempotency.NewMemoryStore()
	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce sync.Once
	h := idempotency.Middleware(idempotency.Config{Store: store, Required: true})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			enterOnce.Do(func() { close(entered) })
			<-release
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "done")
		}),
	)

	var wg sync.WaitGroup
	wg.Add(2)
	codes := make(chan int, 2)
	go func() {
		defer wg.Done()
		rr := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
		r.Header.Set(idempotency.Header, "same")
		h.ServeHTTP(rr, r)
		codes <- rr.Code
	}()
	go func() {
		defer wg.Done()
		<-entered
		rr := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
		r.Header.Set(idempotency.Header, "same")
		h.ServeHTTP(rr, r)
		codes <- rr.Code
		close(release)
	}()
	wg.Wait()
	close(codes)

	ok, conflict := 0, 0
	for c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("unexpected status %d", c)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("ok=%d conflict=%d", ok, conflict)
	}
}

func TestCompositeKey(t *testing.T) {
	if got := idempotency.CompositeKey("", "k"); got != "k" {
		t.Fatalf("got=%s", got)
	}
	if got := idempotency.CompositeKey("u1", "k"); got != "u1:k" {
		t.Fatalf("got=%s", got)
	}
}
