package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// DefaultOAuthStateTTL 是默认 OAuth state 有效期（docs/identity-authn.md §8.2）。
const DefaultOAuthStateTTL = 10 * time.Minute

// oauthSecretBytes 是 state / PKCE verifier 的随机字节长度。
const oauthSecretBytes = 32

// OAuthIdentityInfo 是提供方返回的主体标识。
type OAuthIdentityInfo struct {
	// Subject 提供方侧主体标识（userinfo 的 sub）。
	Subject string
}

// OAuthProvider 是第三方身份提供方端口，由 infrastructure/oauth 适配。
type OAuthProvider interface {
	// Name 返回提供方标识，与配置和路由一致。
	Name() string
	// AuthorizationURL 构造授权端点 URL（含 state 与 PKCE challenge）。
	AuthorizationURL(state, codeVerifier string) (string, error)
	// Exchange 用授权码换取主体标识；4xx / invalid_grant 返回 40100，依赖故障返回 50300。
	Exchange(ctx context.Context, code, codeVerifier string) (OAuthIdentityInfo, error)
}

// OAuthState 是授权发起时保存的一次性上下文。
type OAuthState struct {
	Provider string
	Verifier string
}

// OAuthStateStore 是 state 的一次性消费存储。
type OAuthStateStore interface {
	Save(ctx context.Context, state string, value OAuthState, ttl time.Duration) error
	// Consume 一次性消费；不存在或已消费返回 apperr.NotFound。
	Consume(ctx context.Context, state string) (OAuthState, error)
}

// OAuthServiceConfig 是 OAuth 登录用例的装配参数。
type OAuthServiceConfig struct {
	Providers     map[string]OAuthProvider
	States        OAuthStateStore
	Identities    domain.OAuthIdentityRepository
	Accounts      domain.AccountRepository
	IDs           IDGenerator
	Auth          *AuthService
	AutoProvision bool
	StateTTL      time.Duration
}

// AuthorizeResult 是授权发起结果。
type AuthorizeResult struct {
	// AuthorizeURL 提供方授权地址。
	AuthorizeURL string
	// State 一次性 state。
	State string
}

// OAuthCallbackInput 是授权回调入参。
type OAuthCallbackInput struct {
	Provider string
	Code     string
	State    string
	// IPHash / UAHash 客户端摘要；由 transport 计算。
	IPHash string
	UAHash string
	// TraceID 请求关联 ID。
	TraceID string
}

// OAuthService 编排授权发起、回调换取与外部身份映射（docs/identity-authn.md §8.2）。
type OAuthService struct {
	providers     map[string]OAuthProvider
	states        OAuthStateStore
	identities    domain.OAuthIdentityRepository
	accounts      domain.AccountRepository
	ids           IDGenerator
	auth          *AuthService
	autoProvision bool
	stateTTL      time.Duration
}

// NewOAuthService 构造 OAuth 登录服务；依赖缺失或参数越界时返回错误。
func NewOAuthService(cfg OAuthServiceConfig) (*OAuthService, error) {
	if cfg.States == nil || cfg.Identities == nil || cfg.Accounts == nil ||
		cfg.IDs == nil || cfg.Auth == nil {
		return nil, apperr.Internal("oauth service dependencies are required", nil)
	}
	stateTTL := cfg.StateTTL
	if stateTTL <= 0 {
		stateTTL = DefaultOAuthStateTTL
	}
	providers := make(map[string]OAuthProvider, len(cfg.Providers))
	for name, provider := range cfg.Providers {
		if provider == nil {
			return nil, apperr.Internal("oauth provider is nil", nil)
		}
		providers[name] = provider
	}
	return &OAuthService{
		providers:     providers,
		states:        cfg.States,
		identities:    cfg.Identities,
		accounts:      cfg.Accounts,
		ids:           cfg.IDs,
		auth:          cfg.Auth,
		autoProvision: cfg.AutoProvision,
		stateTTL:      stateTTL,
	}, nil
}

