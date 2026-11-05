package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// oidcFixture 模拟 IdP：可变 RSA 密钥、kid 与当前签发的 id_token。
type oidcFixture struct {
	mu      sync.Mutex
	key     *rsa.PrivateKey
	kid     string
	idToken string
	// jwksStatus 非 0 时 /jwks 直接返回该状态码，用于模拟刷新失败。
	jwksStatus int
}

func newOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return &oidcFixture{key: key, kid: "key-1"}
}

func (f *oidcFixture) snapshot() (key *rsa.PrivateKey, kid, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key, f.kid, f.idToken
}

func (f *oidcFixture) setIDToken(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idToken = token
}

func (f *oidcFixture) rotate(t *testing.T) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rotate rsa key: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key = key
	f.kid = "key-2"
}

// setJWKSStatus 模拟 JWKS 端点故障；0 表示恢复正常。
func (f *oidcFixture) setJWKSStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jwksStatus = status
}

// handler 提供 /jwks 与 /token。
func (f *oidcFixture) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			f.mu.Lock()
			status := f.jwksStatus
			f.mu.Unlock()
			if status != 0 {
				w.WriteHeader(status)
				return
			}
			key, kid, _ := f.snapshot()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(jwksJSON(kid, &key.PublicKey)))
		case "/token":
			_, _, token := f.snapshot()
			w.Header().Set("Content-Type", "application/json")
			//nolint:gosec // G117: 测试 IdP 返回固定假令牌，字段名命中密钥模式但非硬编码凭证。
			_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "access-1", IDToken: token, TokenType: "Bearer"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func jwksJSON(kid string, public *rsa.PublicKey) string {
	return fmt.Sprintf(
		`{"keys":[{"kty":"RSA","kid":%q,"use":"sig","alg":"RS256","n":%q,"e":%q}]}`,
		kid,
		base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
		base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
	)
}

// signIDToken 用 RS256 签发 id_token。
func signIDToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwtv5.MapClaims) string {
	t.Helper()
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign id token: %v", err)
	}
	return raw
}

