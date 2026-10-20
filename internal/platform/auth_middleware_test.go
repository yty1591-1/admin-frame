package platform_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

func TestNewAuthMiddlewareRejectsProductionWithoutAuthenticator(t *testing.T) {
	_, err := platform.NewAuthMiddleware(
		platform.AuthConfig{ExpectedChannel: "c"}, auth.ChannelC, "prod")
	if err == nil {
		t.Fatal("expected error when prod has no jwt configuration")
	}
}

func TestNewAuthMiddlewareRejectsChannelMismatch(t *testing.T) {
	_, err := platform.NewAuthMiddleware(
		platform.AuthConfig{Mode: platform.AuthModeDemo, ExpectedChannel: "admin"}, auth.ChannelC, "test")
	if err == nil {
		t.Fatal("expected channel mismatch")
	}
}

func TestNewAuthMiddlewareRejectsDemoInProd(t *testing.T) {
	_, err := platform.NewAuthMiddleware(
		platform.AuthConfig{Mode: platform.AuthModeDemo, ExpectedChannel: "c"}, auth.ChannelC, "prod")
	if err == nil {
		t.Fatal("expected demo mode to be rejected when env=prod")
	}
}

func TestNewAuthMiddlewareDemo(t *testing.T) {
	mw, err := platform.NewAuthMiddleware(
		platform.AuthConfig{Mode: platform.AuthModeDemo, ExpectedChannel: "c"}, auth.ChannelC, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if mw == nil {
		t.Fatal("nil middleware")
	}

	var got auth.Principal
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = auth.FromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer c.user-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got.SubjectID != "user-1" || got.Channel != auth.ChannelC {
		t.Fatalf("principal=%+v", got)
	}
}

func TestNewAuthMiddlewareProdDefaultsToJWT(t *testing.T) {
	// 未显式声明 auth.mode 时，prod 回退 jwt；提供合法 JWT 配置即可启动。
	const secret = "0123456789abcdef0123456789abcdef"
	mw, err := platform.NewAuthMiddleware(platform.AuthConfig{
		ExpectedChannel: "c",
		JWT: security.JWTConfig{
			Issuer:   "issuer",
			Audience: []string{"c"},
			Secret:   secret,
		},
	}, auth.ChannelC, "prod")
	if err != nil {
		t.Fatalf("prod fallback should use jwt: %v", err)
	}
	if mw == nil {
		t.Fatal("nil middleware")
	}
}

func TestNewAuthMiddlewareLocalDefaultsToDemo(t *testing.T) {
	// 未显式声明 auth.mode 时，非生产环境回退 demo，便于本地起服务。
	mw, err := platform.NewAuthMiddleware(
		platform.AuthConfig{ExpectedChannel: "c"}, auth.ChannelC, "local")
	if err != nil {
		t.Fatalf("local fallback should use demo: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer c.user-1")
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", rec.Code)
	}
}

func TestNewAuthMiddlewareJWT(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef" // 32 bytes
	cfg := platform.AuthConfig{
		Mode:            platform.AuthModeJWT,
		ExpectedChannel: "c",
		JWT: security.JWTConfig{
			Alg:      security.JWTAlgHS256,
			Issuer:   "test-issuer",
			Audience: []string{"c"},
			Secret:   secret,
		},
	}
	mw, err := platform.NewAuthMiddleware(cfg, auth.ChannelC, "prod")
	if err != nil {
		t.Fatal(err)
	}

	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "test-issuer",
		Audience: []string{"c"},
		Secret:   []byte(secret),
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign(jwt.Claims{Subject: "u-1", Extra: map[string]any{
		"merchant_id": "m-1",
		"roles":       []string{"admin"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	var got auth.Principal
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = auth.FromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got.SubjectID != "u-1" || got.MerchantID != "m-1" || len(got.Roles) != 1 {
		t.Fatalf("principal=%+v", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer invalid")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status=%d want 401", rec.Code)
	}
}
