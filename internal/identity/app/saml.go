// Package app 的 SAML 2.0 SP-initiated SSO 用例层。
//
// 仅支持 SP-initiated + HTTP-POST ACS；断言必须由 IdP 签名，加密断言由 SP 私钥解密。
// 协议细节全部收敛在 infrastructure/saml，本层只依赖 SAMLProvider 端口。
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/crypto"
)

const (
	// samlProviderPrefix 区分同名的 OAuth / SAML 提供方，避免映射表冲突。
	samlProviderPrefix = "saml:"
	// maxSAMLRelayStateBytes 限制 RelayState，避免把超长 state 写入存储与审计。
	maxSAMLRelayStateBytes = 1024
	// defaultSAMLRequestTTL AuthnRequest / state 的默认有效期。
	defaultSAMLRequestTTL = 10 * time.Minute
	// defaultSAMLReplayTTL 断言 ID 去重窗口，必须覆盖断言有效期 + 时钟偏差。
	defaultSAMLReplayTTL = 10 * time.Minute
	// defaultSAMLTicketTTL 一次性兑换票据有效期。
	defaultSAMLTicketTTL = time.Minute
)

// SAMLIdentity 是已验签、已解密的断言主体信息。
type SAMLIdentity struct {
	Subject     string
	AssertionID string
	Issuer      string
}

// SAMLProvider 是 IdP 适配端口，由 infrastructure/saml 实现。
type SAMLProvider interface {
	// Name 返回提供方标识，与配置和路由一致。
	Name() string
	// AuthorizationURL 生成 HTTP-Redirect 形式的 AuthnRequest 地址。
	AuthorizationURL(requestID, relayState string) (string, error)
	// ParseResponse 校验签名、条件与加密断言，返回断言主体。
	// 非法断言返回 40100，IdP 元数据不可用返回 50300。
	ParseResponse(ctx context.Context, samlResponse, expectedRequestID string) (SAMLIdentity, error)
}

// SAMLReplayStore 是断言 ID 一次性去重存储。
type SAMLReplayStore interface {
	// Reserve 原子占用断言 ID；已存在返回 apperr.ErrConflict。
	Reserve(ctx context.Context, provider, assertionHash string, ttl time.Duration) error
}

// SAMLTicket 是 ACS 成功后交给前端的一次性兑换凭证载荷；不包含令牌。
type SAMLTicket struct {
	AccountID string
	Provider  string
	IPHash    string
	UAHash    string
	Device    domain.DeviceInfo
	TraceID   string
}

// SAMLTicketStore 是一次性 ticket 存储。
type SAMLTicketStore interface {
	Put(ctx context.Context, ticket string, value SAMLTicket, ttl time.Duration) error
	// Consume 一次性消费；不存在或已消费返回 apperr.NotFound。
	Consume(ctx context.Context, ticket string) (SAMLTicket, error)
}

// SAMLMetrics 是 SAML 登录的低基数指标出口；nil 表示不采集。
type SAMLMetrics interface {
	ObserveSAMLAuthorize(ctx context.Context, provider, result, reason string)
	ObserveSAMLACS(ctx context.Context, provider, result, reason string, duration time.Duration)
}

// SAMLServiceConfig 是 SAML 用例依赖。
type SAMLServiceConfig struct {
	Providers  map[string]SAMLProvider
	States     OAuthStateStore
	Identities domain.OAuthIdentityRepository
	Accounts   domain.AccountRepository
	IDs        IDGenerator
	Auth       *AuthService
	Hasher     *crypto.Hasher
	// AutoProvision 未映射主体是否自动建号；默认 false（失败关闭）。
	AutoProvision bool
	// StateTTL / ReplayTTL / TicketTTL 为 0 时使用默认值。
	StateTTL  time.Duration
	ReplayTTL time.Duration
	TicketTTL time.Duration
	Replay    SAMLReplayStore
	Tickets   SAMLTicketStore
	Metrics   SAMLMetrics
	Limiter   LoginLimiter
	// SPMetadataXML 是所有 IdP 共用的 SP 元数据（IdP 侧登记用）。
	SPMetadataXML []byte
}