func newOIDCProvider(t *testing.T, server *httptest.Server) *OAuth2Provider {
	t.Helper()
	provider, err := NewOAuth2Provider(Config{
		Name:         "oidc",
		ClientID:     "client-1",
		ClientSecret: strings.Repeat("s", 8),
		AuthURL:      "https://idp.example.com/authorize",
		TokenURL:     server.URL + "/token",
		RedirectURI:  "https://c.example.com/callback",
		Scopes:       []string{"profile"},
		OIDCEnabled:  true,
		Issuer:       "https://idp.example.com",
		JWKSURL:      server.URL + "/jwks",
		Audience:     "client-1",
		JWKSTTL:      50 * time.Millisecond,
		// 测试里缩短刷新窗口，便于验证密钥轮换。
		JWKSMinRefreshInterval: 10 * time.Millisecond,
		Leeway:                 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("new oidc provider: %v", err)
	}
	return provider
}

func TestOIDCProviderAuthorizationURLWithNonce(t *testing.T) {
	fixture := newOIDCFixture(t)
	server := httptest.NewServer(fixture.handler(t))
	t.Cleanup(server.Close)
	provider := newOIDCProvider(t, server)

	raw, err := provider.AuthorizationURLWithNonce("state-1", "verifier-1", "nonce-1")
	if err != nil {
		t.Fatalf("authorization url: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if got := parsed.Query().Get("nonce"); got != "nonce-1" {
		t.Fatalf("nonce = %q, want nonce-1", got)
	}
	if got := parsed.Query().Get("scope"); got != "profile openid" {
		t.Fatalf("scope = %q, want profile openid", got)
	}
}

func TestOIDCProviderExchangeVerifiesIDToken(t *testing.T) {
	const (
		issuer   = "https://idp.example.com"
		audience = "client-1"
		nonce    = "nonce-1"
	)
	cases := []struct {
		name    string
		claims  jwtv5.MapClaims
		wantErr int
	}{
		{
			name: "success",
			claims: jwtv5.MapClaims{
				"iss": issuer, "aud": audience, "sub": "user-1", "nonce": nonce,
				"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
			},
		},
		{
			name: "nonce_mismatch",
			claims: jwtv5.MapClaims{
				"iss": issuer, "aud": audience, "sub": "user-1", "nonce": "other",
				"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
			},
			wantErr: apperr.CodeUnauthorized,
		},
		{
			name: "issuer_mismatch",
			claims: jwtv5.MapClaims{
				"iss": "https://evil.example.com", "aud": audience, "sub": "user-1", "nonce": nonce,
				"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
			},
			wantErr: apperr.CodeUnauthorized,
		},
		{
			name: "audience_mismatch",
			claims: jwtv5.MapClaims{
				"iss": issuer, "aud": "other-client", "sub": "user-1", "nonce": nonce,
				"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
			},
			wantErr: apperr.CodeUnauthorized,
		},
		{
			name: "expired",
			claims: jwtv5.MapClaims{
				"iss": issuer, "aud": audience, "sub": "user-1", "nonce": nonce,
				"iat": time.Now().Add(-10 * time.Minute).Unix(), "exp": time.Now().Add(-5 * time.Minute).Unix(),
			},
			wantErr: apperr.CodeUnauthorized,
		},
		{
			name: "missing_subject",
			claims: jwtv5.MapClaims{
				"iss": issuer, "aud": audience, "nonce": nonce,
				"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
			},
			wantErr: apperr.CodeUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newOIDCFixture(t)
			server := httptest.NewServer(fixture.handler(t))
			t.Cleanup(server.Close)
			provider := newOIDCProvider(t, server)
			key, kid, _ := fixture.snapshot()
			fixture.setIDToken(signIDToken(t, key, kid, tc.claims))

			info, err := provider.ExchangeWithNonce(context.Background(), "code-1", "verifier-1", nonce)
			if tc.wantErr != 0 {
				requireCode(t, err, tc.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("exchange: %v", err)
			}
			if info.Subject != "user-1" {
				t.Fatalf("subject = %q, want user-1", info.Subject)
			}
		})
	}
}

func TestOIDCProviderRejectsPlainExchange(t *testing.T) {
	fixture := newOIDCFixture(t)
	server := httptest.NewServer(fixture.handler(t))
	t.Cleanup(server.Close)
	provider := newOIDCProvider(t, server)
	key, kid, _ := fixture.snapshot()
	fixture.setIDToken(signIDToken(t, key, kid, jwtv5.MapClaims{
		"iss": "https://idp.example.com", "aud": "client-1", "sub": "user-1", "nonce": "nonce-1",
		"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
	}))

	// OIDC 模式禁止无 nonce 回退到 userinfo，避免绕过签名校验。
	_, err := provider.Exchange(context.Background(), "code-1", "verifier-1")
	requireCode(t, err, apperr.CodeUnauthorized)
}

func TestJWKSRefreshesForRotatedKey(t *testing.T) {
	fixture := newOIDCFixture(t)
	server := httptest.NewServer(fixture.handler(t))
	t.Cleanup(server.Close)

	now := time.Now()
	keys, err := newJWKS(jwksConfig{
		URL:                server.URL + "/jwks",
		TTL:                time.Minute,
		MinRefreshInterval: 10 * time.Millisecond,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new jwks: %v", err)
	}
	if _, err := keys.key(context.Background(), "key-1", "RS256"); err != nil {
		t.Fatalf("initial key: %v", err)
	}

	fixture.rotate(t)
	now = now.Add(20 * time.Millisecond)
	if _, err := keys.key(context.Background(), "key-2", "RS256"); err != nil {
		t.Fatalf("rotated key after refresh: %v", err)
	}
	_, unknownErr := keys.key(context.Background(), "unknown-key", "RS256")
	requireCode(t, unknownErr, apperr.CodeUnauthorized)
}

// TestJWKSRetainsCacheWhenRefreshFails 验证 IdP 故障时保留旧公钥：
// 已缓存 kid 的合法令牌仍可校验；未知 kid 返回 50300 而不是降级到 userinfo。
func TestJWKSRetainsCacheWhenRefreshFails(t *testing.T) {
	fixture := newOIDCFixture(t)
	server := httptest.NewServer(fixture.handler(t))
	t.Cleanup(server.Close)

	now := time.Now()
	keys, err := newJWKS(jwksConfig{
		URL:                server.URL + "/jwks",
		TTL:                50 * time.Millisecond,
		MinRefreshInterval: 10 * time.Millisecond,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new jwks: %v", err)
	}
	if _, err := keys.key(context.Background(), "key-1", "RS256"); err != nil {
		t.Fatalf("initial key: %v", err)
	}

	fixture.setJWKSStatus(http.StatusInternalServerError)
	now = now.Add(100 * time.Millisecond) // 超过 TTL 且超过最小刷新间隔
	if _, err := keys.key(context.Background(), "key-1", "RS256"); err != nil {
		t.Fatalf("cached key must keep working when refresh fails: %v", err)
	}
	_, unknownErr := keys.key(context.Background(), "unknown-key", "RS256")
	if unknownErr == nil {
		t.Fatal("unknown kid during outage must fail closed")
	}
	if !errors.Is(unknownErr, apperr.ErrUnauthorized) && !errors.Is(unknownErr, apperr.ErrUnavailable) {
		t.Fatalf("unknown kid during outage = %v, want 40100/50300", unknownErr)
	}

	fixture.setJWKSStatus(0)
	fixture.rotate(t)
	now = now.Add(20 * time.Millisecond)
	if _, err := keys.key(context.Background(), "key-2", "RS256"); err != nil {
		t.Fatalf("rotated key after recovery: %v", err)
	}
	if age, ok := keys.age(); !ok || age < 0 {
		t.Fatalf("cache age = %v, %v, want non-negative", age, ok)
	}
}

// TestOIDCRequiresExplicitFlag 验证 OIDC 必须显式开启，避免仅凭 issuer/jwks 隐式切换登录语义。
func TestOIDCRequiresExplicitFlag(t *testing.T) {
	idp := "https://idp.example.com"
	base := Config{
		Name:         "oidc",
		ClientID:     "client-1",
		ClientSecret: strings.Repeat("s", 8),
		AuthURL:      idp + "/authorize",
		TokenURL:     idp + "/token",
		RedirectURI:  "https://c.example.com/callback",
	}
	implicit := base
	implicit.Issuer = "https://idp.example.com"
	implicit.JWKSURL = "https://idp.example.com/jwks"
	if _, err := NewOAuth2Provider(implicit); err == nil {
		t.Fatal("issuer/jwks without oidc_enabled must be rejected")
	}
	missing := base
	missing.OIDCEnabled = true
	if _, err := NewOAuth2Provider(missing); err == nil {
		t.Fatal("oidc_enabled without issuer/jwks must be rejected")
	}
}

func TestJWKSRejectsUnsupportedAlgorithm(t *testing.T) {
	fixture := newOIDCFixture(t)
	server := httptest.NewServer(fixture.handler(t))
	t.Cleanup(server.Close)
	keys, err := newJWKS(jwksConfig{URL: server.URL + "/jwks"})
	if err != nil {
		t.Fatalf("new jwks: %v", err)
	}
	_, keyErr := keys.key(context.Background(), "key-1", "HS256")
	requireCode(t, keyErr, apperr.CodeUnauthorized)
}
