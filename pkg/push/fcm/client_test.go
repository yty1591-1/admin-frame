package fcm

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
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

const testFCMToken = "fcm-token-0123456789abcdef0123456789abcdef"

func TestSendSuccess(t *testing.T) {
	var mu sync.Mutex
	tokenRequests := 0
	var sendBody map[string]any
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			mu.Lock()
			tokenRequests++
			mu.Unlock()
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				t.Fatalf("grant type=%q", r.Form.Get("grant_type"))
			}
			assertJWT(t, r.Form.Get("assertion"))
			_, _ = w.Write([]byte(`{"access_token":"token-1","expires_in":3600,"token_type":"Bearer"}`))
		case "/v1/projects/test-project/messages:send":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			mu.Lock()
			authorization = r.Header.Get("authorization")
			sendBody = decoded
			mu.Unlock()
			_, _ = w.Write([]byte(`{"name":"projects/test-project/messages/123"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	sender, err := Open(context.Background(), testConfig(t, server.URL))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()
	msg := pushtest.ValidMessage()
	msg.Platform = push.PlatformAndroid
	msg.Token = testFCMToken
	receipt, err := sender.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if receipt.Provider != "fcm" || receipt.MessageID != "projects/test-project/messages/123" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	mu.Lock()
	defer mu.Unlock()
	if tokenRequests != 1 {
		t.Fatalf("token requests=%d want 1", tokenRequests)
	}
	if authorization != "Bearer token-1" {
		t.Fatalf("authorization=%q", authorization)
	}
	message, ok := sendBody["message"].(map[string]any)
	if !ok {
		t.Fatalf("message missing: %v", sendBody)
	}
	if message["token"] != testFCMToken {
		t.Fatalf("token=%v", message["token"])
	}
	notification, ok := message["notification"].(map[string]any)
	if !ok || notification["title"] != msg.Title || notification["body"] != msg.Body {
		t.Fatalf("notification=%v", message["notification"])
	}
	data, ok := message["data"].(map[string]any)
	if !ok || data["order_id"] != "123" {
		t.Fatalf("data=%v", message["data"])
	}
	android, ok := message["android"].(map[string]any)
	if !ok || android["priority"] != "HIGH" || android["ttl"] != "3600s" {
		t.Fatalf("android=%v", message["android"])
	}
	if android["collapse_key"] != msg.CollapseKey {
		t.Fatalf("collapse=%v", android["collapse_key"])
	}
	apns, ok := message["apns"].(map[string]any)
	if !ok {
		t.Fatalf("apns missing: %v", message)
	}
	headers, ok := apns["headers"].(map[string]any)
	if !ok || headers["apns-priority"] != "10" || headers["apns-expiration"] == nil {
		t.Fatalf("apns headers=%v", apns["headers"])
	}
}

func TestTokenIsCached(t *testing.T) {
	var mu sync.Mutex
	tokenRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			mu.Lock()
			tokenRequests++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"access_token":"token-1","expires_in":3600}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"projects/test-project/messages/1"}`))
	}))
	defer server.Close()
	sender, err := Open(context.Background(), testConfig(t, server.URL))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()
	msg := pushtest.ValidMessage()
	msg.Platform = push.PlatformAndroid
	msg.Token = testFCMToken
	for i := 0; i < 2; i++ {
		if _, err := sender.Send(context.Background(), msg); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if tokenRequests != 1 {
		t.Fatalf("token requests=%d want 1", tokenRequests)
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		wantIs     error
		wantCode   pkgerrors.Code
		retryable  bool
	}{
		{
			name: "unregistered", status: 404,
			body:     `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`,
			wantIs:   push.ErrTokenInvalid,
			wantCode: pkgerrors.CodeFailedPrecondition,
		},
		{
			name: "invalid argument", status: 400,
			body:     `{"error":{"status":"INVALID_ARGUMENT"}}`,
			wantIs:   push.ErrRejected,
			wantCode: pkgerrors.CodeInvalidArgument,
		},
		{
			name: "quota exceeded", status: 429, retryAfter: "30",
			body:      `{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"errorCode":"QUOTA_EXCEEDED"}]}}`,
			wantCode:  pkgerrors.CodeResourceExhausted,
			retryable: true,
		},
		{
			name: "unavailable", status: 503,
			body:      `{"error":{"status":"UNAVAILABLE"}}`,
			wantCode:  pkgerrors.CodeUnavailable,
			retryable: true,
		},
		{
			name: "permission denied", status: 403,
			body:     `{"error":{"status":"PERMISSION_DENIED"}}`,
			wantCode: pkgerrors.CodePermissionDenied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					_, _ = w.Write([]byte(`{"access_token":"token-1","expires_in":3600}`))
					return
				}
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			sender, err := Open(context.Background(), testConfig(t, server.URL))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = sender.Close() }()
			msg := pushtest.ValidMessage()
			msg.Platform = push.PlatformAndroid
			msg.Token = testFCMToken
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
				if got := push.RetryAfterOf(err); got != 30*time.Second {
					t.Fatalf("retry after=%s", got)
				}
			}
			if strings.Contains(err.Error(), testFCMToken) {
				t.Fatalf("error leaks token: %v", err)
			}
		})
	}
}

