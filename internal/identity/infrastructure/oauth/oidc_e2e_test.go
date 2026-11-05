package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// fakeIDP 是端到端测试用的最小 OIDC 提供方：授权端点签发一次性授权码，
// 令牌端点校验 PKCE 并签发 id_token，JWKS 端点暴露当前 RSA 公钥。
type fakeIDP struct {
	t           *testing.T
	mu          sync.Mutex
	key         *rsa.PrivateKey
	kid         string
	issuer      string
	clientID    string
	redirectURI string
	codes       map[string]*fakeAuthCode
}

// fakeAuthCode 记录授权码绑定的 PKCE challenge、nonce 与主体。
type fakeAuthCode struct {
	challenge string
	nonce     string
	subject   string
	used      bool
}

// newFakeIDP 生成 RSA 密钥并返回测试 IdP。
func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return &fakeIDP{
		t:           t,
		key:         key,
		kid:         "kid-1",
		issuer:      "https://idp.example.com",
		clientID:    "client-1",
		redirectURI: "https://c.example.com/callback",
		codes:       map[string]*fakeAuthCode{},
	}
}

// handler 提供 /authorize、/token 与 /jwks。
func (f *fakeIDP) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			f.authorize(w, r)
		case "/token":
			f.token(w, r)
		case "/jwks":
			f.mu.Lock()
			key, kid := f.key, f.kid
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(jwksJSON(kid, &key.PublicKey)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// authorize 校验授权请求并重定向回 redirect_uri（携带 code 与 state）。
func (f *fakeIDP) authorize(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Get("response_type") != "code" ||
		query.Get("client_id") != f.clientID ||
		query.Get("redirect_uri") != f.redirectURI ||
		query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") == "" {
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, "server_error", http.StatusInternalServerError)
		return
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	f.mu.Lock()
	f.codes[code] = &fakeAuthCode{
		challenge: query.Get("code_challenge"),
		nonce:     query.Get("nonce"),
		subject:   "user-1",
	}
	f.mu.Unlock()

	redirect := fmt.Sprintf("%s?code=%s&state=%s",
		f.redirectURI, url.QueryEscape(code), url.QueryEscape(query.Get("state")))
	http.Redirect(w, r, redirect, http.StatusFound)
}

// token 校验一次性授权码与 PKCE，签发 id_token。
func (f *fakeIDP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}
	code := r.PostForm.Get("code")
	if code == "" || r.PostForm.Get("grant_type") != "authorization_code" {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	f.mu.Lock()
	entry, ok := f.codes[code]
	if !ok || entry.used || entry.challenge != challenge {
		f.mu.Unlock()
		http.Error(w, "invalid_grant", http.StatusBadRequest)
		return
	}
	entry.used = true
	key, kid := f.key, f.kid
	subject, nonce := entry.subject, entry.nonce
	f.mu.Unlock()

	claims := jwtv5.MapClaims{
		"iss":   f.issuer,
		"aud":   f.clientID,
		"sub":   subject,
		"nonce": nonce,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
	}
	w.Header().Set("Content-Type", "application/json")
	//nolint:gosec // G117: 测试 IdP 返回固定假令牌，字段名命中密钥模式但非硬编码凭证。
	_ = json.NewEncoder(w).Encode(tokenResponse{
		AccessToken: "access-1",
		IDToken:     signIDToken(f.t, key, kid, claims),
		TokenType:   "Bearer",
	})
}

// TestOIDCEndToEndAuthorizationCodeFlow 覆盖授权码 + PKCE + nonce 的完整链路：
// 授权请求 → IdP 重定向 → 换取 id_token → 主体映射，并验证授权码重放与 nonce 不匹配被拒。
func TestOIDCEndToEndAuthorizationCodeFlow(t *testing.T) {
	idp := newFakeIDP(t)
	server := httptest.NewServer(idp.handler())
	t.Cleanup(server.Close)

	provider, err := NewOAuth2Provider(Config{
		Name:         "oidc",
		ClientID:     idp.clientID,
		ClientSecret: "secret-1",
		AuthURL:      server.URL + "/authorize",
		TokenURL:     server.URL + "/token",
		RedirectURI:  idp.redirectURI,
		Scopes:       []string{"profile"},
		OIDCEnabled:  true,
		Issuer:       idp.issuer,
		JWKSURL:      server.URL + "/jwks",
		Audience:     idp.clientID,
		JWKSTTL:      15 * time.Minute,
		Leeway:       30 * time.Second,
	})
	if err != nil {
		t.Fatalf("new oidc provider: %v", err)
	}
	if !provider.OIDCEnabled() {
		t.Fatal("oidc must be enabled")
	}

	const (
		state      = "state-e2e"
		verifier   = "verifier-e2e"
		nonce      = "nonce-e2e"
		wrongNonce = "nonce-replayed"
	)
	authorizeURL, err := provider.AuthorizationURLWithNonce(state, verifier, nonce)
	if err != nil {
		t.Fatalf("authorization url: %v", err)
	}

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Get(authorizeURL)
	if err != nil {
		t.Fatalf("authorize request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	location := response.Header.Get("Location")
	redirected, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if got := redirected.Query().Get("state"); got != state {
		t.Fatalf("state = %q, want %q", got, state)
	}
	code := redirected.Query().Get("code")
	if code == "" {
		t.Fatal("authorization code is missing")
	}

	info, err := provider.ExchangeWithNonce(context.Background(), code, verifier, nonce)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if info.Subject != "user-1" {
		t.Fatalf("subject = %q, want user-1", info.Subject)
	}

	// 同一授权码重放必须被 IdP 拒绝，登录侧返回 40100。
	if _, err := provider.ExchangeWithNonce(context.Background(), code, verifier, nonce); err == nil {
		t.Fatal("replayed code must be rejected")
	} else {
		requireCode(t, err, apperr.CodeUnauthorized)
	}

	// 重新发起一次授权，但用上一个 nonce 回调：nonce 必须绑定本次会话。
	secondURL, err := provider.AuthorizationURLWithNonce("state-second", "verifier-second", "nonce-second")
	if err != nil {
		t.Fatalf("second authorization url: %v", err)
	}
	secondResponse, err := client.Get(secondURL)
	if err != nil {
		t.Fatalf("second authorize request: %v", err)
	}
	defer func() { _ = secondResponse.Body.Close() }()
	second, err := url.Parse(secondResponse.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse second redirect: %v", err)
	}
	_, err = provider.ExchangeWithNonce(
		context.Background(), second.Query().Get("code"), "verifier-second", wrongNonce,
	)
	requireCode(t, err, apperr.CodeUnauthorized)
}
