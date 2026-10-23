package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// 默认 HTTP 超时与响应体上限。
const (
	defaultHTTPTimeout = 10 * time.Second
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
	// HTTPClient 是出站客户端；nil 时使用带超时的默认客户端。
	HTTPClient *http.Client
}

// OAuth2Provider 用标准库实现授权码 + PKCE(S256) 的 OAuth2 客户端。
type OAuth2Provider struct {
	cfg    Config
	client *http.Client
}

// 编译期断言。
var _ app.OAuthProvider = (*OAuth2Provider)(nil)

// NewOAuth2Provider 构造提供方；必填字段缺失时返回错误。
func NewOAuth2Provider(cfg Config) (*OAuth2Provider, error) {
	if strings.TrimSpace(cfg.Name) == "" ||
		strings.TrimSpace(cfg.ClientID) == "" ||
		strings.TrimSpace(cfg.ClientSecret) == "" ||
		strings.TrimSpace(cfg.AuthURL) == "" ||
		strings.TrimSpace(cfg.TokenURL) == "" ||
		strings.TrimSpace(cfg.UserInfoURL) == "" ||
		strings.TrimSpace(cfg.RedirectURI) == "" {
		return nil, fmt.Errorf("oauth provider %q is missing required config", cfg.Name)
	}
	subjectField := strings.TrimSpace(cfg.SubjectField)
	if subjectField == "" {
		subjectField = "sub"
	}
	cfg.SubjectField = subjectField
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &OAuth2Provider{cfg: cfg, client: client}, nil
}

// Name 返回提供方标识。
func (p *OAuth2Provider) Name() string { return p.cfg.Name }

// AuthorizationURL 构造授权地址；code_challenge = BASE64URL(SHA256(verifier))。
func (p *OAuth2Provider) AuthorizationURL(state, codeVerifier string) (string, error) {
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
	if len(p.cfg.Scopes) > 0 {
		query.Set("scope", strings.Join(p.cfg.Scopes, " "))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// Exchange 用授权码换取 access_token，再读取 userinfo 中的主体标识。
func (p *OAuth2Provider) Exchange(ctx context.Context, code, codeVerifier string) (app.OAuthIdentityInfo, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(codeVerifier) == "" {
		return app.OAuthIdentityInfo{}, apperr.Unauthorized("invalid oauth authorization")
	}
	token, err := p.exchangeToken(ctx, code, codeVerifier)
	if err != nil {
		return app.OAuthIdentityInfo{}, err
	}
	return p.fetchUserInfo(ctx, token)
}

// exchangeToken 调用令牌端点换取 access_token。
func (p *OAuth2Provider) exchangeToken(ctx context.Context, code, codeVerifier string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.cfg.RedirectURI)
	form.Set("client_id", p.cfg.ClientID)
	form.Set("code_verifier", codeVerifier)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build oauth token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.SetBasicAuth(p.cfg.ClientID, p.cfg.ClientSecret)

	response, err := p.client.Do(request)
	if err != nil {
		return "", oauthUnavailable(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return "", oauthUnavailable(err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		if response.StatusCode < http.StatusInternalServerError {
			return "", apperr.Unauthorized("oauth authorization rejected")
		}
		return "", apperr.Unavailable("oauth provider unavailable")
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", apperr.Unavailable("oauth provider returned invalid token response")
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return "", apperr.Unauthorized("oauth authorization rejected")
	}
	return payload.AccessToken, nil
}

// fetchUserInfo 读取 userinfo 并按 subject_field 解析主体标识。
func (p *OAuth2Provider) fetchUserInfo(ctx context.Context, accessToken string) (app.OAuthIdentityInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserInfoURL, nil)
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
