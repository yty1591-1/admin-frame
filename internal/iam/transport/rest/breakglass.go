// Package rest 提供 IAM 的通道无关 HTTP 传输层。
//
// 本文件承载破玻璃（限时超级权限）端点：申请、审批生效、提前撤销。
// 与审批单一致，这些端点只在 A 端注册；B 端路由不暴露（SEC-14）。
package rest

import (
	"net/http"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// BreakglassDTO 是破玻璃申请响应；载荷字段平铺，便于安全值班快速核对。
type BreakglassDTO struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	SubjectType     string     `json:"subject_type"`
	SubjectID       string     `json:"subject_id"`
	DurationSeconds int        `json:"duration_seconds"`
	Reason          string     `json:"reason"`
	RequestedBy     string     `json:"requested_by"`
	RequestedAt     time.Time  `json:"requested_at"`
	ApprovedBy      string     `json:"approved_by"`
	ApprovedAt      *time.Time `json:"approved_at,omitempty"`
	AppliedAt       *time.Time `json:"applied_at,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	BindingID       string     `json:"binding_id,omitempty"`
	Version         int64      `json:"version"`
}

// CreateBreakglassRequest 是破玻璃申请请求体。
type CreateBreakglassRequest struct {
	SubjectType     string `json:"subject_type"`
	SubjectID       string `json:"subject_id"`
	DurationSeconds int    `json:"duration_seconds"`
	Reason          string `json:"reason"`
}

// toBreakglassDTO 转换审批单；expires_at 由生效时间 + 申请时长推导（上限 2h）。
func toBreakglassDTO(request *domain.ChangeRequest) *BreakglassDTO {
	dto := &BreakglassDTO{
		ID:          request.ID,
		Status:      string(request.Status),
		Reason:      request.Reason,
		RequestedBy: request.RequestedBy,
		RequestedAt: request.RequestedAt,
		ApprovedBy:  request.ApprovedBy,
		ApprovedAt:  request.ApprovedAt,
		AppliedAt:   request.AppliedAt,
		BindingID:   request.TargetID,
		Version:     request.Version,
	}
	var payload breakglassRequestPayload
	if err := request.DecodePayload(&payload); err == nil {
		dto.SubjectType = payload.SubjectType
		dto.SubjectID = payload.SubjectID
		dto.DurationSeconds = payload.DurationSeconds
		if request.AppliedAt != nil {
			expiresAt := request.AppliedAt.Add(time.Duration(payload.DurationSeconds) * time.Second)
			dto.ExpiresAt = &expiresAt
		}
	}
	return dto
}

// breakglassRequestPayload 是破玻璃载荷的传输镜像。
type breakglassRequestPayload struct {
	SubjectType     string `json:"subject_type"`
	SubjectID       string `json:"subject_id"`
	MerchantID      string `json:"merchant_id"`
	DurationSeconds int    `json:"duration_seconds"`
}

// RequestBreakglass POST /iam/breakglass
func (h *Handler) RequestBreakglass(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if !h.adminOnly(w, traceID) {
		return
	}
	if h.breakglass == nil {
		httpx.FromError(w, apperr.Internal("iam breakglass service is not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req CreateBreakglassRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	request, err := h.breakglass.Request(r.Context(), app.RequestBreakglassCommand{
		SubjectType: req.SubjectType,
		SubjectID:   req.SubjectID,
		Duration:    time.Duration(req.DurationSeconds) * time.Second,
		Reason:      req.Reason,
		Actor:       principal.SubjectID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toBreakglassDTO(request), traceID)
}

// ApproveBreakglass POST /iam/breakglass/{id}/approve
//
// 审批通过并立即生效；重试已 approved 的单子会继续应用，已 applied 则幂等返回。
func (h *Handler) ApproveBreakglass(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if !h.adminOnly(w, traceID) {
		return
	}
	if h.breakglass == nil {
		httpx.FromError(w, apperr.Internal("iam breakglass service is not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req DecideChangeRequestRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	request, err := h.breakglass.Approve(r.Context(), app.ApproveBreakglassCommand{
		ID:       r.PathValue("id"),
		Version:  version,
		Approver: principal.SubjectID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toBreakglassDTO(request), traceID)
}

// RevokeBreakglass POST /iam/breakglass/{id}/revoke
//
// 提前撤销已生效的限时绑定；version 必须与当前审批单一致，避免旧页面误撤新授权。
func (h *Handler) RevokeBreakglass(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if !h.adminOnly(w, traceID) {
		return
	}
	if h.breakglass == nil {
		httpx.FromError(w, apperr.Internal("iam breakglass service is not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req DecideChangeRequestRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	request, err := h.breakglass.Revoke(r.Context(), r.PathValue("id"), version, principal.SubjectID)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toBreakglassDTO(request), traceID)
}

// adminOnly 拒绝 B 端访问破玻璃端点。
//
// 破玻璃是平台级应急能力：B 端若可自助申请，等于把超级权限的入口下放到租户，
// 超出当前威胁模型（与 ChangeRequestService.Create 的闸门互为兜底）。
// 路由层直接 404，避免暴露端点存在性。
func (h *Handler) adminOnly(w http.ResponseWriter, traceID string) bool {
	if h.channel == domain.ChannelBiz {
		httpx.FromError(w, apperr.NotFound("breakglass not found"), traceID)
		return false
	}
	return true
}
