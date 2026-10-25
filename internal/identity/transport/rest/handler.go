// Package rest 提供 A / B 端共用的身份认证 HTTP Handler 与 DTO。
//
// 通道差异（端别、路由前缀）在 transport/admin 与 transport/biz 的路由层注入，
// 本包只负责「请求 → 用例 → 响应」的机械转换。
package rest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// HandlerConfig 是认证 Handler 的装配参数。
type HandlerConfig struct {
	// Auth 认证用例服务。
	Auth *app.AuthService
	// Channel 进程固定端别；不接受请求参数覆盖。
	Channel domain.Channel
}

// Handler 处理认证 HTTP 请求。
type Handler struct {
	auth    *app.AuthService
	channel domain.Channel
}

// NewHandler 构造认证 Handler。
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Auth == nil {
		return nil, apperr.Internal("auth handler service is required", nil)
	}
	if err := cfg.Channel.Validate(); err != nil {
		return nil, err
	}
	return &Handler{auth: cfg.Auth, channel: cfg.Channel}, nil
}

// Login POST /{channel}/v1/auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	result, err := h.auth.Login(r.Context(), app.LoginInput{
		Subject:  req.Subject,
		Password: req.Password,
		IPHash:   app.HashClientRef(clientIP(r)),
		UAHash:   app.HashClientRef(r.UserAgent()),
		TraceID:  traceID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if result.MFABindingRequired {
		httpx.OK(w, mfaBindingResponse{MFABindingRequired: true, MFASetupTicket: result.MFASetupTicket}, traceID)
		return
	}
	if result.MFARequired {
		httpx.OK(w, mfaChallengeResponse{MFARequired: true, MFATicket: result.MFATicket}, traceID)
		return
	}
	httpx.OK(w, tokenResponseFrom(result.TokenPair), traceID)
}