func TestUnauthorizedRefreshesToken(t *testing.T) {
	var mu sync.Mutex
	tokenRequests := 0
	sendRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			mu.Lock()
			tokenRequests++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"access_token":"token","expires_in":3600}`))
			return
		}
		mu.Lock()
		sendRequests++
		call := sendRequests
		mu.Unlock()
		if call == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"status":"UNAUTHENTICATED"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"projects/test-project/messages/2"}`))
	}))
	defer server.Close()
	sender, err := Open(context.Background(), testConfig(t, server.URL))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()
	msg := pushtest.ValidMessage()
	msg.Platform = push.PlatformAndroid
	msg.Token = testFCMToken
	if _, err := sender.Send(context.Background(), msg); err == nil {
		t.Fatal("first send should fail")
	}
	if _, err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("second send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if tokenRequests != 2 {
		t.Fatalf("token requests=%d want 2", tokenRequests)
	}
}

func TestPlatformMismatch(t *testing.T) {
	sender, err := Open(context.Background(), testConfig(t, "http://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = sender.Close() }()
	msg := pushtest.ValidMessage()
	msg.Platform = push.PlatformIOS
	if _, err := sender.Send(context.Background(), msg); !errors.Is(err, push.ErrNotSupported) {
		t.Fatalf("err=%v want ErrNotSupported", err)
	}
}

func TestOpenValidatesConfig(t *testing.T) {
	tests := []struct {
		name   string
		config func(t *testing.T) push.Config
	}{
		{name: "missing credentials", config: func(_ *testing.T) push.Config {
			return push.Config{Driver: "fcm"}
		}},
		{name: "bad json", config: func(_ *testing.T) push.Config {
			return push.Config{Driver: "fcm", FCM: push.FCMConfig{CredentialsJSON: "{not json"}}
		}},
		{name: "missing key", config: func(_ *testing.T) push.Config {
			return push.Config{Driver: "fcm", FCM: push.FCMConfig{ //nolint:gosec // 测试用假凭据。
				CredentialsJSON: `{"client_email":"a@b.c"}`,
			}}
		}},
		{name: "bad private key", config: func(_ *testing.T) push.Config {
			raw, _ := json.Marshal(map[string]string{
				"client_email": "svc@example.com",
				"private_key":  "not pem",
				"project_id":   "p",
			})
			return push.Config{Driver: "fcm", FCM: push.FCMConfig{CredentialsJSON: string(raw)}}
		}},
		{name: "missing project", config: func(t *testing.T) push.Config {
			raw, _ := json.Marshal(map[string]string{
				"client_email": "svc@example.com",
				"private_key":  generateRSAKeyPEM(t),
			})
			return push.Config{Driver: "fcm", FCM: push.FCMConfig{CredentialsJSON: string(raw)}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Open(context.Background(), tt.config(t)); !errors.Is(err, push.ErrInvalidConfig) {
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
	msg.Platform = push.PlatformAndroid
	msg.Token = testFCMToken
	if _, err := sender.Send(ctx, msg); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
}

func testConfig(t *testing.T, serverURL string) push.Config {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"project_id":   "test-project",
		"client_email": "svc@example.com",
		"private_key":  generateRSAKeyPEM(t),
		"token_uri":    serverURL + "/token",
	})
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	return push.Config{
		Driver: "fcm",
		Clock:  clock.NewManual(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)),
		FCM: push.FCMConfig{
			Endpoint:        serverURL,
			ProjectID:       "test-project",
			CredentialsJSON: string(raw),
		},
	}
}

func generateRSAKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal rsa key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func assertJWT(t *testing.T, assertion string) {
	t.Helper()
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion parts=%d", len(parts))
	}
	for _, segment := range parts[:2] {
		raw, err := base64.RawURLEncoding.DecodeString(segment)
		if err != nil {
			t.Fatalf("decode segment: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("unmarshal segment: %v", err)
		}
		if len(decoded) == 0 {
			t.Fatal("empty segment")
		}
	}
}
