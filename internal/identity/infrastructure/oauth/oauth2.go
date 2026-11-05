// Package oauth 适配 OAuth2 授权码 + PKCE 登录（docs/identity-authn.md §8.2）。
package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// 默认 HTTP 超时与响应体上限。
const (
	defaultHTTPTimeout = 5 * time.Second
	maxResponseBytes   = 1 << 20
)

// Config 是单个 OAuth2 提供方参数。
//
// ClientSecret 只允许由环境变量注入，禁止写入 YAML。
type Config struct {
	Name         string
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	RedirectURI  string
	Scopes       []string
	// SubjectField 是 userinfo JSON 中主体标识的字段名；默认 sub。
	SubjectField string
	// Timeout 单次出站调用超时；≤0 使用默认 5s（PERF-06）。
	Timeout time.Duration
	// HTTPClient 是出站客户端；nil 时使用带超时的默认客户端。
	HTTPClient *http.Client

	// OIDCEnabled 显式启用 OIDC（id_token 验签）；默认关闭，避免仅因误配 issuer 而改变登录语义。
	OIDCEnabled bool
	// 以下字段在 OIDCEnabled=true 时必填：校验 id_token 的签名、issuer、audience 与 nonce。
	Issuer  string
	JWKSURL string
	// Audience 是 id_token 的期望 audience；为空时使用 ClientID。
	Audience string
	// JWKSTTL 是公钥缓存时长；≤0 使用 15m。
	JWKSTTL time.Duration
	// JWKSMinRefreshInterval 是未知 kid 触发强制刷新的最小间隔；≤0 使用 1m。
	JWKSMinRefreshInterval time.Duration
	// Leeway 是 id_token 时间字段的时钟偏差容忍；≤0 使用 30s。
	Leeway time.Duration
	// Metrics 是 OIDC 公钥集刷新指标出口；可为 nil。
	Metrics app.Metrics
}

// OAuth2Provider 用标准库实现授权码 + PKCE(S256) 的 OAuth2 客户端。
type OAuth2Provider struct {
	cfg    Config
	client *http.Client
	jwks   *jwks
}

// 编译期断言。
var _ app.OAuthProvider = (*OAuth2Provider)(nil)
var _ app.OIDCProvider = (*OAuth2Provider)(nil)

// NewOAuth2Provider 构造提供方；必填字段缺失时返回错误。
func NewOAuth2Provider(cfg Config) (*OAuth2Provider, error) {
	if strings.TrimSpace(cfg.Name) == "" ||
		strings.TrimSpace(cfg.ClientID) == "" ||
		strings.TrimSpace(cfg.ClientSecret) == "" ||
		strings.TrimSpace(cfg.AuthURL) == "" ||
		strings.TrimSpace(cfg.TokenURL) == "" ||
		strings.TrimSpace(cfg.RedirectURI) == "" {
		return nil, fmt.Errorf("oauth provider %q is missing required config", cfg.Name)
	}
	oidcConfigured := strings.TrimSpace(cfg.Issuer) != "" || strings.TrimSpace(cfg.JWKSURL) != ""
	if cfg.OIDCEnabled && (strings.TrimSpace(cfg.Issuer) == "" || strings.TrimSpace(cfg.JWKSURL) == "") {
		return nil, fmt.Errorf("oauth provider %q must set both issuer and jwks url when oidc is enabled", cfg.Name)
	}
	if !cfg.OIDCEnabled && oidcConfigured {
		// 显式开关是安全基线：禁止仅凭 issuer/jwks 字段隐式启用，避免配置漂移绕过评审。
		return nil, fmt.Errorf("oauth provider %q sets issuer/jwks url without oidc_enabled", cfg.Name)
	}
	oidcEnabled := cfg.OIDCEnabled
	if !oidcEnabled && strings.TrimSpace(cfg.UserInfoURL) == "" {
		return nil, fmt.Errorf("oauth provider %q is missing required config", cfg.Name)
	}
	subjectField := strings.TrimSpace(cfg.SubjectField)
	if subjectField == "" {
		subjectField = "sub"
	}
	cfg.SubjectField = subjectField
	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultHTTPTimeout
		}
		client = &http.Client{Timeout: timeout}
	}
	provider := &OAuth2Provider{cfg: cfg, client: client}
	if oidcEnabled {
		keys, err := newJWKS(jwksConfig{
			URL:                cfg.JWKSURL,
			TTL:                cfg.JWKSTTL,
			MinRefreshInterval: cfg.JWKSMinRefreshInterval,
			Client:             client,
			Provider:           cfg.Name,
			Metrics:            cfg.Metrics,
		})
		if err != nil {
			return nil, fmt.Errorf("oauth provider %q: %w", cfg.Name, err)
		}
		provider.jwks = keys
	}
	return provider, nil
}

