// Package c 提供 C 端（消费者端）身份认证 HTTP Handler 与路由。
package c

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// HandlerConfig 是 C 端认证 Handler 的装配参数。
type HandlerConfig struct {
	// Rest 复用 A/B 端的刷新与登出 Handler。
	Rest *rest.Handler
	// SMS 短信登录服务；未启用时为 nil，登录端点返回 50300。
	SMS *app.SMSLoginService
	// OAuth OAuth 登录服务；未配置 provider 时为 nil。
	OAuth *app.OAuthService
	// Auth 认证用例服务；用于自助注销等与短信无关的 C 端操作。
	Auth *app.AuthService
	// Providers 已配置的 provider，用于路由裁剪。
	Providers []string
}

// Handler 处理 C 端认证 HTTP 请求。
type Handler struct {
	rest      *rest.Handler
	sms       *app.SMSLoginService
	oauth     *app.OAuthService
	auth      *app.AuthService
	providers []string
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
	return &Handler{rest: cfg.Rest, sms: cfg.SMS, oauth: cfg.OAuth, auth: cfg.Auth, providers: providers}, nil
}

// SendSMSCode POST /c/v1/auth/sms/send
//
// 发送 6 位短信验证码。响应与手机号是否存在无关（统一 204），防止枚举；
// 受重发间隔、单手机号日限与单 IP 小时限约束，超限返回 42900。
func (h *Handler) SendSMSCode(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	var req smsSendRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.sms == nil {
		httpx.FromError(w, apperr.Unavailable("sms login is not enabled"), traceID)
		return
	}
	if err := h.sms.Send(r.Context(), app.SendSMSCodeInput{
		Phone:  req.Phone,
		IPHash: app.HashClientRef(clientIP(r)),
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
	if err := decodeJSON(r, &req); err != nil {
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
		IPHash:  app.HashClientRef(clientIP(r)),
		UAHash:  app.HashClientRef(r.UserAgent()),
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
	if err := decodeJSON(r, &req); err != nil {
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
		IPHash:  app.HashClientRef(clientIP(r)),
		UAHash:  app.HashClientRef(r.UserAgent()),
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
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.sms == nil {
		httpx.FromError(w, apperr.Unavailable("phone rebind is not enabled"), traceID)
		return
	}
	if err := h.sms.Rebind(r.Context(), app.SMSRebindInput{
		Actor:  actor,
		Phone:  req.Phone,
		Code:   req.Code,
		IPHash: app.HashClientRef(clientIP(r)),
	}); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteAccount DELETE /c/v1/auth/account
//
// 用户自助注销（软删）；手机号主体仍占用唯一键，不允许立即被他人重新注册。
func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := cActorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.auth == nil {
		httpx.FromError(w, apperr.Unavailable("account deletion is not configured"), traceID)
		return
	}
	if err := h.auth.DeleteConsumerAccount(r.Context(), actor); err != nil {
		httpx.FromError(w, err, traceID)
		return
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

// OAuthAuthorize POST /c/v1/auth/oauth/{provider}/authorize
//
// 为指定 provider 生成一次性 state 与 PKCE verifier，返回第三方授权地址；
// provider 未配置时路由不存在（404），state 有效期 10 分钟。
func (h *Handler) OAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.oauth == nil {
		httpx.FromError(w, apperr.Unavailable("oauth login is not enabled"), traceID)
		return
	}
	result, err := h.oauth.Authorize(r.Context(), r.PathValue("provider"))
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
	var req oauthCallbackRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.oauth == nil {
		httpx.FromError(w, apperr.Unavailable("oauth login is not enabled"), traceID)
		return
	}
	result, err := h.oauth.Callback(r.Context(), app.OAuthCallbackInput{
		Provider: r.PathValue("provider"),
		Code:     req.Code,
		State:    req.State,
		IPHash:   app.HashClientRef(clientIP(r)),
		UAHash:   app.HashClientRef(r.UserAgent()),
		TraceID:  traceID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, tokenResponseFrom(result.TokenPair), traceID)
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
type smsRebindRequest struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
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
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// tokenResponseFrom 把令牌对转换为响应 DTO。
func tokenResponseFrom(pair app.TokenPair) tokenResponse {
	return tokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    pair.ExpiresIn,
	}
}

// decodeJSON 解码请求体；未知字段与格式错误统一返回 40000。
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return apperr.BadRequest("invalid request body")
	}
	return nil
}

// clientIP 提取客户端 IP；与 rest 包保持一致的代理头约定。
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first, _, ok := strings.Cut(forwarded, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(forwarded)
	}
	return r.RemoteAddr
}
