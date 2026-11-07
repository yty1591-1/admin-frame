package rest

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// RoleConstraintDTO 是角色 RBAC2 约束配置响应。
type RoleConstraintDTO struct {
	RoleID          string   `json:"role_id"`
	MaxBindings     *int     `json:"max_bindings"`
	RequiredRoleIDs []string `json:"required_role_ids"`
	Version         int64    `json:"version"`
}

// UpdateRoleConstraintRequest 是全量替换约束请求体。
//
// PUT 语义：required_role_ids 与 max_bindings 都视为完整快照，
// max_bindings 为 null 表示取消上限。
type UpdateRoleConstraintRequest struct {
	RequiredRoleIDs []string `json:"required_role_ids"`
	MaxBindings     *int     `json:"max_bindings"`
	Version         int64    `json:"version"`
	Reason          string   `json:"reason"`
}

// toRoleConstraintDTO 转换约束视图。
func toRoleConstraintDTO(view *app.RoleConstraintView) *RoleConstraintDTO {
	return &RoleConstraintDTO{
		RoleID:          view.RoleID,
		MaxBindings:     view.MaxBindings,
		RequiredRoleIDs: view.RequiredRoleIDs,
		Version:         view.Version,
	}
}

// GetRoleConstraints GET /iam/roles/{id}/constraints
func (h *Handler) GetRoleConstraints(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if h.roleConstraints == nil {
		apiresp.FromError(w, apperr.Internal("role constraint service is not configured", nil), traceID)
		return
	}
	view, err := h.roleConstraints.Get(r.Context(), r.PathValue("id"), h.channel, h.merchantScope(principal))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toRoleConstraintDTO(view), traceID)
}

// UpdateRoleConstraints PUT /iam/roles/{id}/constraints
func (h *Handler) UpdateRoleConstraints(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionRoleConstraintUpdate) {
		return
	}
	if h.roleConstraints == nil {
		apiresp.FromError(w, apperr.Internal("role constraint service is not configured", nil), traceID)
		return
	}
	var req UpdateRoleConstraintRequest
	if err := decodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	view, err := h.roleConstraints.Update(r.Context(), app.UpdateRoleConstraintCommand{
		RoleID:          r.PathValue("id"),
		Channel:         h.channel,
		MerchantID:      h.merchantScope(principal),
		RequiredRoleIDs: req.RequiredRoleIDs,
		MaxBindings:     req.MaxBindings,
		Version:         version,
		Actor:           principal.SubjectID,
		Reason:          req.Reason,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toRoleConstraintDTO(view), traceID)
}
