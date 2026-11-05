package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	"github.com/admin-frame/admin-frame/pkg/crypto"
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

// OIDCProvider 是 OIDC 提供方的可选扩展。
//
// 未实现该接口的 provider 继续走纯 OAuth2 userinfo 路径；
// 实现后服务会在授权发起时生成 nonce 并持久化到 state，回调时回传校验，
// 从而把 id_token 的 nonce 绑定到本次浏览器会话（防重放 / 防跨会话注入）。
type OIDCProvider interface {
	OAuthProvider
	// OIDCEnabled 报告该 provider 是否已配置 issuer/JWKS；false 时按纯 OAuth2 处理。
	OIDCEnabled() bool
	// AuthorizationURLWithNonce 在授权地址中加入 nonce 与 openid scope。
	AuthorizationURLWithNonce(state, codeVerifier, nonce string) (string, error)
	// ExchangeWithNonce 换取并校验 id_token；nonce 不匹配或签名/issuer/audience 非法返回 40100。
	ExchangeWithNonce(ctx context.Context, code, codeVerifier, nonce string) (OAuthIdentityInfo, error)
}

// OAuthState 是授权发起时保存的一次性上下文。
type OAuthState struct {
	Provider string
	Verifier string
	// Nonce 是 OIDC id_token 的一次性绑定值；纯 OAuth2 流程为空。
	Nonce string
	// AccountID 非空表示绑定流程：回调只能落到该账号（BIZ-02）。
	AccountID string
}

// OAuthBindingView 是账号已绑定的外部身份视图；不含外部主体原文。
type OAuthBindingView struct {
	Provider string
	BoundAt  time.Time
}

// OAuthStateStore 是 state 的一次性消费存储。
type OAuthStateStore interface {
	Save(ctx context.Context, state string, value OAuthState, ttl time.Duration) error
	// Consume 一次性消费；不存在或已消费返回 apperr.NotFound。
	Consume(ctx context.Context, state string) (OAuthState, error)
}

// OAuthServiceConfig 是 OAuth 登录用例的装配参数。
type OAuthServiceConfig struct {
	Providers  map[string]OAuthProvider
	States     OAuthStateStore
	Identities domain.OAuthIdentityRepository
	Accounts   domain.AccountRepository
	IDs        IDGenerator
	Auth       *AuthService
	// Hasher 是第三方主体摘要器；nil 时回退历史算法（仅限本地/tests）。
	Hasher        *crypto.Hasher
	AutoProvision bool
	StateTTL      time.Duration
	// Metrics 是核心指标端口；可为 nil。
	Metrics Metrics
	// Limiter 是 authorize / callback 的 IP 级与全局限流器；nil 时仅保留 provider 超时（IDN-P2-05）。
	Limiter LoginLimiter
	// BreakerFailures 是 provider 连续失败阈值；≤0 使用 5（PERF-06）。
	BreakerFailures int
	// BreakerTimeout 是熔断打开时长；≤0 使用 60s。
	BreakerTimeout time.Duration
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
	// Device 可展示设备画像；由 transport 从原始 UA / IP 派生。
	Device domain.DeviceInfo
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
	hasher        *crypto.Hasher
	metrics       Metrics
	limiter       LoginLimiter
	breakers      map[string]*circuitbreaker.Breaker
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
	breakers := make(map[string]*circuitbreaker.Breaker, len(cfg.Providers))
	failures := cfg.BreakerFailures
	if failures <= 0 {
		failures = 5
	}
	breakerTimeout := cfg.BreakerTimeout
	if breakerTimeout <= 0 {
		breakerTimeout = time.Minute
	}
	for name, provider := range cfg.Providers {
		if provider == nil {
			return nil, apperr.Internal("oauth provider is nil", nil)
		}
		providers[name] = provider
		breaker, err := circuitbreaker.New(circuitbreaker.Config{
			Name:    "oauth:" + name,
			Timeout: breakerTimeout,
			ReadyToTrip: func(counts circuitbreaker.Counts) bool {
				return int64(counts.ConsecutiveFailures) >= int64(failures)
			},
			// 4xx / invalid_grant 是业务拒绝，不应打开 provider 熔断。
			IsSuccessful: func(err error) bool {
				return err == nil || errors.Is(err, apperr.ErrUnauthorized)
			},
		})
		if err != nil {
			return nil, apperr.Internal("build oauth breaker", err)
		}
		breakers[name] = breaker
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
		hasher:        cfg.Hasher,
		metrics:       cfg.Metrics,
		limiter:       cfg.Limiter,
		breakers:      breakers,
	}, nil
}

