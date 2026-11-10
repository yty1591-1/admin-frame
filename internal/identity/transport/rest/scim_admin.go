package rest

// 本文件实现 SCIM 2.0 的 A 端管理端点（docs/identity-authn.md §21.1.6）：
//   - GET    /admin/v1/auth/scim/tokens       列出令牌指纹与状态
//   - POST   /admin/v1/auth/scim/tokens       签发令牌（返回一次原文）
//   - DELETE /admin/v1/auth/scim/tokens/{id}  吊销令牌
//   - GET    /admin/v1/auth/scim/groups       组、映射角色与成员数量
//
// 这些端点沿用统一信封与 JWT 鉴权；协议端点 /admin/scim/v2/* 在 transport/scim 下。

import (
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// scimMaxPageSize 是管理端组列表单页上限。
const scimMaxPageSize = 100

// SCIMEnabled 报告 SCIM 是否已装配；false 时管理端点不注册（404）。
func (h *Handler) SCIMEnabled() bool {
	return h != nil && h.scim != nil
}

// scimTokenDTO 是令牌视图；不包含任何原文。
type scimTokenDTO struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	Fingerprint string `json:"fingerprint"`
	Status      string `json:"status"`
	CreatedBy   string `json:"created_by,omitempty"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	RevokedAt   string `json:"revoked_at,omitempty"`
}

// ListSCIMTokens GET /admin/v1/auth/scim/tokens
func (h *Handler) ListSCIMTokens(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.scim == nil {
		apiresp.FromError(w, apperr.NotFound("scim is not enabled"), traceID)
		return
	}
	tokens, err := h.scim.ListTokens(r.Context())
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items := make([]scimTokenDTO, 0, len(tokens))
	for _, token := range tokens {
		items = append(items, scimTokenDTO{
			ID:          token.ID,
			Provider:    token.Provider,
			Fingerprint: token.Fingerprint,
			Status:      string(token.Status),
			CreatedBy:   token.CreatedBy,
			CreatedAt:   token.CreatedAt.UTC().Format(time.RFC3339),
			ExpiresAt:   formatOptionalTime(token.ExpiresAt),
			RevokedAt:   formatOptionalTime(token.RevokedAt),
		})
	}
	apiresp.OK(w, map[string]any{"items": items, "total": len(items)}, traceID)
}

// CreateSCIMToken POST /admin/v1/auth/scim/tokens
//
// 响应中的 token 字段只出现一次；库内只存 SHA-256 与指纹。
func (h *Handler) CreateSCIMToken(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.scim == nil {
		apiresp.FromError(w, apperr.NotFound("scim is not enabled"), traceID)
		return
	}
	actorID := actorIDOf(r)
	created, err := h.scim.CreateToken(r.Context(), actorID, traceID)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	apiresp.OK(w, map[string]any{
		"id":          created.Token.ID,
		"provider":    created.Token.Provider,
		"fingerprint": created.Token.Fingerprint,
		"status":      string(created.Token.Status),
		"created_at":  created.Token.CreatedAt.UTC().Format(time.RFC3339),
		// token 只在本次响应出现；调用方必须立即保存到密钥管理系统。
		"token": created.Plaintext,
	}, traceID)
}

// RevokeSCIMToken DELETE /admin/v1/auth/scim/tokens/{id}
func (h *Handler) RevokeSCIMToken(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.scim == nil {
		apiresp.FromError(w, apperr.NotFound("scim is not enabled"), traceID)
		return
	}
	if err := h.scim.RevokeToken(r.Context(), r.PathValue("id"), actorIDOf(r), traceID); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, map[string]any{"revoked": true}, traceID)
}

// scimGroupDTO 是管理端组视图。
type scimGroupDTO struct {
	ID          string `json:"id"`
	ExternalID  string `json:"external_id"`
	DisplayName string `json:"display_name"`
	MappedRole  string `json:"mapped_role"`
	MemberCount int    `json:"member_count"`
	Version     int64  `json:"version"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ListSCIMGroups GET /admin/v1/auth/scim/groups
func (h *Handler) ListSCIMGroups(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.scim == nil {
		apiresp.FromError(w, apperr.NotFound("scim is not enabled"), traceID)
		return
	}
	page, err := pagination.ParseWith(r, pagination.Config{PageSizeParam: "page_size", MaxPageSize: scimMaxPageSize})
	if err != nil {
		apiresp.FromError(w, apperr.BadRequest("invalid pagination parameters"), traceID)
		return
	}
	groups, total, listErr := h.scim.ListGroupSummaries(r.Context(), page.Page, page.PageSize)
	if listErr != nil {
		apiresp.FromError(w, listErr, traceID)
		return
	}
	items := make([]scimGroupDTO, 0, len(groups))
	for _, group := range groups {
		items = append(items, scimGroupDTO{
			ID:          group.ID,
			ExternalID:  group.ExternalID,
			DisplayName: group.DisplayName,
			MappedRole:  group.MappedRole,
			MemberCount: group.MemberCount,
			Version:     group.Version,
			CreatedAt:   group.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:   group.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	apiresp.OK(w, map[string]any{
		"items": items,
		"total": total,
		"page":  page.Page,
		"size":  page.PageSize,
	}, traceID)
}

// actorIDOf 返回当前登录主体 ID；未认证时为空串（路由层已保证认证）。
//
// 与 M2M 管理端点共用同一约定：优先取 Principal.SubjectID。
func actorIDOf(r *http.Request) string {
	principal, ok := auth.FromContext(r.Context())
	if !ok {
		return ""
	}
	return strings.TrimSpace(principal.SubjectID)
}

// formatOptionalTime 格式化可空时间；nil 返回空串。
func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