// SAMLService 编排授权发起、ACS 校验与一次性票据兑换。
type SAMLService struct {
	providers     map[string]SAMLProvider
	states        OAuthStateStore
	identities    domain.OAuthIdentityRepository
	accounts      domain.AccountRepository
	ids           IDGenerator
	auth          *AuthService
	hasher        *crypto.Hasher
	autoProvision bool
	stateTTL      time.Duration
	replayTTL     time.Duration
	ticketTTL     time.Duration
	replay        SAMLReplayStore
	tickets       SAMLTicketStore
	metrics       SAMLMetrics
	limiter       LoginLimiter
	spMetadata    []byte
}

// NewSAMLService 构造 SAML 登录服务；依赖缺失时返回错误。
func NewSAMLService(cfg SAMLServiceConfig) (*SAMLService, error) {
	if cfg.States == nil || cfg.Identities == nil || cfg.Accounts == nil ||
		cfg.IDs == nil || cfg.Auth == nil || cfg.Replay == nil || cfg.Tickets == nil {
		return nil, apperr.Internal("saml service dependencies are required", nil)
	}
	providers := make(map[string]SAMLProvider, len(cfg.Providers))
	for name, provider := range cfg.Providers {
		if provider == nil {
			return nil, apperr.Internal("saml provider is nil", nil)
		}
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || trimmed != provider.Name() {
			return nil, apperr.Internal("saml provider name mismatch", nil)
		}
		providers[trimmed] = provider
	}
	if len(providers) == 0 {
		return nil, apperr.Internal("saml service requires at least one provider", nil)
	}
	stateTTL := cfg.StateTTL
	if stateTTL <= 0 {
		stateTTL = defaultSAMLRequestTTL
	}
	replayTTL := cfg.ReplayTTL
	if replayTTL <= 0 {
		replayTTL = defaultSAMLReplayTTL
	}
	ticketTTL := cfg.TicketTTL
	if ticketTTL <= 0 {
		ticketTTL = defaultSAMLTicketTTL
	}
	return &SAMLService{
		providers:     providers,
		states:        cfg.States,
		identities:    cfg.Identities,
		accounts:      cfg.Accounts,
		ids:           cfg.IDs,
		auth:          cfg.Auth,
		hasher:        cfg.Hasher,
		autoProvision: cfg.AutoProvision,
		stateTTL:      stateTTL,
		replayTTL:     replayTTL,
		ticketTTL:     ticketTTL,
		replay:        cfg.Replay,
		tickets:       cfg.Tickets,
		metrics:       cfg.Metrics,
		limiter:       cfg.Limiter,
		spMetadata:    cfg.SPMetadataXML,
	}, nil
}