// oauthAuditReason 把 OAuth 错误映射为低基数审计原因；不含外部主体或令牌。
func oauthAuditReason(err error) string {
	switch {
	case err == nil:
		return "oauth_callback"
	case errors.Is(err, apperr.ErrNotFound):
		return "oauth_not_configured"
	case errors.Is(err, apperr.ErrUnauthorized):
		return "oauth_invalid"
	case errors.Is(err, apperr.ErrUnavailable):
		return "oauth_unavailable"
	}
	var limited *RateLimitedError
	if errors.As(err, &limited) {
		return "rate_limited"
	}
	return "oauth_failed"
}

// oauthMetricReason 把回调错误映射为低基数指标 reason；与审计原因保持一致。
func oauthMetricReason(err error) string {
	switch {
	case err == nil:
		return MetricReasonOK
	case errors.Is(err, apperr.ErrNotFound):
		return MetricReasonOAuthNotConfigured
	case errors.Is(err, apperr.ErrUnauthorized):
		return MetricReasonOAuthInvalid
	case errors.Is(err, apperr.ErrUnavailable):
		return MetricReasonUnavailable
	}
	var limited *RateLimitedError
	if errors.As(err, &limited) {
		return MetricReasonRateLimited
	}
	return MetricReasonInternal
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
//
// 无客户端摘要时仅受全局限流约束；HTTP 入口应调用 AuthorizeWithClient。
func (s *OAuthService) Authorize(ctx context.Context, provider string) (AuthorizeResult, error) {
	return s.AuthorizeWithClient(ctx, provider, "", "")
}

// AuthorizeWithClient 在 IP 级与全局限流通过后生成 state（IDN-P2-05）。
//
// 每次请求写一个 Redis state 键；限流用于封顶单 IP 与全局键增长速度。
func (s *OAuthService) AuthorizeWithClient(ctx context.Context, provider, ipHash, traceID string) (AuthorizeResult, error) {
	if err := s.checkRateLimit("authorize", ipHash); err != nil {
		return AuthorizeResult{}, err
	}
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
	stateValue := OAuthState{Provider: name, Verifier: verifier}
	authorizeURL, err := s.authorizationURL(providerImpl, state, verifier, &stateValue)
	if err != nil {
		return AuthorizeResult{}, err
	}
	if err := s.states.Save(ctx, state, stateValue, s.stateTTL); err != nil {
		return AuthorizeResult{}, apperr.Unavailable("oauth state store unavailable")
	}
	// 授权发起属于外部身份入口事件；审计失败时直接拒绝（state 由 TTL 自然过期）。
	if auditErr := s.auth.emitAuditAs(ctx, "anonymous", audit.ActionAuthOAuthAuthorize, "anonymous", "",
		"oauth_provider", name, audit.OutcomeSuccess, "state_issued", traceID, nil); auditErr != nil {
		return AuthorizeResult{}, auditErr
	}
	return AuthorizeResult{AuthorizeURL: authorizeURL, State: state}, nil
}

// authorizationURL 按 provider 能力构造授权地址；OIDC 时生成 nonce 并写入 stateValue。
func (s *OAuthService) authorizationURL(
	provider OAuthProvider,
	state, verifier string,
	stateValue *OAuthState,
) (string, error) {
	if oidc, ok := provider.(OIDCProvider); ok && oidc.OIDCEnabled() {
		nonce, err := randomURLSafe(oauthSecretBytes)
		if err != nil {
			return "", apperr.Unavailable("oauth state generation failed")
		}
		stateValue.Nonce = nonce
		url, err := oidc.AuthorizationURLWithNonce(state, verifier, nonce)
		if err != nil {
			return "", apperr.Internal("build oauth authorize url failed", err)
		}
		return url, nil
	}
	url, err := provider.AuthorizationURL(state, verifier)
	if err != nil {
		return "", apperr.Internal("build oauth authorize url failed", err)
	}
	return url, nil
}

// Callback 校验 state 与授权码，完成身份映射与登录。
func (s *OAuthService) Callback(ctx context.Context, in OAuthCallbackInput) (result LoginResult, err error) {
	started := time.Now()
	providerName := strings.TrimSpace(in.Provider)
	defer func() {
		outcome := audit.OutcomeSuccess
		reason := "oauth_callback"
		if err != nil {
			outcome = audit.OutcomeFailure
			reason = oauthAuditReason(err)
		}
		resourceID := providerName
		if resourceID == "" {
			resourceID = "unknown"
		}
		// 回调失败同样记录，便于排查 IdP 异常与 state 重放；审计失败不覆盖业务结果。
		_ = s.auth.emitAuditAs(ctx, "anonymous", audit.ActionAuthOAuthCallback, "anonymous", "",
			"oauth_provider", resourceID, outcome, reason, in.TraceID, nil)
	}()
	deferred := func() {
		if s.metrics == nil {
			return
		}
		status := MetricResultSuccess
		if err != nil {
			status = MetricResultFailure
		}
		providerLabel := strings.TrimSpace(in.Provider)
		s.metrics.ObserveOAuthCallback(ctx, providerLabel, status, oauthMetricReason(err))
		s.metrics.ObserveOAuthCallbackDuration(ctx, providerLabel, status, time.Since(started))
	}
	defer deferred()
	if err := s.checkRateLimit("callback", in.IPHash); err != nil {
		return LoginResult{}, err
	}
	name := strings.TrimSpace(in.Provider)
	code := strings.TrimSpace(in.Code)
	state := strings.TrimSpace(in.State)
	if code == "" || state == "" {
		return LoginResult{}, oauthInvalid()
	}
	// SEC-03：回调参数在换取令牌前限制长度，避免超长输入占用网络与内存。
	if len(code) > maxOAuthCodeBytes || len(state) > maxOAuthStateBytes {
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

	info, err := s.exchangeCode(ctx, name, provider, code, saved.Verifier, saved.Nonce)
	if err != nil {
		return LoginResult{}, err
	}
	subject := strings.TrimSpace(info.Subject)
	if subject == "" {
		return LoginResult{}, oauthInvalid()
	}

	account, err := s.resolveAccount(ctx, name, subject, in.TraceID)
	if err != nil {
		return LoginResult{}, err
	}
	return s.auth.CompleteVerifiedLogin(ctx, VerifiedLoginInput{
		AccountID: account.ID,
		IPHash:    in.IPHash,
		UAHash:    in.UAHash,
		Device:    in.Device,
		TraceID:   in.TraceID,
	})
}

// exchangeCode 经 provider 级熔断换取外部主体；4xx / invalid_grant 统一 40100。
func (s *OAuthService) exchangeCode(
	ctx context.Context,
	name string,
	provider OAuthProvider,
	code, verifier, nonce string,
) (OAuthIdentityInfo, error) {
	var info OAuthIdentityInfo
	exchange := func() error {
		var exchangeErr error
		if nonce != "" {
			oidc, ok := provider.(OIDCProvider)
			if !ok || !oidc.OIDCEnabled() {
				// state 带 nonce 却无 OIDC 能力，说明状态被篡改或配置漂移，按无效授权拒绝。
				return oauthInvalid()
			}
			info, exchangeErr = oidc.ExchangeWithNonce(ctx, code, verifier, nonce)
			return exchangeErr
		}
		info, exchangeErr = provider.Exchange(ctx, code, verifier)
		return exchangeErr
	}
	var err error
	if breaker := s.breakers[name]; breaker != nil {
		if breakerErr := breaker.Do(exchange); breakerErr != nil {
			if errors.Is(breakerErr, circuitbreaker.ErrOpen) {
				return OAuthIdentityInfo{}, apperr.Unavailable("oauth provider temporarily unavailable")
			}
			err = breakerErr
		}
	} else {
		err = exchange()
	}
	if err != nil {
		switch {
		case errors.Is(err, apperr.ErrUnauthorized):
			return OAuthIdentityInfo{}, oauthInvalid()
		case errors.Is(err, apperr.ErrUnavailable):
			return OAuthIdentityInfo{}, apperr.Unavailable("oauth provider unavailable")
		default:
			return OAuthIdentityInfo{}, apperr.Internal("exchange oauth code failed", err)
		}
	}
	return info, nil
}

// ListBindings 返回当前账号已绑定的外部身份（BIZ-02）。
func (s *OAuthService) ListBindings(ctx context.Context, actor SessionActor) ([]OAuthBindingView, error) {
	if strings.TrimSpace(actor.AccountID) == "" {
		return nil, invalidToken()
	}
	identities, err := s.identities.ListByAccount(ctx, actor.AccountID)
	if err != nil {
		return nil, apperr.Internal("list oauth bindings failed", err)
	}
	out := make([]OAuthBindingView, 0, len(identities))
	for _, identity := range identities {
		out = append(out, OAuthBindingView{Provider: identity.Provider, BoundAt: identity.CreatedAt})
	}
	return out, nil
}

// Unbind 解绑当前账号的指定 provider；最后一个可用凭据不允许解绑（BIZ-02）。
func (s *OAuthService) Unbind(ctx context.Context, actor SessionActor, provider string) error {
	if strings.TrimSpace(actor.AccountID) == "" {
		return invalidToken()
	}
	name := strings.TrimSpace(provider)
	if name == "" {
		return apperr.BadRequest("provider is required")
	}
	bindings, err := s.identities.ListByAccount(ctx, actor.AccountID)
	if err != nil {
		return apperr.Internal("list oauth bindings failed", err)
	}
	target := ""
	for _, binding := range bindings {
		if binding.Provider == name {
			target = binding.ID
		}
	}
	if target == "" {
		return apperr.NotFound("oauth binding not found")
	}
	credentials, credErr := s.auth.credentials.ListByAccount(ctx, actor.AccountID)
	if credErr != nil {
		return apperr.Internal("list credentials failed", credErr)
	}
	hasPassword := false
	for _, credential := range credentials {
		if credential.Type == domain.CredentialTypePassword {
			hasPassword = true
		}
	}
	if !hasPassword && len(bindings) <= 1 {
		return apperr.Conflict("cannot unbind the last credential")
	}
	if err := s.identities.Delete(ctx, actor.AccountID, name); err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return apperr.NotFound("oauth binding not found")
		}
		return apperr.Internal("delete oauth binding failed", err)
	}
	return s.auth.emitAudit(ctx, audit.ActionAuthOAuthCallback, actor.AccountID, actor.MerchantID,
		"oauth_provider", name, audit.OutcomeSuccess, "oauth_unbound", actor.TraceID, nil)
}

