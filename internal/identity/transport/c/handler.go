// Package c 提供 C 端（消费者端）身份认证 HTTP Handler 与路由。
package c

import (
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
)

// HandlerConfig 是 C 端认证 Handler 的装配参数。
type HandlerConfig struct {
	// Rest 复用 A/B 端的刷新与登出 Handler。
	Rest *rest.Handler
	// SMS 短信登录服务；未启用时为 nil，登录端点返回 50300。
	SMS *app.SMSLoginService
	// OAuth OAuth 登录服务；未配置 provider 时为 nil。
	OAuth *app.OAuthService
	// SAML SAML 登录服务；未启用时为 nil，SAML 路由不注册。
	SAML *app.SAMLService
	// SAMLRedirectURL 是 ACS 成功后的前端登录页绝对地址；SAML 启用时必填。
	SAMLRedirectURL string
	// Auth 认证用例服务；用于自助注销等与短信无关的 C 端操作。
	Auth *app.AuthService
	// Providers 已配置的 provider，用于路由裁剪。
	Providers []string
	// ClientIP 可信代理解析器；nil 表示只使用 RemoteAddr。
	ClientIP *pkghttpx.ClientIPResolver
	// Hasher 是 IP / UA 摘要器；nil 时回退历史算法（仅限本地/tests）。
	Hasher *crypto.Hasher
}

// Handler 处理 C 端认证 HTTP 请求。
type Handler struct {
	rest            *rest.Handler
	sms             *app.SMSLoginService
	oauth           *app.OAuthService
	saml            *app.SAMLService
	samlRedirectURL string
	auth            *app.AuthService
	providers       []string
	ipResolver      *pkghttpx.ClientIPResolver
	hasher          *crypto.Hasher
}

// NewHandler 构造 C 端认证 Handler。
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Rest == nil {
		return nil, apperr.Internal("c auth handler requires rest handler", nil)
	}
	providers := make([]string, 0, len(cfg.Providers))
	for _, name := range cfg.Providers {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			providers = append(providers, trimmed)
		}
	}
	return &Handler{
		rest: cfg.Rest, sms: cfg.SMS, oauth: cfg.OAuth, auth: cfg.Auth,
		saml: cfg.SAML, samlRedirectURL: strings.TrimSpace(cfg.SAMLRedirectURL),
		providers: providers, ipResolver: cfg.ClientIP, hasher: cfg.Hasher,
	}, nil
}