// ProviderNames 返回已配置的 SAML provider 名称（升序）。
func (s *SAMLService) ProviderNames() []string {
	out := make([]string, 0, len(s.providers))
	for name := range s.providers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// HasProvider 报告 provider 是否已配置，供路由裁剪使用。
func (s *SAMLService) HasProvider(name string) bool {
	_, ok := s.providers[strings.TrimSpace(name)]
	return ok
}

// SPMetadata 返回 SP 元数据 XML；用于在 IdP 侧登记本系统。
func (s *SAMLService) SPMetadata() ([]byte, error) {
	if len(s.spMetadata) == 0 {
		return nil, apperr.NotFound("saml sp metadata is not configured")
	}
	return s.spMetadata, nil
}

// AuthorizeWithClient 限流后生成 AuthnRequest 并保存一次性 state。
func (s *SAMLService) AuthorizeWithClient(ctx context.Context, provider, ipHash, traceID string) (result AuthorizeResult, err error) {
	started := time.Now()
	name := strings.TrimSpace(provider)
	defer func() {
		s.observeAuthorize(ctx, name, err, time.Since(started))
	}()
	if limitErr := s.checkRateLimit("authorize", ipHash); limitErr != nil {
		return AuthorizeResult{}, limitErr
	}
	providerImpl, ok := s.providers[name]
	if !ok {
		return AuthorizeResult{}, apperr.NotFound("saml provider is not configured")
	}
	state, err := randomURLSafe(oauthSecretBytes)
	if err != nil {
		return AuthorizeResult{}, apperr.Unavailable("saml state generation failed")
	}
	// xs:ID 必须以字母或下划线开头；base64url 可能以数字开头，因此加固定前缀。
	requestID, err := randomURLSafe(oauthSecretBytes)
	if err != nil {
		return AuthorizeResult{}, apperr.Unavailable("saml state generation failed")
	}
	requestID = "_" + requestID
	stateValue := OAuthState{Provider: name, RequestID: requestID}
	authorizeURL, err := providerImpl.AuthorizationURL(requestID, state)
	if err != nil {
		return AuthorizeResult{}, apperr.Internal("build saml authorize url failed", err)
	}
	if err = s.states.Save(ctx, state, stateValue, s.stateTTL); err != nil {
		return AuthorizeResult{}, apperr.Unavailable("saml state store unavailable")
	}
	// 授权发起属于外部身份入口事件；审计失败时直接拒绝（state 由 TTL 自然过期）。
	if auditErr := s.auth.emitAuditAs(ctx, "anonymous", audit.ActionAuthSAMLAuthorize, "anonymous", "",
		"saml_provider", name, audit.OutcomeSuccess, "state_issued", traceID, nil); auditErr != nil {
		return AuthorizeResult{}, auditErr
	}
	return AuthorizeResult{AuthorizeURL: authorizeURL, State: state}, nil
}

// SAMLACSInput 是 ACS 表单入参。
type SAMLACSInput struct {
	Provider     string
	SAMLResponse string
	RelayState   string
	IPHash       string
	UAHash       string
	Device       domain.DeviceInfo
	TraceID      string
}

// ACS 校验断言并签发一次性 ticket；失败关闭，不返回任何令牌。
func (s *SAMLService) ACS(ctx context.Context, in SAMLACSInput) (ticket string, err error) {
	started := time.Now()
	name := strings.TrimSpace(in.Provider)
	defer func() {
		s.observeACS(ctx, name, err, time.Since(started))
	}()
	if limitErr := s.checkRateLimit("acs", in.IPHash); limitErr != nil {
		return "", limitErr
	}
	state := strings.TrimSpace(in.RelayState)
	if strings.TrimSpace(in.SAMLResponse) == "" || state == "" {
		return "", apperr.BadRequest("saml response and relay state are required")
	}
	if len(state) > maxSAMLRelayStateBytes {
		return "", samlInvalid()
	}
	saved, err := s.states.Consume(ctx, state)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", samlInvalid()
		}
		return "", apperr.Unavailable("saml state store unavailable")
	}
	if stateProvider := strings.TrimSpace(saved.Provider); stateProvider != "" {
		if name == "" {
			name = stateProvider
		} else if name != stateProvider {
			return "", samlInvalid()
		}
	}
	providerImpl, ok := s.providers[name]
	if !ok {
		return "", apperr.NotFound("saml provider is not configured")
	}
	identity, err := providerImpl.ParseResponse(ctx, in.SAMLResponse, saved.RequestID)
	if err != nil {
		return "", err
	}
	subject := strings.TrimSpace(identity.Subject)
	assertionID := strings.TrimSpace(identity.AssertionID)
	if subject == "" || assertionID == "" {
		return "", samlInvalid()
	}
	// 断言 ID 去重：同一断言在有效期内只允许兑换一次（防重放）。
	if err = s.replay.Reserve(ctx, name, hashAssertionID(assertionID), s.replayTTL); err != nil {
		if errors.Is(err, apperr.ErrConflict) {
			return "", samlInvalid()
		}
		return "", apperr.Unavailable("saml replay store unavailable")
	}
	account, err := s.resolver().resolve(ctx, samlProviderRef(name), subject, in.TraceID)
	if err != nil {
		return "", err
	}
	rawTicket, err := randomURLSafe(oauthSecretBytes)
	if err != nil {
		return "", apperr.Unavailable("saml ticket generation failed")
	}
	payload := SAMLTicket{
		AccountID: account.ID,
		Provider:  name,
		IPHash:    in.IPHash,
		UAHash:    in.UAHash,
		Device:    in.Device,
		TraceID:   in.TraceID,
	}
	if err = s.tickets.Put(ctx, rawTicket, payload, s.ticketTTL); err != nil {
		return "", apperr.Unavailable("saml ticket store unavailable")
	}
	// ACS 审计失败不阻断业务结果（与 OAuth 回调一致），由指标与日志兜底。
	_ = s.auth.emitAuditAs(ctx, "anonymous", audit.ActionAuthSAMLCallback, "anonymous", "",
		"saml_provider", name, audit.OutcomeSuccess, "assertion_verified", in.TraceID, nil)
	return rawTicket, nil
}