// AuthorizeBinding 为已登录账号发起 OAuth 绑定授权（BIZ-02）。
func (s *OAuthService) AuthorizeBinding(
	ctx context.Context,
	actor SessionActor,
	provider, ipHash, _ string,
) (AuthorizeResult, error) {
	if strings.TrimSpace(actor.AccountID) == "" {
		return AuthorizeResult{}, invalidToken()
	}
	if err := s.checkRateLimit("authorize", ipHash); err != nil {
		return AuthorizeResult{}, err
	}
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
	stateValue := OAuthState{
		Provider: name, Verifier: verifier, AccountID: actor.AccountID,
	}
	authorizeURL, err := s.authorizationURL(providerImpl, state, verifier, &stateValue)
	if err != nil {
		return AuthorizeResult{}, err
	}
	if err := s.states.Save(ctx, state, stateValue, s.stateTTL); err != nil {
		return AuthorizeResult{}, apperr.Unavailable("oauth state store unavailable")
	}
	return AuthorizeResult{AuthorizeURL: authorizeURL, State: state}, nil
}

// CompleteBinding 完成绑定回调：state 必须由同一账号发起，外部主体不得已绑定他人（BIZ-02）。
func (s *OAuthService) CompleteBinding(
	ctx context.Context,
	actor SessionActor,
	in OAuthCallbackInput,
) error {
	if strings.TrimSpace(actor.AccountID) == "" {
		return invalidToken()
	}
	if err := s.checkRateLimit("callback", in.IPHash); err != nil {
		return err
	}
	name := strings.TrimSpace(in.Provider)
	code := strings.TrimSpace(in.Code)
	state := strings.TrimSpace(in.State)
	if code == "" || state == "" {
		return oauthInvalid()
	}
	if len(code) > maxOAuthCodeBytes || len(state) > maxOAuthStateBytes {
		return oauthInvalid()
	}
	provider, ok := s.providers[name]
	if !ok {
		return oauthInvalid()
	}
	saved, err := s.states.Consume(ctx, state)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return oauthInvalid()
		}
		return apperr.Unavailable("oauth state store unavailable")
	}
	if saved.Provider != name || saved.Verifier == "" || saved.AccountID != actor.AccountID {
		return oauthInvalid()
	}
	info, err := s.exchangeCode(ctx, name, provider, code, saved.Verifier, saved.Nonce)
	if err != nil {
		return err
	}
	subject := strings.TrimSpace(info.Subject)
	if subject == "" {
		return oauthInvalid()
	}
	existing, err := s.identities.FindByProviderSubject(ctx, name, subject)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return apperr.Unavailable("oauth identity store unavailable")
	}
	if err == nil {
		if existing.AccountID == actor.AccountID {
			return apperr.Conflict("oauth identity is already bound to this account")
		}
		return apperr.Conflict("oauth identity is already bound to another account")
	}
	identityID, err := s.ids.NextString()
	if err != nil {
		return apperr.Internal("generate oauth identity id failed", err)
	}
	identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID:              identityID,
		AccountID:       actor.AccountID,
		Provider:        name,
		ProviderSubject: subject,
	})
	if err != nil {
		return apperr.Internal("build oauth identity failed", err)
	}
	if err := s.identities.Save(ctx, identity); err != nil {
		if errors.Is(err, apperr.ErrConflict) {
			return apperr.Conflict("oauth identity is already bound")
		}
		return apperr.Unavailable("oauth identity store unavailable")
	}
	return s.auth.emitAudit(ctx, audit.ActionAuthOAuthCallback, actor.AccountID, actor.MerchantID,
		"oauth_provider", name, audit.OutcomeSuccess, "oauth_bound", in.TraceID, nil)
}

