package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/push"
	"github.com/admin-frame/admin-frame/pkg/push/pushtest"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSignProviderToken(t *testing.T) {
	key := generateKey(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	token, err := signProviderToken(key, "KEYID", "TEAMID", now)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("parts=%d", len(parts))
	}
	header := decodeSegment(t, parts[0])
	if header["alg"] != "ES256" || header["kid"] != "KEYID" {
		t.Fatalf("header=%v", header)
	}
	claims := decodeSegment(t, parts[1])
	if claims["iss"] != "TEAMID" {
		t.Fatalf("claims=%v", claims)
	}
	iat, ok := claims["iat"].(float64)
	if !ok || int64(iat) != now.Unix() {
		t.Fatalf("iat=%v", claims["iat"])
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		t.Fatalf("signature len=%d err=%v", len(signature), err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
		t.Fatal("signature verification failed")
	}
}

func TestTokenCacheAndRefresh(t *testing.T) {
	clk := clock.NewManual(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	provider := newTokenProvider(generateKey(t), "KEYID", "TEAMID", clk)
	first, err := provider.token()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	second, err := provider.token()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if first != second {
		t.Fatal("token should be cached")
	}
	clk.Advance(51 * time.Minute)
	third, err := provider.token()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if third == first {
		t.Fatal("token should refresh after lifetime")
	}
	provider.invalidate()
	fourth, err := provider.token()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if fourth == third {
		t.Fatal("invalidate should force refresh")
	}
}

func TestSendSuccess(t *testing.T) {
	var mu sync.Mutex
	var captured *http.Request
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		mu.Lock()
		captured = r.Clone(r.Context())
		body = data
		mu.Unlock()
		w.Header().Set("apns-id", "11111111-2222-4333-8444-555555555555")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender, err := Open(context.Background(), testConfig(t, server.URL))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()

	msg := pushtest.ValidMessage()
	msg.Token = testToken
	msg.IdempotencyKey = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	receipt, err := sender.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if receipt.MessageID != "11111111-2222-4333-8444-555555555555" || receipt.Provider != "apns" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	mu.Lock()
	defer mu.Unlock()
	if captured.Method != http.MethodPost || captured.URL.Path != "/3/device/"+testToken {
		t.Fatalf("request=%s %s", captured.Method, captured.URL.Path)
	}
	if got := captured.Header.Get("authorization"); !strings.HasPrefix(got, "bearer ") {
		t.Fatalf("authorization=%q", got)
	}
	if got := captured.Header.Get("apns-topic"); got != "com.example.app" {
		t.Fatalf("topic=%q", got)
	}
	if got := captured.Header.Get("apns-push-type"); got != "alert" {
		t.Fatalf("push type=%q", got)
	}
	if got := captured.Header.Get("apns-priority"); got != "10" {
		t.Fatalf("priority=%q", got)
	}
	if got := captured.Header.Get("apns-id"); got != msg.IdempotencyKey {
		t.Fatalf("apns-id=%q", got)
	}
	if got := captured.Header.Get("apns-collapse-id"); got != msg.CollapseKey {
		t.Fatalf("collapse=%q", got)
	}
	if got := captured.Header.Get("apns-expiration"); got == "" {
		t.Fatal("missing expiration")
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	aps, ok := payload["aps"].(map[string]any)
	if !ok {
		t.Fatalf("aps missing: %s", body)
	}
	alert, ok := aps["alert"].(map[string]any)
	if !ok || alert["title"] != msg.Title {
		t.Fatalf("alert=%v", aps["alert"])
	}
	if payload["order_id"] != "123" {
		t.Fatalf("custom data missing: %s", body)
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		reason     string
		retryAfter string
		wantIs     error
		wantCode   pkgerrors.Code
		retryable  bool
	}{
		{name: "bad device token", status: 400, reason: "BadDeviceToken", wantIs: push.ErrTokenInvalid, wantCode: pkgerrors.CodeFailedPrecondition},
		{name: "unregistered", status: 410, reason: "Unregistered", wantIs: push.ErrTokenInvalid, wantCode: pkgerrors.CodeFailedPrecondition},
		{name: "rate limited", status: 429, reason: "TooManyRequests", retryAfter: "60", wantCode: pkgerrors.CodeResourceExhausted, retryable: true},
		{name: "service unavailable", status: 503, reason: "ServiceUnavailable", wantCode: pkgerrors.CodeUnavailable, retryable: true},
		{name: "provider token", status: 403, reason: "InvalidProviderToken", wantCode: pkgerrors.CodePermissionDenied},
		{name: "bad topic", status: 400, reason: "BadTopic", wantIs: push.ErrRejected, wantCode: pkgerrors.CodeInvalidArgument},
		{name: "internal", status: 500, wantCode: pkgerrors.CodeUnavailable, retryable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(tt.status)
				if tt.reason != "" {
					_, _ = w.Write([]byte(`{"reason":"` + tt.reason + `"}`))
				}
			}))
			defer server.Close()
			sender, err := Open(context.Background(), testConfig(t, server.URL))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = sender.Close() }()
			msg := pushtest.ValidMessage()
			msg.Token = testToken
			_, err = sender.Send(context.Background(), msg)
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("err=%v want %v", err, tt.wantIs)
			}
			if !pkgerrors.IsCode(err, tt.wantCode) {
				t.Fatalf("code=%s want %s (err=%v)", pkgerrors.CodeOf(err), tt.wantCode, err)
			}
			if got := pkgerrors.RetryableOf(err); got != tt.retryable {
				t.Fatalf("retryable=%v want %v", got, tt.retryable)
			}
			if tt.retryAfter != "" {
				if got := push.RetryAfterOf(err); got != 60*time.Second {
					t.Fatalf("retry after=%s", got)
				}
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatalf("error leaks token: %v", err)
			}
		})
	}
}

