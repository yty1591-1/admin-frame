// Package rest 提供 IAM 的通道无关 HTTP 传输层。
//
// 本文件集中承载高风险变更审批单（change request）的 DTO 与 Handler。
//
// 同一套 Handler 同时服务 A 端与 B 端：端别来自路由挂载点，商户来自认证主体
// （Principal.MerchantID），请求体与查询参数都不能指定商户（SEC-14）。
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
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// ChangeRequestDTO 是审批单响应；payload 原样返回，便于审批人核对申请意图。
//
// merchant_id 只对 A 端回显（平台运营需要定位租户）；B 端回显一个自己已知的值
// 没有信息增益，反而把租户标识扩散到客户端日志，因此 B 端置空。
type ChangeRequestDTO struct {
	ID             string          `json:"id"`
	Channel        string          `json:"channel"`
	MerchantID     string          `json:"merchant_id,omitempty"`
	Action         string          `json:"action"`
	TargetType     string          `json:"target_type"`
	TargetID       string          `json:"target_id"`
	Payload        json.RawMessage `json:"payload"`
	PayloadVersion int32           `json:"payload_version"`
	Status         string          `json:"status"`
	Reason         string          `json:"reason"`
	RequestedBy    string          `json:"requested_by"`
	RequestedAt    time.Time       `json:"requested_at"`
	ExpiresAt      *time.Time      `json:"expires_at,omitempty"`
	ApprovedBy     string          `json:"approved_by"`
	ApprovedAt     *time.Time      `json:"approved_at,omitempty"`
	DecisionReason string          `json:"decision_reason,omitempty"`
	AppliedBy      string          `json:"applied_by,omitempty"`
	AppliedAt      *time.Time      `json:"applied_at,omitempty"`
	Version        int64           `json:"version"`
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

// toChangeRequestDTO 转换审批单聚合；空载荷归一为 {}，避免客户端出现 null。
//
// includeMerchant=false 时不回显商户（B 端视角，见 ChangeRequestDTO 注释）。
func toChangeRequestDTO(request *domain.ChangeRequest, includeMerchant bool) *ChangeRequestDTO {
	payload := request.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	merchantID := ""
	if includeMerchant {
		merchantID = request.MerchantID
	}
	return &ChangeRequestDTO{
		ID:             request.ID,
		Channel:        string(request.Channel),
		MerchantID:     merchantID,
		Action:         string(request.Action),
		TargetType:     string(request.TargetType),
		TargetID:       request.TargetID,
		Payload:        json.RawMessage(payload),
		PayloadVersion: request.PayloadVersion,
		Status:         string(request.Status),
		Reason:         request.Reason,
		RequestedBy:    request.RequestedBy,
		RequestedAt:    request.RequestedAt,
		ExpiresAt:      request.ExpiresAt,
		ApprovedBy:     request.ApprovedBy,
		ApprovedAt:     request.ApprovedAt,
		DecisionReason: request.DecisionReason,
		AppliedBy:      request.AppliedBy,
		AppliedAt:      request.AppliedAt,
		Version:        request.Version,
	}
}

// changeScope 解析本次请求的审批作用域。
//
// 端别取 transport 挂载点，商户取 Principal：任何试图通过 query / body 指定
// 商户的调用都会被忽略，而不是「看起来生效了、实际用的是别的租户」。
func (h *Handler) changeScope(r *http.Request) (auth.Principal, app.ChangeScope, error) {
	principal, err := h.principal(r)
	if err != nil {
		return auth.Principal{}, app.ChangeScope{}, err
	}
	scope := app.ChangeScope{Channel: h.channel, MerchantID: h.merchantScope(principal)}
	if err := scope.Validate(); err != nil {
		return auth.Principal{}, app.ChangeScope{}, err
	}
	return principal, scope, nil
}

// ListChangeRequests GET /iam/change-requests
//
// 列表入口（A 端复核 / B 端待办）：默认按申请时间倒序，可按 status / action /
// requested_by 过滤。端别与商户强制来自作用域，query 中的 channel / merchant_id 被忽略。
func (h *Handler) ListChangeRequests(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	_, scope, err := h.changeScope(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	page, err := pagination.Parse(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	filter := domain.ChangeRequestFilter{
		Status:      domain.ChangeStatus(strings.TrimSpace(query.Get("status"))),
		RequestedBy: strings.TrimSpace(query.Get("requested_by")),
		Action:      domain.ChangeAction(strings.TrimSpace(query.Get("action"))),
		Limit:       page.Limit(),
		Offset:      page.Offset(),
	}
	requests, err := h.changes.List(r.Context(), scope, filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	total, err := h.changes.Count(r.Context(), scope, filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	includeMerchant := h.channel != domain.ChannelBiz
	items := make([]*ChangeRequestDTO, 0, len(requests))
	for _, request := range requests {
		items = append(items, toChangeRequestDTO(request, includeMerchant))
	}
	httpx.OK(w, ChangeRequestListResponse{Items: items, Total: total}, traceID)
}

// CreateChangeRequest POST /iam/change-requests
func (h *Handler) CreateChangeRequest(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, scope, err := h.changeScope(r)
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
		Channel:    channel,
		MerchantID: scope.MerchantID,
		Action:     domain.ChangeAction(strings.TrimSpace(req.Action)),
		TargetID:   strings.TrimSpace(req.TargetID),
		Payload:    req.Payload,
		Reason:     req.Reason,
		Actor:      principal.SubjectID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toChangeRequestDTO(request, h.channel != domain.ChannelBiz), traceID)
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
	principal, scope, err := h.changeScope(r)
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
	request, err := h.changes.Cancel(r.Context(), r.PathValue("id"), version, principal.SubjectID, scope)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toChangeRequestDTO(request, h.channel != domain.ChannelBiz), traceID)
}

// ApplyChangeRequest POST /iam/change-requests/{id}/apply
func (h *Handler) ApplyChangeRequest(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, scope, err := h.changeScope(r)
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
		Scope:   scope,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toChangeRequestDTO(request, h.channel != domain.ChannelBiz), traceID)
}

// decideChangeRequest 抽取通过 / 拒绝的公共解析逻辑。
func (h *Handler) decideChangeRequest(w http.ResponseWriter, r *http.Request,
	decide func(context.Context, string, int64, string, string, app.ChangeScope) (*domain.ChangeRequest, error)) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, scope, err := h.changeScope(r)
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
	request, err := decide(r.Context(), r.PathValue("id"), version, principal.SubjectID, req.Reason, scope)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toChangeRequestDTO(request, h.channel != domain.ChannelBiz), traceID)
}