// resolveAccount 返回外部身份对应账号；未映射时按 auto_provision 决定是否建号。
func (s *OAuthService) resolveAccount(ctx context.Context, provider, subject, traceID string) (*domain.Account, error) {
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
	return s.provisionAccount(ctx, provider, subject, traceID)
}

// provisionAccount 在同一事务内自动建号、写外部身份映射与 account.created 审计。
//
// 审计与账号写入必须原子：避免「账号已建、审计丢失」（IDN-P2-02）。
func (s *OAuthService) provisionAccount(ctx context.Context, provider, subject, traceID string) (*domain.Account, error) {
	var account *domain.Account
	if err := s.auth.runTx(ctx, func(txCtx context.Context) error {
		created := true
		accountID, err := s.ids.NextString()
		if err != nil {
			return apperr.Internal("generate account id failed", err)
		}
		provisioned, err := domain.NewAccount(domain.NewAccountParams{
			ID:      accountID,
			Channel: domain.ChannelC,
			Subject: OAuthSubjectRef(s.hasher, provider, subject),
		})
		if err != nil {
			return apperr.Internal("build account failed", err)
		}
		if err := s.accounts.Save(txCtx, provisioned, 0); err != nil {
			if errors.Is(err, apperr.ErrConflict) {
				// 并发建号：以已存在的账号为准，避免同一外部身份产生两个账号。
				existing, loadErr := s.accounts.FindBySubject(txCtx, domain.ChannelC, "", provisioned.Subject)
				if loadErr != nil {
					return apperr.Internal("load account failed", loadErr)
				}
				created = false
				account = existing
			} else {
				return apperr.Internal("save account failed", err)
			}
		} else {
			account = provisioned
		}

		identityID, err := s.ids.NextString()
		if err != nil {
			return apperr.Internal("generate oauth identity id failed", err)
		}
		identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
			ID:              identityID,
			AccountID:       account.ID,
			Provider:        provider,
			ProviderSubject: subject,
		})
		if err != nil {
			return apperr.Internal("build oauth identity failed", err)
		}
		if err := s.identities.Save(txCtx, identity); err != nil {
			if !errors.Is(err, apperr.ErrConflict) {
				return apperr.Unavailable("oauth identity store unavailable")
			}
			// REL-07：并发回调可能已写入映射。回查并复用已存在账号，避免 503 与孤儿账号。
			existing, findErr := s.identities.FindByProviderSubject(txCtx, provider, subject)
			if findErr != nil {
				return apperr.Unavailable("oauth identity store unavailable")
			}
			existingAccount, loadErr := s.accounts.FindByID(txCtx, existing.AccountID)
			if loadErr != nil {
				return apperr.Internal("load mapped account failed", loadErr)
			}
			if created && account != nil && account.ID != existingAccount.ID {
				// 本次新建但未建立映射：软删，避免留下可登录的孤儿账号。
				expectedVersion := account.Version
				if deleteErr := account.Delete(s.auth.clock.Now().UTC()); deleteErr != nil {
					return deleteErr
				}
				if saveErr := s.accounts.Save(txCtx, account, expectedVersion); saveErr != nil {
					return apperr.Internal("remove orphan account failed", saveErr)
				}
			}
			account = existingAccount
			return nil
		}
		if created {
			// 只记录 provider 名，不写外部主体原文。
			if err := s.auth.emitAuditAs(txCtx, "system", audit.ActionAuthAccountCreated,
				account.ID, account.MerchantID, "account", account.ID, audit.OutcomeSuccess, "",
				traceID, map[string]string{"provider": provider}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return account, nil
}

// checkRateLimit 同时检查全局限流与单 IP 限流；任一超限返回 42900。
//
// ipHash 为空时跳过单 IP 维度（仅全局限流），用于无客户端摘要的内部调用。
func (s *OAuthService) checkRateLimit(action, ipHash string) error {
	if s.limiter == nil {
		return nil
	}
	keys := []string{action + ":global"}
	if ip := strings.TrimSpace(ipHash); ip != "" {
		keys = append(keys, action+":ip:"+ip)
	}
	denied := false
	var retryAfter time.Duration
	for _, key := range keys {
		allowed, after := s.limiter.Allow(key)
		if allowed {
			continue
		}
		denied = true
		if after > retryAfter {
			retryAfter = after
		}
	}
	if !denied {
		return nil
	}
	return NewRateLimitedError("too many oauth requests", retryAfter)
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