// SendSMSCode POST /c/v1/auth/sms/send
//
// 发送 6 位短信验证码。响应与手机号是否存在无关（统一 204），防止枚举；
// 受重发间隔、单手机号日限与单 IP 小时限约束，超限返回 42900。
func (h *Handler) SendSMSCode(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	var req smsSendRequest
	if err := rest.DecodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.sms == nil {
		httpx.FromError(w, apperr.Unavailable("sms login is not enabled"), traceID)
		return
	}
	if err := h.sms.Send(r.Context(), app.SendSMSCodeInput{
		Phone:   req.Phone,
		IPHash:  h.clientIPHash(r),
		TraceID: traceID,
	}); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Login POST /c/v1/auth/login
//
// 短信验证码登录：验证码错误、过期与未注册手机号统一返回 40100，不自动建号；
// 成功后创建 C 端会话并签发访问 / 刷新令牌。
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	var req smsLoginRequest
	if err := rest.DecodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.sms == nil {
		httpx.FromError(w, apperr.Unavailable("sms login is not enabled"), traceID)
		return
	}
	result, err := h.sms.Login(r.Context(), app.SMSLoginInput{
		Phone:   req.Phone,
		Code:    req.Code,
		IPHash:  h.clientIPHash(r),
		UAHash:  h.clientUAHash(r),
		Device:  h.clientDevice(r),
		TraceID: traceID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, tokenResponseFrom(result.TokenPair), traceID)
}

// Register POST /c/v1/auth/sms/register
//
// 验证码通过后为未注册手机号开户并直接签发令牌；已注册与验证码错误统一 40100。
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	var req smsLoginRequest
	if err := rest.DecodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.sms == nil {
		httpx.FromError(w, apperr.Unavailable("sms registration is not enabled"), traceID)
		return
	}
	result, err := h.sms.Register(r.Context(), app.SMSLoginInput{
		Phone:   req.Phone,
		Code:    req.Code,
		IPHash:  h.clientIPHash(r),
		UAHash:  h.clientUAHash(r),
		Device:  h.clientDevice(r),
		TraceID: traceID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, tokenResponseFrom(result.TokenPair), traceID)
}

// RebindPhone POST /c/v1/auth/phone/rebind
//
// 校验新手机号验证码后换绑；成功后撤销全部会话，用户需用新手机号重新登录。
func (h *Handler) RebindPhone(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := cActorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req smsRebindRequest
	if err := rest.DecodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.sms == nil {
		httpx.FromError(w, apperr.Unavailable("phone rebind is not enabled"), traceID)
		return
	}
	if err := h.sms.Rebind(r.Context(), app.SMSRebindInput{
		Actor:   actor,
		Phone:   req.Phone,
		Code:    req.Code,
		OldCode: req.OldCode,
		IPHash:  h.clientIPHash(r),
	}); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteAccount DELETE /c/v1/auth/account
//
// 用户自助注销（软删）；手机号主体仍占用唯一键，不允许立即被他人重新注册。
// 短信启用时请求体需携带当前手机号验证码（{"code":"123456"}）作为再认证；
// 短信未启用或 OAuth-only 账号无手机号时要求最近一次登录在窗口内（8.4 R-8.4-04），
// 否则返回 40100，用户需重新登录后再发起注销。
func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := cActorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req smsDeleteRequest
	if err := rest.DecodeOptionalJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.sms != nil {
		if err := h.sms.DeleteAccount(r.Context(), actor, req.Code); err != nil {
			httpx.FromError(w, err, traceID)
			return
		}
	} else {
		if h.auth == nil {
			httpx.FromError(w, apperr.Unavailable("account deletion is not configured"), traceID)
			return
		}
		if err := h.auth.DeleteConsumerAccount(r.Context(), actor); err != nil {
			httpx.FromError(w, err, traceID)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// cActorFrom 从鉴权上下文构造 C 端用例入参。
func cActorFrom(r *http.Request) (app.SessionActor, error) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		return app.SessionActor{}, apperr.Unauthorized("missing authentication context")
	}
	return app.SessionActor{
		AccountID:      p.SubjectID,
		SessionID:      p.SessionID,
		TokenID:        p.TokenID,
		TokenExpiresAt: p.TokenExpiresAt,
		MerchantID:     p.MerchantID,
		TraceID:        httpx.TraceIDFromContext(r.Context()),
	}, nil
}

// providerConfigured 报告 provider 是否在装配期白名单内。
//
// 白名单为空表示 OAuth 能力整体关闭：此时直接返回 50300 说明能力未启用，
// 与「provider 未配置」的 40400 区分；路由注册同样以非空白名单为前提。
func (h *Handler) providerConfigured(name string) bool {
	if len(h.providers) == 0 {
		return true
	}
	for _, provider := range h.providers {
		if provider == name {
			return true
		}
	}
	return false
}

// OAuthAuthorize POST /c/v1/auth/oauth/{provider}/authorize
//
// 为指定 provider 生成一次性 state 与 PKCE verifier，返回第三方授权地址；
// provider 未配置时路由不存在（404），state 有效期 10 分钟。
func (h *Handler) OAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	provider := strings.TrimSpace(r.PathValue("provider"))
	if !h.providerConfigured(provider) {
		httpx.FromError(w, apperr.NotFound("oauth provider is not configured"), traceID)
		return
	}
	if h.oauth == nil {
		httpx.FromError(w, apperr.Unavailable("oauth login is not enabled"), traceID)
		return
	}
	result, err := h.oauth.AuthorizeWithClient(r.Context(), provider, h.clientIPHash(r), traceID)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, oauthAuthorizeResponse{
		AuthorizeURL: result.AuthorizeURL,
		State:        result.State,
	}, traceID)
}

