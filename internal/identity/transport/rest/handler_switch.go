package rest

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// SwitchTenant 处理 POST /biz/v1/auth/tenant-switch（ENT-09 T-22）。
//
// 凭据是当前 Bearer 令牌（须携带有效会话）；成功返回同会话重签的
// access token（current_tenant 已更新）。refresh token 不变——
// 切换租户不是重新认证。
func (h *Handler) SwitchTenant(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var body switchTenantRequest
	if err := decodeStrictJSON(r, &body, false); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	result, err := h.auth.SwitchTenant(r.Context(), app.SwitchTenantInput{
		SessionActor: actor,
		TenantID:     body.TenantID,
		TraceID:      traceID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, switchTenantResponse{
		AccessToken:   result.AccessToken,
		TokenType:     "Bearer",
		ExpiresIn:     result.ExpiresIn,
		CurrentTenant: result.CurrentTenant,
	}, traceID)
}

// switchTenantRequest 是租户切换请求体。
type switchTenantRequest struct {
	// TenantID 目标租户 ID。
	TenantID string `json:"tenant_id"`
}

// switchTenantResponse 是租户切换响应体。
type switchTenantResponse struct {
	// AccessToken 携带新 current_tenant 的访问令牌；旧令牌自然过期，
	// 生效租户回落 merchant_id（旧令牌行为的明确定义）。
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	// CurrentTenant 切换后的生效租户。
	CurrentTenant string `json:"current_tenant"`
}
