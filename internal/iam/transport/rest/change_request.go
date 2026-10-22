// Package rest 提供 IAM 的通道无关 HTTP 传输层。
//
// 本文件集中承载高风险变更审批单（change request）的 DTO 与 Handler：
// 审批端点是 A 端专属能力，B 端路由不注册，避免在缺少 merchant_id 的
// 数据模型上暴露跨租户数据（SEC-14）。
package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// ChangeRequestDTO 是审批单响应；payload 原样返回，便于审批人核对申请意图。
type ChangeRequestDTO struct {
	ID          string          `json:"id"`
	Channel     string          `json:"channel"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason"`
	RequestedBy string          `json:"requested_by"`
	RequestedAt time.Time       `json:"requested_at"`
	ApprovedBy  string          `json:"approved_by"`
	ApprovedAt  *time.Time      `json:"approved_at,omitempty"`
	AppliedAt   *time.Time      `json:"applied_at,omitempty"`
	Version     int64           `json:"version"`
}

// ChangeRequestListResponse 是审批单列表响应。
type ChangeRequestListResponse struct {
	Items []*ChangeRequestDTO `json:"items"`
	Total int                 `json:"total"`
}

// CreateChangeRequestRequest 是创建审批单请求体。
//
// payload 必须是 JSON 对象：服务端会拒绝 Token / 密钥字段，B 端还会拒绝 merchant_id。
type CreateChangeRequestRequest struct {
	Channel  string          `json:"channel"`
	Action   string          `json:"action"`
	TargetID string          `json:"target_id"`
	Payload  json.RawMessage `json:"payload"`
	Reason   string          `json:"reason"`
}

// DecideChangeRequestRequest 是通过 / 拒绝 / 撤销 / 应用审批单的请求体。
type DecideChangeRequestRequest struct {
	Version int64  `json:"version"`
	Reason  string `json:"reason"`
}

// toChangeRequestDTO 转换审批单聚合；空载荷归一为 {}，避免前端出现 null。
func toChangeRequestDTO(request *domain.ChangeRequest) *ChangeRequestDTO {
	payload := request.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	return &ChangeRequestDTO{
		ID:          request.ID,
		Channel:     string(request.Channel),
		Action:      string(request.Action),
		TargetType:  string(request.TargetType),
		TargetID:    request.TargetID,
		Payload:     json.RawMessage(payload),
		Status:      string(request.Status),
		Reason:      request.Reason,
		RequestedBy: request.RequestedBy,
		RequestedAt: request.RequestedAt,
		ApprovedBy:  request.ApprovedBy,
		ApprovedAt:  request.ApprovedAt,
		AppliedAt:   request.AppliedAt,
		Version:     request.Version,
	}
}

// ListChangeRequests GET /iam/change-requests
//
// A 端复核入口：默认按申请时间倒序，可按 status / action / requested_by 过滤。
func (h *Handler) ListChangeRequests(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if !h.adminOnly(w, traceID) {
		return
	}
	page, err := pagination.Parse(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	filter := domain.ChangeRequestFilter{
		Channel:     domain.Channel(strings.TrimSpace(query.Get("channel"))),
		Status:      domain.ChangeStatus(strings.TrimSpace(query.Get("status"))),
		RequestedBy: strings.TrimSpace(query.Get("requested_by")),
		Action:      domain.ChangeAction(strings.TrimSpace(query.Get("action"))),
		Limit:       page.Limit(),
		Offset:      page.Offset(),
	}
	requests, err := h.changes.List(r.Context(), filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	total, err := h.changes.Count(r.Context(), filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items := make([]*ChangeRequestDTO, 0, len(requests))
	for _, request := range requests {
		items = append(items, toChangeRequestDTO(request))
	}
	httpx.OK(w, ChangeRequestListResponse{Items: items, Total: total}, traceID)
}

// CreateChangeRequest POST /iam/change-requests
func (h *Handler) CreateChangeRequest(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if !h.adminOnly(w, traceID) {
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req CreateChangeRequestRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	channel := h.effectiveChannel(req.Channel)
	if channel == "" {
		httpx.FromError(w, apperr.BadRequest("channel does not match this endpoint"), traceID)
		return
	}
	request, err := h.changes.Create(r.Context(), app.CreateChangeRequestCommand{
		Channel:  channel,
		Action:   domain.ChangeAction(strings.TrimSpace(req.Action)),
		TargetID: strings.TrimSpace(req.TargetID),
		Payload:  req.Payload,
		Reason:   req.Reason,
		Actor:    principal.SubjectID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toChangeRequestDTO(request), traceID)
}

// ApproveChangeRequest POST /iam/change-requests/{id}/approve
func (h *Handler) ApproveChangeRequest(w http.ResponseWriter, r *http.Request) {
	h.decideChangeRequest(w, r, h.changes.Approve)
}

// RejectChangeRequest POST /iam/change-requests/{id}/reject
func (h *Handler) RejectChangeRequest(w http.ResponseWriter, r *http.Request) {
	h.decideChangeRequest(w, r, h.changes.Reject)
}

// CancelChangeRequest POST /iam/change-requests/{id}/cancel
func (h *Handler) CancelChangeRequest(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if !h.adminOnly(w, traceID) {
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
	request, err := h.changes.Cancel(r.Context(), r.PathValue("id"), version, principal.SubjectID)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toChangeRequestDTO(request), traceID)
}

// ApplyChangeRequest POST /iam/change-requests/{id}/apply
func (h *Handler) ApplyChangeRequest(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if !h.adminOnly(w, traceID) {
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
	request, err := h.changes.Apply(r.Context(), app.ApplyChangeRequestCommand{
		ID:      r.PathValue("id"),
		Version: version,
		Actor:   principal.SubjectID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toChangeRequestDTO(request), traceID)
}

// decideChangeRequest 抽取通过 / 拒绝的公共解析逻辑。
func (h *Handler) decideChangeRequest(w http.ResponseWriter, r *http.Request,
	decide func(context.Context, string, int64, string) (*domain.ChangeRequest, error)) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if !h.adminOnly(w, traceID) {
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
	request, err := decide(r.Context(), r.PathValue("id"), version, principal.SubjectID)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toChangeRequestDTO(request), traceID)
}

// adminOnly 拒绝 B 端访问审批端点。
//
// 审批单表暂不含 merchant_id，跨租户隔离无法在 SQL 层保证；在补列迁移落地前
// 直接对外 404，避免 B 端看到其它商户的申请内容（SEC-14）。
func (h *Handler) adminOnly(w http.ResponseWriter, traceID string) bool {
	if h.channel == domain.ChannelBiz {
		httpx.FromError(w, apperr.NotFound("change request not found"), traceID)
		return false
	}
	return true
}
