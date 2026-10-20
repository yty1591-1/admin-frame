package webhook_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/webhook"
)

func TestMiddlewareSuccess(t *testing.T) {
	body := []byte(`{"event":"paid"}`)
	verifier := mustVerifier(t, testVerifierConfig())
	header := signAt(t, mustSigner(t, testSignerConfig()), "msg_middleware", body, signingTime)

	var (
		seen        webhook.Delivery
		handlerBody []byte
		called      bool
	)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		delivery, ok := webhook.DeliveryFrom(r.Context())
		if !ok {
			t.Error("delivery missing from context")
		}
		seen = delivery
		read, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body in handler: %v", err)
		}
		handlerBody = read
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/pay", bytes.NewReader(body))
	req.Header = header.Clone()
	rec := httptest.NewRecorder()
	verifier.Middleware()(next).ServeHTTP(rec, req)

	if !called {
		t.Fatal("next handler not called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", rec.Code)
	}
	if seen.ID != "msg_middleware" {
		t.Fatalf("delivery id=%q", seen.ID)
	}
	if !bytes.Equal(handlerBody, body) {
		t.Fatalf("handler body=%q", handlerBody)
	}
}

func TestMiddlewareMissingSignature(t *testing.T) {
	verifier := mustVerifier(t, testVerifierConfig())
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler must not run")
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	verifier.Middleware()(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "webhook verification failed" {
		t.Fatalf("body=%q", got)
	}
	if strings.Contains(rec.Body.String(), webhook.HeaderID) {
		t.Fatalf("body leaks internal detail: %q", rec.Body.String())
	}
}

func TestMiddlewareBodyTooLarge(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 32)
	signerCfg := testSignerConfig()
	signerCfg.MaxBodyBytes = 64
	header := signAt(t, mustSigner(t, signerCfg), "msg_mw_big", body, signingTime)

	verifierCfg := testVerifierConfig()
	verifierCfg.MaxBodyBytes = 16
	verifier := mustVerifier(t, verifierCfg)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header = header.Clone()
	rec := httptest.NewRecorder()
	verifier.Middleware()(http.NotFoundHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "request body too large" {
		t.Fatalf("body=%q", got)
	}
}

func TestMiddlewareReplay(t *testing.T) {
	body := []byte(`{"event":"paid"}`)
	verifier := mustVerifier(t, testVerifierConfig())
	header := signAt(t, mustSigner(t, testSignerConfig()), "msg_mw_replay", body, signingTime)

	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	})
	handler := verifier.Middleware()(next)

	first := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	first.Header = header.Clone()
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusNoContent || calls != 1 {
		t.Fatalf("first status=%d calls=%d", firstRec.Code, calls)
	}

	second := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	second.Header = header.Clone()
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusConflict || calls != 1 {
		t.Fatalf("second status=%d calls=%d", secondRec.Code, calls)
	}
	if got := strings.TrimSpace(secondRec.Body.String()); got != "duplicate webhook" {
		t.Fatalf("body=%q", got)
	}
}

func TestMiddlewareNonceUnavailable(t *testing.T) {
	body := []byte(`{"event":"paid"}`)
	header := signAt(t, mustSigner(t, testSignerConfig()), "msg_mw_store", body, signingTime)
	cfg := testVerifierConfig()
	cfg.Nonce = failingNonce{err: errors.New("store down")}
	verifier := mustVerifier(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header = header.Clone()
	rec := httptest.NewRecorder()
	verifier.Middleware()(http.NotFoundHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "webhook verification unavailable" {
		t.Fatalf("body=%q", got)
	}
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func (failingBody) Close() error { return nil }

func TestMiddlewareBodyReadFailure(t *testing.T) {
	verifier := mustVerifier(t, testVerifierConfig())
	req := httptest.NewRequest(http.MethodPost, "/webhook", http.NoBody)
	req.Body = failingBody{}
	rec := httptest.NewRecorder()
	verifier.Middleware()(http.NotFoundHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestMiddlewareOnError(t *testing.T) {
	cfg := testVerifierConfig()
	var captured error
	cfg.OnError = func(w http.ResponseWriter, _ *http.Request, err error) {
		captured = err
		w.WriteHeader(http.StatusTeapot)
	}
	verifier := mustVerifier(t, cfg)

	rec := httptest.NewRecorder()
	verifier.Middleware()(http.NotFoundHandler()).ServeHTTP(
		rec, httptest.NewRequest(http.MethodPost, "/webhook", http.NoBody))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status=%d want 418", rec.Code)
	}
	if !errors.Is(captured, webhook.ErrMissingHeader) {
		t.Fatalf("captured err=%v want ErrMissingHeader", captured)
	}
}

func TestMiddlewareNilVerifier(t *testing.T) {
	var verifier *webhook.Verifier
	rec := httptest.NewRecorder()
	verifier.Middleware()(http.NotFoundHandler()).ServeHTTP(
		rec, httptest.NewRequest(http.MethodPost, "/webhook", http.NoBody))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", rec.Code)
	}
}
