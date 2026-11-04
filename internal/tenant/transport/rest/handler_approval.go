package rest

import (
	"context"
	"net/http"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// approvalRequest 是提交 / 通过 / 驳回的请求体。
//
// 只有 note 一个字段：审批不携带业务数据（材料在租户资料里），
// 避免出现「审批改了租户字段」这种不可追溯的旁路写入。
type approvalRequest struct {
	Note string `json:"note"`
}

// approvalEventResponse 是单条审批事件。
type approvalEventResponse struct {
	ID        int64  `json:"id"`
	Action    string `json:"action"`
	ActorID   string `json:"actor_id"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"created_at"`
}

// approvalResponse 是审批详情（当前态 + 最近一轮事件）。
type approvalResponse struct {
	TenantID     string                  `json:"tenant_id"`
	TenantStatus string                  `json:"tenant_status"`
	Status       string                  `json:"status"`
	Submitter    string                  `json:"submitter,omitempty"`
	Latest       *approvalEventResponse  `json:"latest"`
	History      []approvalEventResponse `json:"history"`
}

// GetApproval 处理 GET /admin/v1/tenants/{id}/approval。
func (h *Handler) GetApproval(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if _, err := h.actor(r); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	view, err := h.service.GetApproval(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toApprovalResponse(view), traceID)
}

// SubmitApproval 处理 POST /admin/v1/tenants/{id}/approval（提交 / 重提）。
//
// 请求体可选：只提交申请不写说明是合法操作（契约 requestBody.required=false）。
func (h *Handler) SubmitApproval(w http.ResponseWriter, r *http.Request) {
	h.handleApproval(w, r, h.service.SubmitApproval, http.StatusOK, false)
}

// ApproveApproval 处理 POST /admin/v1/tenants/{id}/approval/approve。
func (h *Handler) ApproveApproval(w http.ResponseWriter, r *http.Request) {
	h.handleApproval(w, r, h.service.ApproveApproval, http.StatusOK, false)
}

// RejectApproval 处理 POST /admin/v1/tenants/{id}/approval/reject。
//
// 驳回必须带原因（契约 requestBody.required=true）：原因缺失时由领域层返回 400，
// 这里先要求请求体存在，避免「空 body 被当成有原因」的歧义。
func (h *Handler) RejectApproval(w http.ResponseWriter, r *http.Request) {
	h.handleApproval(w, r, h.service.RejectApproval, http.StatusOK, true)
}

// handleApproval 收敛三个动作的解析与响应，避免三份重复代码各自漂移。
func (h *Handler) handleApproval(w http.ResponseWriter, r *http.Request,
	action func(context.Context, app.ApprovalInput) (app.ApprovalView, error), status int, requireBody bool) {
	traceID := httpx.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var body approvalRequest
	decode := decodeOptionalJSON
	if requireBody {
		decode = decodeJSON
	}
	if err := decode(r, &body); err != nil {
		httpx.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	view, err := action(r.Context(), app.ApprovalInput{
		TenantID: r.PathValue("id"),
		Note:     body.Note,
		Actor:    actor,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.JSON(w, status, 0, "ok", toApprovalResponse(view), traceID)
}

// toApprovalResponse 把视图转为响应体；draft 时 latest 为 null。
func toApprovalResponse(view app.ApprovalView) approvalResponse {
	history := make([]approvalEventResponse, 0, len(view.History))
	for _, event := range view.History {
		history = append(history, toApprovalEvent(event))
	}
	var latest *approvalEventResponse
	if view.Latest.Action != "" {
		converted := toApprovalEvent(view.Latest)
		latest = &converted
	}
	return approvalResponse{
		TenantID:     view.TenantID,
		TenantStatus: string(view.TenantStatus),
		Status:       string(view.Status),
		Submitter:    view.Submitter,
		Latest:       latest,
		History:      history,
	}
}

// toApprovalEvent 转换单条事件。
func toApprovalEvent(event domain.Approval) approvalEventResponse {
	return approvalEventResponse{
		ID:        event.ID,
		Action:    string(event.Action),
		ActorID:   event.ActorID,
		Note:      event.Note,
		CreatedAt: formatTime(event.CreatedAt),
	}
}