func TestExpiredProviderTokenIsRefreshed(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	tokens := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		call := calls
		tokens = append(tokens, r.Header.Get("authorization"))
		mu.Unlock()
		if call == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"reason":"ExpiredProviderToken"}`))
			return
		}
		w.Header().Set("apns-id", "refreshed")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	clk := clock.NewManual(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	cfg := testConfig(t, server.URL)
	cfg.Clock = clk
	sender, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()
	msg := pushtest.ValidMessage()
	msg.Token = testToken
	if _, err := sender.Send(context.Background(), msg); err == nil {
		t.Fatal("first send should fail")
	}
	clk.Advance(time.Second)
	if _, err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("second send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != 2 || tokens[0] == tokens[1] {
		t.Fatalf("provider token was not refreshed: %v", tokens)
	}
}

func TestPlatformMismatch(t *testing.T) {
	sender, err := Open(context.Background(), testConfig(t, "http://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()
	msg := pushtest.ValidMessage()
	msg.Platform = push.PlatformAndroid
	if _, err := sender.Send(context.Background(), msg); !errors.Is(err, push.ErrNotSupported) {
		t.Fatalf("err=%v want ErrNotSupported", err)
	}
}

func TestOpenValidatesConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*push.Config)
	}{
		{name: "missing topic", mutate: func(c *push.Config) { c.APNS.Topic = "" }},
		{name: "missing key id", mutate: func(c *push.Config) { c.APNS.KeyID = "" }},
		{name: "missing key", mutate: func(c *push.Config) { c.APNS.PrivateKey = "" }},
		{name: "bad key", mutate: func(c *push.Config) { c.APNS.PrivateKey = "not pem" }},
		{name: "bad push type", mutate: func(c *push.Config) { c.APNS.PushType = "silent" }},
		{name: "bad endpoint", mutate: func(c *push.Config) { c.APNS.Endpoint = "ftp://example.com" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(t, "https://api.push.apple.com")
			tt.mutate(&cfg)
			if _, err := Open(context.Background(), cfg); !errors.Is(err, push.ErrInvalidConfig) {
				t.Fatalf("err=%v want ErrInvalidConfig", err)
			}
		})
	}
}

func TestCanceledContext(t *testing.T) {
	sender, err := Open(context.Background(), testConfig(t, "http://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	msg := pushtest.ValidMessage()
	msg.Token = testToken
	if _, err := sender.Send(ctx, msg); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
}

func testConfig(t *testing.T, endpoint string) push.Config {
	t.Helper()
	return push.Config{
		Driver: "apns",
		Clock:  clock.NewManual(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)),
		APNS: push.APNSConfig{
			Endpoint:   endpoint,
			Topic:      "com.example.app",
			KeyID:      "KEYID",
			TeamID:     "TEAMID",
			PrivateKey: generateKeyPEM(t),
			PushType:   "alert",
		},
	}
}

func generateKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func generateKeyPEM(t *testing.T) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(generateKey(t))
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func decodeSegment(t *testing.T, segment string) map[string]any {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal segment: %v", err)
	}
	return out
}