// OAuthCallback POST /c/v1/auth/oauth/{provider}/callback
//
// 校验 state / PKCE 后换取外部身份并完成登录；state 无效、provider 不匹配、
// 授权码被拒与未映射身份统一 40100，IdP 故障返回 50300。
func (h *Handler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	provider := strings.TrimSpace(r.PathValue("provider"))
	if !h.providerConfigured(provider) {
		httpx.FromError(w, apperr.NotFound("oauth provider is not configured"), traceID)
		return
	}
	var req oauthCallbackRequest
	if err := rest.DecodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.oauth == nil {
		httpx.FromError(w, apperr.Unavailable("oauth login is not enabled"), traceID)
		return
	}
	result, err := h.oauth.Callback(r.Context(), app.OAuthCallbackInput{
		Provider: provider,
		Code:     req.Code,
		State:    req.State,
		IPHash:   h.clientIPHash(r),
		UAHash:   h.clientUAHash(r),
		Device:   h.clientDevice(r),
		TraceID:  traceID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, tokenResponseFrom(result.TokenPair), traceID)
}

// ListBindings GET /c/v1/me/bindings
//
// 返回当前账号已绑定的外部身份；不返回 provider_subject 原文。
func (h *Handler) ListBindings(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := cActorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.oauth == nil {
		httpx.FromError(w, apperr.Unavailable("oauth binding is not enabled"), traceID)
		return
	}
	bindings, err := h.oauth.ListBindings(r.Context(), actor)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	out := make([]oauthBindingDTO, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, oauthBindingDTO{Provider: binding.Provider, BoundAt: binding.BoundAt.UTC()})
	}
	httpx.OK(w, out, traceID)
}

// Unbind DELETE /c/v1/me/bindings/{provider}
//
// 解绑指定外部身份；账号没有密码且仅剩一个绑定时返回 40900，避免账号失联。
func (h *Handler) Unbind(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := cActorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.oauth == nil {
		httpx.FromError(w, apperr.Unavailable("oauth binding is not enabled"), traceID)
		return
	}
	if err := h.oauth.Unbind(r.Context(), actor, r.PathValue("provider")); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// OAuthBindAuthorize POST /c/v1/auth/bindings/{provider}/authorize
//
// 已登录账号发起绑定；state 绑定当前账号，回调必须由同一账号完成。
func (h *Handler) OAuthBindAuthorize(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := cActorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	provider := strings.TrimSpace(r.PathValue("provider"))
	if !h.providerConfigured(provider) {
		httpx.FromError(w, apperr.NotFound("oauth provider is not configured"), traceID)
		return
	}
	if h.oauth == nil {
		httpx.FromError(w, apperr.Unavailable("oauth binding is not enabled"), traceID)
		return
	}
	result, err := h.oauth.AuthorizeBinding(r.Context(), actor, provider, h.clientIPHash(r), traceID)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, oauthAuthorizeResponse{AuthorizeURL: result.AuthorizeURL, State: result.State}, traceID)
}

