// Package rest 提供 A / B 端共用的身份认证 HTTP Handler 与 DTO。
//
// 通道差异（端别、路由前缀）在 transport/admin 与 transport/biz 的路由层注入，
// 本包只负责「请求 → 用例 → 响应」的机械转换。
package rest

import (
	"encoding/json"
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
	httpx.OK(w, tokenResponseFrom(result.TokenPair), traceID)
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