// ProviderNames 返回已配置的 provider 名称（升序），供路由裁剪使用。
func (s *OAuthService) ProviderNames() []string {
	out := make([]string, 0, len(s.providers))
	for name := range s.providers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Authorize 生成一次性 state 与 PKCE verifier，并返回授权地址。
func (s *OAuthService) Authorize(ctx context.Context, provider string) (AuthorizeResult, error) {
	name := strings.TrimSpace(provider)
	providerImpl, ok := s.providers[name]
	if !ok {
		return AuthorizeResult{}, apperr.NotFound("oauth provider is not configured")
	}
	state, err := randomURLSafe(oauthSecretBytes)
	if err != nil {
		return AuthorizeResult{}, apperr.Unavailable("oauth state generation failed")
	}
	verifier, err := randomURLSafe(oauthSecretBytes)
	if err != nil {
		return AuthorizeResult{}, apperr.Unavailable("oauth state generation failed")
	}
	if err := s.states.Save(ctx, state, OAuthState{Provider: name, Verifier: verifier}, s.stateTTL); err != nil {
		return AuthorizeResult{}, apperr.Unavailable("oauth state store unavailable")
	}
	url, err := providerImpl.AuthorizationURL(state, verifier)
	if err != nil {
		return AuthorizeResult{}, apperr.Internal("build oauth authorize url failed", err)
	}
	return AuthorizeResult{AuthorizeURL: url, State: state}, nil
}

// Callback 校验 state 与授权码，完成身份映射与登录。
func (s *OAuthService) Callback(ctx context.Context, in OAuthCallbackInput) (LoginResult, error) {
	name := strings.TrimSpace(in.Provider)
	code := strings.TrimSpace(in.Code)
	state := strings.TrimSpace(in.State)
	if code == "" || state == "" {
		return LoginResult{}, oauthInvalid()
	}
	provider, ok := s.providers[name]
	if !ok {
		return LoginResult{}, oauthInvalid()
	}
	saved, err := s.states.Consume(ctx, state)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return LoginResult{}, oauthInvalid()
		}
		return LoginResult{}, apperr.Unavailable("oauth state store unavailable")
	}
	if saved.Provider != name || saved.Verifier == "" {
		return LoginResult{}, oauthInvalid()
	}

	info, err := provider.Exchange(ctx, code, saved.Verifier)
	if err != nil {
		switch {
		case errors.Is(err, apperr.ErrUnauthorized):
			return LoginResult{}, oauthInvalid()
		case errors.Is(err, apperr.ErrUnavailable):
			return LoginResult{}, apperr.Unavailable("oauth provider unavailable")
		default:
			return LoginResult{}, apperr.Internal("exchange oauth code failed", err)
		}
	}
	subject := strings.TrimSpace(info.Subject)
	if subject == "" {
		return LoginResult{}, oauthInvalid()
	}

	account, err := s.resolveAccount(ctx, name, subject)
	if err != nil {
		return LoginResult{}, err
	}
	return s.auth.CompleteVerifiedLogin(ctx, VerifiedLoginInput{
		AccountID: account.ID,
		IPHash:    in.IPHash,
		UAHash:    in.UAHash,
		TraceID:   in.TraceID,
	})
}

// resolveAccount 返回外部身份对应账号；未映射时按 auto_provision 决定是否建号。
func (s *OAuthService) resolveAccount(ctx context.Context, provider, subject string) (*domain.Account, error) {
	identity, err := s.identities.FindByProviderSubject(ctx, provider, subject)
	if err == nil {
		account, loadErr := s.accounts.FindByID(ctx, identity.AccountID)
		if loadErr != nil {
			if errors.Is(loadErr, apperr.ErrNotFound) {
				return nil, oauthInvalid()
			}
			return nil, apperr.Internal("load account failed", loadErr)
		}
		return account, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return nil, apperr.Unavailable("oauth identity store unavailable")
	}
	if !s.autoProvision {
		// D4：失败关闭，不自动建号。
		return nil, oauthInvalid()
	}
	return s.provisionAccount(ctx, provider, subject)
}

// provisionAccount 自动建号并写入外部身份映射。
func (s *OAuthService) provisionAccount(ctx context.Context, provider, subject string) (*domain.Account, error) {
	accountID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate account id failed", err)
	}
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      accountID,
		Channel: domain.ChannelC,
		Subject: oauthAccountSubject(provider, subject),
	})
	if err != nil {
		return nil, apperr.Internal("build account failed", err)
	}
	if err := s.accounts.Save(ctx, account, 0); err != nil {
		if errors.Is(err, apperr.ErrConflict) {
			// 并发建号：以已存在的账号为准，避免同一外部身份产生两个账号。
			existing, loadErr := s.accounts.FindBySubject(ctx, domain.ChannelC, account.Subject)
			if loadErr != nil {
				return nil, apperr.Internal("load account failed", loadErr)
			}
			account = existing
		} else {
			return nil, apperr.Internal("save account failed", err)
		}
	}

	identityID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate oauth identity id failed", err)
	}
	identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID:              identityID,
		AccountID:       account.ID,
		Provider:        provider,
		ProviderSubject: subject,
	})
	if err != nil {
		return nil, apperr.Internal("build oauth identity failed", err)
	}
	if err := s.identities.Save(ctx, identity); err != nil {
		// 残留 pending 账号可被后续登录复用或人工清理。
		return nil, apperr.Unavailable("oauth identity store unavailable")
	}
	return account, nil
}

// oauthAccountSubject 返回外部身份在身份域的登录主体：摘要化，避免第三方主体含 PII 或超长。
func oauthAccountSubject(provider, subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return "oauth:" + provider + ":" + hex.EncodeToString(sum[:])
}

// randomURLSafe 生成 n 字节随机数的 base64url 编码。
func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// oauthInvalid 返回授权失败统一 40100 文案。
func oauthInvalid() error {
	return apperr.Unauthorized("invalid oauth authorization")
}