// SetupMFA POST /admin/v1/auth/mfa/setup
//
// 两种入口：Bearer（已登录绑定）或请求体中的 mfa_setup_ticket（登录前强制绑定）。
// 返回 TOTP 密钥与恢复码，仅本次响应可见；已绑定时返回 40900。
func (h *Handler) SetupMFA(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	var req mfaSetupRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var actor app.SessionActor
	if strings.TrimSpace(req.SetupTicket) == "" {
		var actorErr error
		actor, actorErr = actorFrom(r)
		if actorErr != nil {
			httpx.FromError(w, actorErr, traceID)
			return
		}
	}
	result, err := h.auth.SetupMFA(r.Context(), app.MFASetupInput{
		SetupTicket: req.SetupTicket,
		Actor:       actor,
		IPHash:      app.HashClientRef(clientIP(r)),
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, mfaSetupResponse{
		Secret:        result.Secret,
		OTPAuthURI:    result.OTPAuthURI,
		RecoveryCodes: result.RecoveryCodes,
	}, traceID)
}

// VerifyMFA POST /admin/v1/auth/mfa/verify
//
// 三种模式：mfa_ticket 完成两步登录；mfa_setup_ticket 完成强制绑定并签发令牌；
// 两者都不带时凭 Authorization 完成已登录账号的绑定确认（204）。
func (h *Handler) VerifyMFA(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	var req mfaVerifyRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var actor app.SessionActor
	if strings.TrimSpace(req.Ticket) == "" && strings.TrimSpace(req.SetupTicket) == "" {
		var actorErr error
		actor, actorErr = actorFrom(r)
		if actorErr != nil {
			httpx.FromError(w, actorErr, traceID)
			return
		}
	}
	result, err := h.auth.VerifyMFA(r.Context(), app.MFAVerifyInput{
		Ticket:      req.Ticket,
		SetupTicket: req.SetupTicket,
		Code:        req.Code,
		Actor:       actor,
		IPHash:      app.HashClientRef(clientIP(r)),
		UAHash:      app.HashClientRef(r.UserAgent()),
		TraceID:     traceID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if result.BindingConfirmed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpx.OK(w, tokenResponseFrom(result.TokenPair), traceID)
}

// DisableMFA POST /admin/v1/auth/mfa/disable
//
// 校验动态码或恢复码后解绑，并撤销该账号全部会话。
func (h *Handler) DisableMFA(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req mfaDisableRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if err := h.auth.DisableMFA(r.Context(), actor, req.Code); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Refresh POST /{channel}/v1/auth/refresh
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	var req refreshRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	pair, err := h.auth.Refresh(r.Context(), app.RefreshInput{
		RefreshToken: req.RefreshToken,
		TraceID:      traceID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, tokenResponseFrom(pair), traceID)
}

// Logout POST /{channel}/v1/auth/logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if err := h.auth.Logout(r.Context(), actor); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// LogoutAll POST /{channel}/v1/auth/logout-all
func (h *Handler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if err := h.auth.LogoutAll(r.Context(), actor); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListSessions GET /admin/v1/auth/sessions
func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	sessions, err := h.auth.ListSessions(r.Context(), actor)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items := make([]sessionDTO, 0, len(sessions))
	for _, view := range sessions {
		items = append(items, sessionDTO{
			ID:            view.ID,
			Current:       view.Current,
			Status:        view.Status,
			CreatedAt:     view.CreatedAt,
			LastUsedAt:    view.LastUsedAt,
			ExpiresAt:     view.ExpiresAt,
			IdleExpiresAt: zeroAsNil(view.IdleExpiresAt),
			RevokedAt:     view.RevokedAt,
		})
	}
	httpx.OK(w, sessionListResponse{Items: items}, traceID)
}

// RevokeSession DELETE /admin/v1/auth/sessions/{id}
func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if err := h.auth.RevokeSession(r.Context(), actor, r.PathValue("id")); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loginRequest 只接受主体与密码；channel / roles / merchant_id 一律由服务端推导。
type loginRequest struct {
	Subject  string `json:"subject"`
	Password string `json:"password"`
}

// refreshRequest 刷新请求。
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// mfaChallengeResponse 密码正确但还需 MFA 时的响应；不包含任何令牌。
type mfaChallengeResponse struct {
	MFARequired bool   `json:"mfa_required"`
	MFATicket   string `json:"mfa_ticket"`
}

// mfaBindingResponse 密码正确但仍需完成首次绑定时的响应；同样不包含业务令牌。
type mfaBindingResponse struct {
	MFABindingRequired bool   `json:"mfa_binding_required"`
	MFASetupTicket     string `json:"mfa_setup_ticket"`
}

// mfaSetupRequest 绑定初始化请求；已登录绑定时可省略整个请求体。
type mfaSetupRequest struct {
	SetupTicket string `json:"mfa_setup_ticket,omitempty"`
}

// mfaSetupResponse 绑定初始化响应；密钥与恢复码只返回一次。
type mfaSetupResponse struct {
	Secret        string   `json:"secret"`
	OTPAuthURI    string   `json:"otpauth_uri"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// mfaVerifyRequest 校验请求：mfa_ticket 用于两步登录，mfa_setup_ticket 用于强制绑定；
// 两者都缺省时按已认证会话确认绑定。
type mfaVerifyRequest struct {
	Ticket      string `json:"mfa_ticket,omitempty"`
	SetupTicket string `json:"mfa_setup_ticket,omitempty"`
	Code        string `json:"code"`
}

// mfaDisableRequest 解绑请求。
type mfaDisableRequest struct {
	Code string `json:"code"`
}

// tokenResponse 登录 / 刷新成功响应；令牌原文只在此出现一次。
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// sessionDTO 会话列表项；不包含任何令牌或 IP / UA 原文。
type sessionDTO struct {
	ID            string     `json:"id"`
	Current       bool       `json:"current"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	LastUsedAt    time.Time  `json:"last_used_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	IdleExpiresAt *time.Time `json:"idle_expires_at,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

// sessionListResponse 会话列表。
type sessionListResponse struct {
	Items []sessionDTO `json:"items"`
}

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

// decodeOptionalJSON 解码可选请求体：空 body 视为「未提供」，保持向后兼容。
func decodeOptionalJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return apperr.BadRequest("invalid request body")
	}
	return nil
}

// actorFrom 从鉴权上下文构造用例入参；缺少 Principal 视为未认证。
func actorFrom(r *http.Request) (app.SessionActor, error) {
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

// clientIP 提取客户端 IP；只用于计算不可逆摘要，不直接落库。
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first, _, ok := strings.Cut(forwarded, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(forwarded)
	}
	return r.RemoteAddr
}

// zeroAsNil 把零值时间转换为 null，避免响应出现 0001-01-01。
func zeroAsNil(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
