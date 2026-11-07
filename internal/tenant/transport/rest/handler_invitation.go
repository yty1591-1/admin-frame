package rest

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// ListInvitations 处理 GET /admin/v1/tenants/{id}/invitations。
func (h *Handler) ListInvitations(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if _, err := h.actor(r); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	page, err := pagination.ParseWith(r, h.pageCfg)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	invitations, total, err := h.invitations.List(r.Context(), r.PathValue("id"),
		r.URL.Query().Get("status"), page.Limit(), page.Offset())
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items := make([]invitationResponse, 0, len(invitations))
	for _, invitation := range invitations {
		items = append(items, toInvitationResponse(invitation))
	}
	apiresp.OK(w, invitationListResponse{
		Items:    items,
		Total:    total,
		Page:     page.Page,
		PageSize: page.PageSize,
	}, traceID)
}

// CreateInvitation 处理 POST /admin/v1/tenants/{id}/invitations。
//
// 明文 token 只在本次响应返回一次（库里仅存摘要）；ENT-10 就绪前由
// 运营线下转交——这是「只落库不发送」的明确阶段语义。
func (h *Handler) CreateInvitation(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body createInvitationRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	invitation, token, err := h.invitations.Create(r.Context(), app.CreateInvitationInput{
		TenantID: r.PathValue("id"),
		Email:    body.Email,
		Role:     domain.MemberRole(body.Role),
		Actor:    actor,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.JSON(w, http.StatusCreated, 0, "ok", invitationWithTokenResponse{
		invitationResponse: toInvitationResponse(invitation),
		Token:              token,
	}, traceID)
}

// AcceptInvitation 处理 POST /admin/v1/tenants/{id}/invitations/accept。
//
// 凭据是 token 本身（摘要查找，不暴露 invitation_id）；重复接受幂等返回 200。
func (h *Handler) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body acceptInvitationRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	result, err := h.invitations.Accept(r.Context(), app.AcceptInvitationInput{
		Token:     body.Token,
		SubjectID: body.SubjectID,
		Actor:     actor,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.JSON(w, http.StatusOK, 0, "ok", invitationAcceptResponse{
		Member:     toMemberResponse(result.Member),
		Invitation: toInvitationResponse(result.Invitation),
		Idempotent: result.Idempotent,
	}, traceID)
}

// RevokeInvitation 处理 POST /admin/v1/tenants/{id}/invitations/{invitation_id}/revoke。
func (h *Handler) RevokeInvitation(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body revokeInvitationRequest
	if err := decodeOptionalJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	invitation, err := h.invitations.Revoke(r.Context(), app.RevokeInvitationInput{
		TenantID:     r.PathValue("id"),
		InvitationID: r.PathValue("invitation_id"),
		Reason:       body.Reason,
		Actor:        actor,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toInvitationResponse(invitation), traceID)
}

// ResendInvitation 处理 POST /admin/v1/tenants/{id}/invitations/{invitation_id}/resend。
//
// 旧 token 立即失效（摘要被覆盖），新 token 在本次响应返回。
func (h *Handler) ResendInvitation(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	invitation, token, err := h.invitations.Resend(r.Context(), app.ResendInvitationInput{
		TenantID:     r.PathValue("id"),
		InvitationID: r.PathValue("invitation_id"),
		Actor:        actor,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.JSON(w, http.StatusOK, 0, "ok", invitationWithTokenResponse{
		invitationResponse: toInvitationResponse(invitation),
		Token:              token,
	}, traceID)
}

// createInvitationRequest 是创建邀请请求体。
type createInvitationRequest struct {
	// Email 被邀请人邮箱。
	Email string `json:"email"`
	// Role 受邀角色。
	Role string `json:"role"`
}

// acceptInvitationRequest 是接受邀请请求体。
type acceptInvitationRequest struct {
	// Token 一次性明文 token。
	Token string `json:"token"`
	// SubjectID 实际接受邀请的主体。
	SubjectID string `json:"subject_id"`
}

// revokeInvitationRequest 是撤销邀请请求体（reason 可选）。
type revokeInvitationRequest struct {
	// Reason 撤销原因，写入审计。
	Reason string `json:"reason,omitempty"`
}

// invitationResponse 是邀请响应体（不含 token）。
type invitationResponse struct {
	ID         string  `json:"id"`
	TenantID   string  `json:"tenant_id"`
	Email      string  `json:"email"`
	Role       string  `json:"role"`
	Status     string  `json:"status"`
	InvitedBy  string  `json:"invited_by"`
	AcceptedBy string  `json:"accepted_by,omitempty"`
	ExpiresAt  string  `json:"expires_at"`
	AcceptedAt *string `json:"accepted_at"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

// invitationWithTokenResponse 是创建 / 重发响应体：多带一次性的明文 token。
type invitationWithTokenResponse struct {
	invitationResponse
	// Token 明文 token；仅此一次下发，服务端只存摘要。
	Token string `json:"token"`
}

// invitationAcceptResponse 是接受响应体：成员关系 + 邀请终态。
type invitationAcceptResponse struct {
	Member     memberResponse     `json:"member"`
	Invitation invitationResponse `json:"invitation"`
	// Idempotent 为 true 表示重复接受（此前已生效）。
	Idempotent bool `json:"idempotent"`
}

// invitationListResponse 是邀请列表响应体。
type invitationListResponse struct {
	Items    []invitationResponse `json:"items"`
	Total    int                  `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
}

// toInvitationResponse 转换邀请实体。
func toInvitationResponse(invitation domain.Invitation) invitationResponse {
	return invitationResponse{
		ID:         invitation.ID,
		TenantID:   invitation.TenantID,
		Email:      invitation.Email,
		Role:       string(invitation.Role),
		Status:     string(invitation.Status),
		InvitedBy:  invitation.InvitedBy,
		AcceptedBy: invitation.AcceptedBy,
		ExpiresAt:  formatTime(invitation.ExpiresAt),
		AcceptedAt: formatTimePtr(invitation.AcceptedAt),
		CreatedAt:  formatTime(invitation.CreatedAt),
		UpdatedAt:  formatTime(invitation.UpdatedAt),
	}
}