// OAuthBindCallback POST /c/v1/auth/bindings/{provider}/callback
//
// 完成绑定；已绑定本账号或他人账号时返回 40900，其他失败统一 40100 / 50300。
func (h *Handler) OAuthBindCallback(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := cActorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req oauthCallbackRequest
	if err := rest.DecodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	provider := strings.TrimSpace(r.PathValue("provider"))
	if !h.providerConfigured(provider) {
		httpx.FromError(w, apperr.NotFound("oauth provider is not configured"), traceID)
		return
	}
	if h.oauth == nil {
		httpx.FromError(w, apperr.Unavailable("oauth binding is not enabled"), traceID)
		return
	}
	if err := h.oauth.CompleteBinding(r.Context(), actor, app.OAuthCallbackInput{
		Provider: provider,
		Code:     req.Code,
		State:    req.State,
		IPHash:   h.clientIPHash(r),
		UAHash:   h.clientUAHash(r),
		Device:   h.clientDevice(r),
		TraceID:  traceID,
	}); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// oauthBindingDTO 是已绑定外部身份的展示结构。
type oauthBindingDTO struct {
	Provider string    `json:"provider"`
	BoundAt  time.Time `json:"bound_at"`
}

// smsSendRequest 是发送验证码请求。
type smsSendRequest struct {
	Phone string `json:"phone"`
}

// smsLoginRequest 是短信验证码登录请求。
type smsLoginRequest struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

// smsRebindRequest 是手机号换绑请求。
//
// old_code 用于证明持有当前手机号；新号验证码只证明持有新号。
type smsRebindRequest struct {
	Phone   string `json:"phone"`
	Code    string `json:"code"`
	OldCode string `json:"old_code,omitempty"`
}

// smsDeleteRequest 是注销账号请求；短信启用时必须携带当前手机号验证码。
type smsDeleteRequest struct {
	Code string `json:"code,omitempty"`
}

// oauthCallbackRequest 是 OAuth 回调请求。
type oauthCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// oauthAuthorizeResponse 是授权发起响应。
type oauthAuthorizeResponse struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
}

// tokenResponse 与 A / B 端字段完全一致。
type tokenResponse struct {
	AccessToken       string     `json:"access_token"`
	RefreshToken      string     `json:"refresh_token"`
	TokenType         string     `json:"token_type"`
	ExpiresIn         int64      `json:"expires_in"`
	AbsoluteExpiresAt *time.Time `json:"absolute_expires_at,omitempty"`
}

// tokenResponseFrom 把令牌对转换为响应 DTO。
func tokenResponseFrom(pair app.TokenPair) tokenResponse {
	var absolute *time.Time
	if !pair.AbsoluteExpiresAt.IsZero() {
		value := pair.AbsoluteExpiresAt
		absolute = &value
	}
	return tokenResponse{
		AccessToken:       pair.AccessToken,
		RefreshToken:      pair.RefreshToken,
		TokenType:         "Bearer",
		ExpiresIn:         pair.ExpiresIn,
		AbsoluteExpiresAt: absolute,
	}
}

// 请求体解码统一复用 transport/rest 的严格实现（SEC-10）。

// clientIPHash 返回客户端 IP 摘要；配置 hash_key 时使用带密钥 HMAC。
func (h *Handler) clientIPHash(r *http.Request) string {
	return app.ClientRef(h.hasher, app.HashDomainClientIP, h.clientIP(r))
}

// clientUAHash 返回 User-Agent 摘要；与 IP 使用不同域。
func (h *Handler) clientUAHash(r *http.Request) string {
	return app.ClientRef(h.hasher, app.HashDomainClientUA, r.UserAgent())
}

// clientDevice 从原始 UA / IP 派生可展示设备画像；原文不落库。
func (h *Handler) clientDevice(r *http.Request) domain.DeviceInfo {
	return domain.ParseDeviceInfo(r.UserAgent(), h.clientIP(r))
}

// clientIP 使用可信代理解析器获取客户端 IP；nil 解析器只使用 RemoteAddr。
func (h *Handler) clientIP(r *http.Request) string {
	if h == nil {
		return (*pkghttpx.ClientIPResolver)(nil).ClientIP(r)
	}
	return h.clientIPResolver().ClientIP(r)
}

// clientIPResolver 返回解析器；nil 接收者等价于不信任任何代理头。
func (h *Handler) clientIPResolver() *pkghttpx.ClientIPResolver {
	if h == nil {
		return nil
	}
	return h.ipResolver
}
