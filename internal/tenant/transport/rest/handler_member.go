package rest

import (
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// ListMembers 处理 GET /admin/v1/tenants/{id}/members。
//
// 默认只返回在册成员；`include_disabled=true` 时一并返回已停用成员（离职回溯用）。
// 分页（T-13）：`page` / `page_size` 与租户列表同一套上限配置，超限按上限收敛。
func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
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
	includeDisabled := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_disabled")), "true")
	members, total, err := h.members.List(r.Context(), r.PathValue("id"), includeDisabled,
		page.Limit(), page.Offset())
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items := make([]memberResponse, 0, len(members))
	for _, member := range members {
		items = append(items, toMemberResponse(member))
	}
	apiresp.OK(w, memberListResponse{
		Items:    items,
		Total:    total,
		Page:     page.Page,
		PageSize: page.PageSize,
	}, traceID)
}

// AddMember 处理 POST /admin/v1/tenants/{id}/members。
func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body addMemberRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	member, err := h.members.Add(r.Context(), app.AddMemberInput{
		TenantID:  r.PathValue("id"),
		SubjectID: body.SubjectID,
		Role:      domain.MemberRole(strings.TrimSpace(body.Role)),
		Actor:     actor,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.JSON(w, http.StatusCreated, 0, "ok", toMemberResponse(member), traceID)
}

// UpdateMember 处理 PATCH /admin/v1/tenants/{id}/members/{subject_id}。
//
// 同一接口承载「改角色」与「启停」：两者都是对同一条成员记录的字段变更，
// 拆成两个路径会让前端需要在一次操作里发两个请求。
//
// 只调用一次用例：入参校验与写入在应用层合并为一次落库，
// 避免「角色已改、状态非法」的半提交（旧实现分两次调用，第二步失败时第一步已生效）。
func (h *Handler) UpdateMember(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body updateMemberRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}

	input := app.UpdateMemberInput{
		TenantID:  r.PathValue("id"),
		SubjectID: r.PathValue("subject_id"),
		Reason:    body.Reason,
		Actor:     actor,
	}
	// 空串表示「未提供」；是否至少给了一个由应用层统一判定，handler 不重复校验。
	if role := strings.TrimSpace(body.Role); role != "" {
		target := domain.MemberRole(role)
		input.Role = &target
	}
	if status := strings.TrimSpace(body.Status); status != "" {
		target := domain.MemberStatus(status)
		input.Status = &target
	}

	member, err := h.members.Update(r.Context(), input)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toMemberResponse(member), traceID)
}

// ListQuotas 处理 GET /admin/v1/tenants/{id}/quotas。
func (h *Handler) ListQuotas(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if _, err := h.actor(r); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	quotas, err := h.quotas.List(r.Context(), r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items := make([]quotaResponse, 0, len(quotas))
	for _, quota := range quotas {
		items = append(items, toQuotaResponse(quota))
	}
	apiresp.OK(w, quotaListResponse{Items: items, Total: len(items)}, traceID)
}

// SetQuota 处理 PUT /admin/v1/tenants/{id}/quotas/{resource}。
func (h *Handler) SetQuota(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := h.actor(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body setQuotaRequest
	if err := decodeJSON(r, &body); err != nil {
		apiresp.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	if body.Limit == nil {
		apiresp.FromError(w, apperr.BadRequest("limit is required"), traceID)
		return
	}
	quota, err := h.quotas.Set(r.Context(), app.SetQuotaInput{
		TenantID: r.PathValue("id"),
		Resource: r.PathValue("resource"),
		Limit:    *body.Limit,
		Actor:    actor,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toQuotaResponse(quota), traceID)
}

// addMemberRequest 是添加成员请求体。
type addMemberRequest struct {
	// SubjectID 成员主体 ID。
	SubjectID string `json:"subject_id"`
	// Role 成员角色。
	Role string `json:"role"`
}

// updateMemberRequest 是成员变更请求体；role / status 至少给一个。
type updateMemberRequest struct {
	// Role 目标角色。
	Role string `json:"role,omitempty"`
	// Status 目标状态。
	Status string `json:"status,omitempty"`
	// Reason 启停原因。
	Reason string `json:"reason,omitempty"`
}

// setQuotaRequest 是配额设置请求体。
type setQuotaRequest struct {
	// Limit 上限；-1 表示不限。指针用于区分「未提供」与「设为 0」。
	Limit *int64 `json:"limit"`
}

// memberResponse 是成员响应体。
type memberResponse struct {
	TenantID  string `json:"tenant_id"`
	SubjectID string `json:"subject_id"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// memberListResponse 是成员列表响应体。
type memberListResponse struct {
	Items    []memberResponse `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// quotaResponse 是配额响应体。
type quotaResponse struct {
	TenantID  string `json:"tenant_id"`
	Resource  string `json:"resource"`
	Limit     int64  `json:"limit"`
	UpdatedAt string `json:"updated_at"`
}

// quotaListResponse 是配额列表响应体。
type quotaListResponse struct {
	Items []quotaResponse `json:"items"`
	Total int             `json:"total"`
}

// toMemberResponse 转换成员实体。
func toMemberResponse(member domain.Member) memberResponse {
	return memberResponse{
		TenantID:  member.TenantID,
		SubjectID: member.SubjectID,
		Role:      string(member.Role),
		Status:    string(member.Status),
		CreatedAt: formatTime(member.CreatedAt),
		UpdatedAt: formatTime(member.UpdatedAt),
	}
}

// toQuotaResponse 转换配额实体。
func toQuotaResponse(quota domain.Quota) quotaResponse {
	return quotaResponse{
		TenantID:  quota.TenantID,
		Resource:  quota.Resource,
		Limit:     quota.Limit,
		UpdatedAt: formatTime(quota.UpdatedAt),
	}
}
