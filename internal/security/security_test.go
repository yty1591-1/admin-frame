package security_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/jwt"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// serve 用给定中间件包裹 handler 并返回状态码。
func serve(t *testing.T, mw func(http.Handler) http.Handler, header string) int {
	t.Helper()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// TestDemoAuth 验证演示令牌的端别隔离。
func TestDemoAuth(t *testing.T) {
	mw := security.NewDemoAuth(auth.ChannelC)
	if code := serve(t, mw, "Bearer c.user-1"); code != http.StatusOK {
		t.Fatalf("valid token status=%d", code)
	}
	if code := serve(t, mw, "Bearer admin.user-1"); code != http.StatusUnauthorized {
		t.Fatalf("cross-channel token status=%d want 401", code)
	}
	if code := serve(t, mw, ""); code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d want 401", code)
	}
	if code := serve(t, mw, "Bearer c."); code != http.StatusUnauthorized {
		t.Fatalf("malformed token status=%d want 401", code)
	}
}

// TestJWTConfigValidate 覆盖配置校验分支（不读取密钥文件）。
func TestJWTConfigValidate(t *testing.T) {
	valid := security.JWTConfig{
		Alg:      security.JWTAlgHS256,
		Issuer:   "issuer",
		Audience: []string{"admin"},
		Secret:   strings.Repeat("s", 32),
	}
	if err := valid.Validate("admin"); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := []struct {
		name string
		cfg  security.JWTConfig
		want string
	}{
		{"bad alg", security.JWTConfig{Alg: "none", Issuer: "i", Audience: []string{"admin"}, Secret: strings.Repeat("s", 32)}, "alg unsupported"},
		{"no issuer", security.JWTConfig{Audience: []string{"admin"}, Secret: strings.Repeat("s", 32)}, "issuer is required"},
		{"no audience", security.JWTConfig{Issuer: "i", Secret: strings.Repeat("s", 32)}, "audience is required"},
		{"audience mismatch", security.JWTConfig{Issuer: "i", Audience: []string{"biz"}, Secret: strings.Repeat("s", 32)}, "must contain channel"},
		{"short secret", security.JWTConfig{Issuer: "i", Audience: []string{"admin"}, Secret: "short"}, "at least 32 bytes"},
		{"rsa without key", security.JWTConfig{Alg: security.JWTAlgRS256, Issuer: "i", Audience: []string{"admin"}}, "public_key_file is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate("admin")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

// TestNewJWTAuthRejectsChannelClaimMismatch 验证跨端复用令牌被拒绝（SEC-03）。
func TestNewJWTAuthRejectsChannelClaimMismatch(t *testing.T) {
	secret := testJWTSecret('s')
	cfg := security.JWTConfig{
		Issuer:   "issuer",
		Audience: []string{"c"},
		Secret:   secret,
	}
	mw, err := security.NewJWTAuth(cfg, auth.ChannelC)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "issuer",
		Audience: []string{"c"},
		Secret:   []byte(secret),
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign(jwt.Claims{Subject: "u-1", Extra: map[string]any{"channel": "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if code := serve(t, mw, "Bearer "+token); code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", code)
	}
}

// TestNewJWTAuthRS256 验证公钥文件加载与签名校验链路。
func TestNewJWTAuthRS256(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jwt-public.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	mw, err := security.NewJWTAuth(security.JWTConfig{
		Alg:           security.JWTAlgRS256,
		Issuer:        "issuer",
		Audience:      []string{"admin"},
		PublicKeyFile: path,
	}, auth.ChannelAdmin)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jwt.NewRSA(jwt.RSAConfig{
		Issuer:     "issuer",
		Audience:   []string{"admin"},
		PrivateKey: key,
		TTL:        time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign(jwt.Claims{Subject: "admin-1"})
	if err != nil {
		t.Fatal(err)
	}
	if code := serve(t, mw, "Bearer "+token); code != http.StatusOK {
		t.Fatalf("status=%d want 200", code)
	}
}

// TestAuthorizer 验证权限中间件的 401/403/200 三态与注入的角色。
func TestAuthorizer(t *testing.T) {
	checker := rbac.New()
	checker.GrantRole("ops", "file:read")
	authorizer := security.NewAuthorizer(checker)

	read := authorizer.Require("file:read")
	write := authorizer.Require("file:write")

	// 未认证。
	if code := serve(t, read, ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d want 401", code)
	}

	withPrincipal := func(p auth.Principal) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
			})
		}
	}
	ops := withPrincipal(auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u", Roles: []string{"ops"}})
	if code := serve(t, func(h http.Handler) http.Handler { return ops(read(h)) }, ""); code != http.StatusOK {
		t.Fatalf("granted status=%d want 200", code)
	}
	if code := serve(t, func(h http.Handler) http.Handler { return ops(write(h)) }, ""); code != http.StatusForbidden {
		t.Fatalf("denied status=%d want 403", code)
	}

	// nil Authorizer 必须 fail-closed（401/403 均可，但绝不能 200）。
	if code := serve(t, security.NewAuthorizer(nil).Require("file:read"), ""); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("nil checker status=%d want 401/403", code)
	}
}

// TestAuthorizerRequireAny 验证任一权限点即可放行。
func TestAuthorizerRequireAny(t *testing.T) {
	checker := rbac.New()
	checker.GrantRole("ops", "file:read")
	authorizer := security.NewAuthorizer(checker)

	mw := authorizer.RequireAny("file:write", "file:read")
	withOps := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithPrincipal(r.Context(), auth.Principal{Roles: []string{"ops"}})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	if code := serve(t, func(next http.Handler) http.Handler { return withOps(mw(next)) }, ""); code != http.StatusOK {
		t.Fatalf("status=%d want 200", code)
	}
}