// Exchange 消费一次性 ticket 并完成登录。
func (s *SAMLService) Exchange(ctx context.Context, ticket string, device domain.DeviceInfo) (LoginResult, error) {
	trimmed := strings.TrimSpace(ticket)
	if trimmed == "" {
		return LoginResult{}, samlInvalid()
	}
	payload, err := s.tickets.Consume(ctx, trimmed)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return LoginResult{}, samlInvalid()
		}
		return LoginResult{}, apperr.Unavailable("saml ticket store unavailable")
	}
	if payload.AccountID == "" {
		return LoginResult{}, samlInvalid()
	}
	if device.IsEmpty() {
		device = payload.Device
	}
	return s.auth.CompleteVerifiedLogin(ctx, VerifiedLoginInput{
		AccountID: payload.AccountID,
		IPHash:    payload.IPHash,
		UAHash:    payload.UAHash,
		Device:    device,
		TraceID:   payload.TraceID,
	})
}

// resolver 复用 OAuth 的账号解析 / 自动开通逻辑。
func (s *SAMLService) resolver() externalAccountResolver {
	return externalAccountResolver{
		identities:    s.identities,
		accounts:      s.accounts,
		ids:           s.ids,
		auth:          s.auth,
		hasher:        s.hasher,
		autoProvision: s.autoProvision,
	}
}

// checkRateLimit 同 OAuth：全局限流 + 单 IP 限流，超限返回 42900。
func (s *SAMLService) checkRateLimit(action, ipHash string) error {
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
	return NewRateLimitedError("too many saml requests", retryAfter)
}

// observeAuthorize 记录授权发起指标；nil 指标安全。
func (s *SAMLService) observeAuthorize(ctx context.Context, provider string, err error, _ time.Duration) {
	if s.metrics == nil {
		return
	}
	if err != nil {
		s.metrics.ObserveSAMLAuthorize(ctx, provider, MetricResultFailure, samlMetricReason(err))
		return
	}
	s.metrics.ObserveSAMLAuthorize(ctx, provider, MetricResultSuccess, MetricReasonOK)
}

// observeACS 记录 ACS 指标；nil 指标安全。
func (s *SAMLService) observeACS(ctx context.Context, provider string, err error, duration time.Duration) {
	if s.metrics == nil {
		return
	}
	if err != nil {
		s.metrics.ObserveSAMLACS(ctx, provider, MetricResultFailure, samlMetricReason(err), duration)
		return
	}
	s.metrics.ObserveSAMLACS(ctx, provider, MetricResultSuccess, MetricReasonOK, duration)
}

// samlMetricReason 复用 OAuth 的错误分类，保证 reason 词表一致。
func samlMetricReason(err error) string {
	return oauthMetricReason(err)
}

// samlProviderRef 返回映射表使用的 provider 键（saml:<name>）。
func samlProviderRef(name string) string {
	return samlProviderPrefix + name
}

// hashAssertionID 对断言 ID 做 SHA-256，避免把原始值写入存储键。
func hashAssertionID(assertionID string) string {
	sum := sha256.Sum256([]byte(assertionID))
	return hex.EncodeToString(sum[:])
}

// samlInvalid 返回断言校验失败统一 40100 文案。
func samlInvalid() error {
	return apperr.Unauthorized("invalid saml assertion")
}