// Name 返回提供方标识。
func (p *OAuth2Provider) Name() string { return p.cfg.Name }

// AuthorizationURL 构造授权地址；code_challenge = BASE64URL(SHA256(verifier))。
func (p *OAuth2Provider) AuthorizationURL(state, codeVerifier string) (string, error) {
	return p.authorizationURL(state, codeVerifier, "")
}

// AuthorizationURLWithNonce 构造 OIDC 授权地址：强制 openid scope 并携带 nonce。
func (p *OAuth2Provider) AuthorizationURLWithNonce(state, codeVerifier, nonce string) (string, error) {
	if p.jwks == nil {
		return "", fmt.Errorf("oidc is not enabled for provider %q", p.cfg.Name)
	}
	if strings.TrimSpace(nonce) == "" {
		return "", fmt.Errorf("oidc nonce is required")
	}
	return p.authorizationURL(state, codeVerifier, nonce)
}

func (p *OAuth2Provider) authorizationURL(state, codeVerifier, nonce string) (string, error) {
	if strings.TrimSpace(state) == "" || strings.TrimSpace(codeVerifier) == "" {
		return "", fmt.Errorf("oauth state and code verifier are required")
	}
	parsed, err := url.Parse(p.cfg.AuthURL)
	if err != nil {
		return "", fmt.Errorf("parse oauth auth url: %w", err)
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", p.cfg.ClientID)
	query.Set("redirect_uri", p.cfg.RedirectURI)
	query.Set("state", state)
	query.Set("code_challenge", pkceChallenge(codeVerifier))
	query.Set("code_challenge_method", "S256")
	if nonce != "" {
		query.Set("nonce", nonce)
	}
	scopes := p.cfg.Scopes
	if nonce != "" && !containsScope(scopes, "openid") {
		scopes = append(append([]string(nil), scopes...), "openid")
	}
	if len(scopes) > 0 {
		query.Set("scope", strings.Join(scopes, " "))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// Exchange 用授权码换取 access_token，再读取 userinfo 中的主体标识。
func (p *OAuth2Provider) Exchange(ctx context.Context, code, codeVerifier string) (app.OAuthIdentityInfo, error) {
	return p.exchange(ctx, code, codeVerifier, "")
}

// ExchangeWithNonce 用授权码换取 id_token 并校验 nonce；OIDC 模式下主体取自 id_token sub。
func (p *OAuth2Provider) ExchangeWithNonce(ctx context.Context, code, codeVerifier, nonce string) (app.OAuthIdentityInfo, error) {
	if strings.TrimSpace(nonce) == "" {
		return app.OAuthIdentityInfo{}, apperr.Unauthorized("invalid oauth authorization")
	}
	return p.exchange(ctx, code, codeVerifier, nonce)
}

// OIDCEnabled 报告是否已启用 id_token 校验。
func (p *OAuth2Provider) OIDCEnabled() bool { return p.jwks != nil }

// JWKSCacheAge 返回距上次成功拉取 JWKS 的时长；纯 OAuth2 或从未成功时为 false。
func (p *OAuth2Provider) JWKSCacheAge() (time.Duration, bool) {
	if p.jwks == nil {
		return 0, false
	}
	return p.jwks.age()
}

func (p *OAuth2Provider) exchange(ctx context.Context, code, codeVerifier, nonce string) (app.OAuthIdentityInfo, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(codeVerifier) == "" {
		return app.OAuthIdentityInfo{}, apperr.Unauthorized("invalid oauth authorization")
	}
	token, err := p.exchangeToken(ctx, code, codeVerifier)
	if err != nil {
		return app.OAuthIdentityInfo{}, err
	}
	if p.jwks != nil {
		// OIDC 必须使用 nonce + id_token，禁止回退到 userinfo 绕过签名校验。
		if nonce == "" || strings.TrimSpace(token.IDToken) == "" {
			return app.OAuthIdentityInfo{}, apperr.Unauthorized("invalid oauth authorization")
		}
		subject, err := p.verifyIDToken(ctx, token.IDToken, nonce)
		if err != nil {
			return app.OAuthIdentityInfo{}, err
		}
		return app.OAuthIdentityInfo{Subject: subject}, nil
	}
	return p.fetchUserInfo(ctx, token.AccessToken)
}

// tokenResponse 是令牌端点的最小响应字段。
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

// exchangeToken 调用令牌端点换取 access_token（OIDC 时同时返回 id_token）。
func (p *OAuth2Provider) exchangeToken(ctx context.Context, code, codeVerifier string) (tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.cfg.RedirectURI)
	form.Set("client_id", p.cfg.ClientID)
	form.Set("code_verifier", codeVerifier)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("build oauth token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.SetBasicAuth(p.cfg.ClientID, p.cfg.ClientSecret)

	response, err := p.client.Do(request)
	if err != nil {
		return tokenResponse{}, oauthUnavailable(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return tokenResponse{}, oauthUnavailable(err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		if response.StatusCode < http.StatusInternalServerError {
			return tokenResponse{}, apperr.Unauthorized("oauth authorization rejected")
		}
		return tokenResponse{}, apperr.Unavailable("oauth provider unavailable")
	}

	var payload tokenResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return tokenResponse{}, apperr.Unavailable("oauth provider returned invalid token response")
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return tokenResponse{}, apperr.Unauthorized("oauth authorization rejected")
	}
	return payload, nil
}

// verifyIDToken 校验 id_token 的签名、issuer、audience、有效期与 nonce，返回 sub。
func (p *OAuth2Provider) verifyIDToken(ctx context.Context, raw, nonce string) (string, error) {
	audience := strings.TrimSpace(p.cfg.Audience)
	if audience == "" {
		audience = p.cfg.ClientID
	}
	leeway := p.cfg.Leeway
	if leeway <= 0 {
		leeway = 30 * time.Second
	}
	parser := jwtv5.NewParser(
		jwtv5.WithValidMethods([]string{"RS256"}),
		jwtv5.WithIssuer(strings.TrimSpace(p.cfg.Issuer)),
		jwtv5.WithAudience(audience),
		jwtv5.WithExpirationRequired(),
		jwtv5.WithIssuedAt(),
		jwtv5.WithLeeway(leeway),
	)
	claims := jwtv5.MapClaims{}
	token, err := parser.ParseWithClaims(raw, claims, func(token *jwtv5.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		return p.jwks.key(ctx, kid, token.Method.Alg())
	})
	if err != nil || !token.Valid {
		return "", apperr.Unauthorized("id_token verification failed")
	}
	tokenNonce, _ := claims["nonce"].(string)
	if tokenNonce == "" || subtle.ConstantTimeCompare([]byte(tokenNonce), []byte(nonce)) != 1 {
		return "", apperr.Unauthorized("id_token nonce mismatch")
	}
	subject, _ := claims["sub"].(string)
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", apperr.Unauthorized("id_token subject missing")
	}
	return subject, nil
}

// containsScope 判断 scope 列表是否已包含目标值。
func containsScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if strings.EqualFold(strings.TrimSpace(scope), target) {
			return true
		}
	}
	return false
}

// fetchUserInfo 读取 userinfo 并按 subject_field 解析主体标识。
func (p *OAuth2Provider) fetchUserInfo(ctx context.Context, accessToken string) (app.OAuthIdentityInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserInfoURL, http.NoBody)
	if err != nil {
		return app.OAuthIdentityInfo{}, fmt.Errorf("build oauth userinfo request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")

	response, err := p.client.Do(request)
	if err != nil {
		return app.OAuthIdentityInfo{}, oauthUnavailable(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return app.OAuthIdentityInfo{}, oauthUnavailable(err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		if response.StatusCode < http.StatusInternalServerError {
			return app.OAuthIdentityInfo{}, apperr.Unauthorized("oauth authorization rejected")
		}
		return app.OAuthIdentityInfo{}, apperr.Unavailable("oauth provider unavailable")
	}

	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return app.OAuthIdentityInfo{}, apperr.Unavailable("oauth provider returned invalid userinfo")
	}
	subject := subjectFromPayload(payload, p.cfg.SubjectField)
	if subject == "" {
		return app.OAuthIdentityInfo{}, apperr.Unauthorized("oauth authorization rejected")
	}
	return app.OAuthIdentityInfo{Subject: subject}, nil
}

// subjectFromPayload 把主体字段规范为字符串；支持字符串与数字。
func subjectFromPayload(payload map[string]any, field string) string {
	value, ok := payload[field]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

// pkceChallenge 返回 BASE64URL(SHA256(verifier))，无 padding。
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// oauthUnavailable 包装网络 / 解码错误为 50300，且不对外暴露敏感请求信息。
func oauthUnavailable(err error) error {
	return apperr.New(apperr.CodeServiceUnavailable, "oauth provider unavailable", err)
}
