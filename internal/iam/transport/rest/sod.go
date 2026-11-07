package rest

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// SodGroupDTO 是互斥角色组响应。
type SodGroupDTO struct {
	ID             string    `json:"id"`
	Channel        string    `json:"channel"`
	MerchantID     string    `json:"merchant_id,omitempty"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	MaxActiveRoles int       `json:"max_active_roles"`
	Status         string    `json:"status"`
	Version        int64     `json:"version"`
	RoleIDs        []string  `json:"role_ids"`
	CreatedBy      string    `json:"created_by,omitempty"`
	UpdatedBy      string    `json:"updated_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SodGroupListResponse 是互斥组列表响应。
type SodGroupListResponse struct {
	Items []*SodGroupDTO `json:"items"`
	Total int            `json:"total"`
}

// CreateSodGroupRequest 是创建互斥组请求体。
type CreateSodGroupRequest struct {
	Channel        string   `json:"channel"`
	Code           string   `json:"code"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	MaxActiveRoles int      `json:"max_active_roles"`
	RoleIDs        []string `json:"role_ids"`
	Reason         string   `json:"reason"`
}

// UpdateSodGroupRequest 是 PATCH 互斥组请求体；nil 字段表示不修改。
type UpdateSodGroupRequest struct {
	Name           *string   `json:"name"`
	Description    *string   `json:"description"`
	MaxActiveRoles *int      `json:"max_active_roles"`
	RoleIDs        *[]string `json:"role_ids"`
	Status         *string   `json:"status"`
	Version        int64     `json:"version"`
	Reason         string    `json:"reason"`
}

// SodViolationDTO 是互斥组存量违规主体。
type SodViolationDTO struct {
	SubjectType string   `json:"subject_type"`
	SubjectID   string   `json:"subject_id"`
	MerchantID  string   `json:"merchant_id,omitempty"`
	RoleIDs     []string `json:"role_ids"`
}

// SodViolationListResponse 是违规预检响应。
type SodViolationListResponse struct {
	Items []*SodViolationDTO `json:"items"`
	Total int                `json:"total"`
}

// DeleteSodGroupRequest 是软删互斥组请求体（version 也可由 If-Match 提供）。
type DeleteSodGroupRequest struct {
	Version int64  `json:"version"`
	Reason  string `json:"reason"`
}

// PreflightSodGroupRequest 是创建 / 改组前的候选配置预检请求。
type PreflightSodGroupRequest struct {
	RoleIDs        []string `json:"role_ids"`
	MaxActiveRoles int      `json:"max_active_roles"`
}

// toSodGroupDTO 转换互斥组聚合。
func toSodGroupDTO(group *domain.SodGroup) *SodGroupDTO {
	return &SodGroupDTO{
		ID:             group.ID,
		Channel:        string(group.Channel),
		MerchantID:     group.MerchantID,
		Code:           group.Code,
		Name:           group.Name,
		Description:    group.Description,
		MaxActiveRoles: group.MaxActiveRoles,
		Status:         string(group.Status),
		Version:        group.Version,
		RoleIDs:        group.SortedRoleIDs(),
		CreatedBy:      group.CreatedBy,
		UpdatedBy:      group.UpdatedBy,
		CreatedAt:      group.CreatedAt,
		UpdatedAt:      group.UpdatedAt,
	}
}

// toSodViolationDTOs 转换违规列表。
func toSodViolationDTOs(violations []domain.SodViolation) []*SodViolationDTO {
	out := make([]*SodViolationDTO, 0, len(violations))
	for _, violation := range violations {
		out = append(out, &SodViolationDTO{
			SubjectType: string(violation.SubjectType),
			SubjectID:   violation.SubjectID,
			MerchantID:  violation.MerchantID,
			RoleIDs:     violation.RoleIDs,
		})
	}
	return out
}

// ListSodGroups GET /iam/sod-groups
func (h *Handler) ListSodGroups(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if h.sodGroups == nil {
		apiresp.FromError(w, apperr.Internal("iam sod group service is not configured", nil), traceID)
		return
	}
	page, err := pagination.Parse(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	filter := domain.SodGroupFilter{
		Channel:    domain.Channel(strings.TrimSpace(query.Get("channel"))),
		MerchantID: strings.TrimSpace(query.Get("merchant_id")),
		Status:     domain.SodGroupStatus(strings.TrimSpace(query.Get("status"))),
		Keyword:    strings.TrimSpace(query.Get("keyword")),
		Limit:      page.Limit(),
		Offset:     page.Offset(),
	}
	if h.channel == domain.ChannelBiz {
		if principal.MerchantID == "" {
			apiresp.FromError(w, apperr.Forbidden("merchant id is required"), traceID)
			return
		}
		filter.Channel = domain.ChannelBiz
		filter.MerchantID = principal.MerchantID
	}
	groups, err := h.sodGroups.List(r.Context(), filter)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	total, err := h.sodGroups.Count(r.Context(), filter)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items := make([]*SodGroupDTO, 0, len(groups))
	for _, group := range groups {
		items = append(items, toSodGroupDTO(group))
	}
	apiresp.OK(w, SodGroupListResponse{Items: items, Total: total}, traceID)
}

// CreateSodGroup POST /iam/sod-groups
func (h *Handler) CreateSodGroup(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if h.sodGroups == nil {
		apiresp.FromError(w, apperr.Internal("iam sod group service is not configured", nil), traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionSodGroupCreate) {
		return
	}
	var req CreateSodGroupRequest
	if err := decodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	channel := h.effectiveChannel(req.Channel)
	if channel == "" {
		apiresp.FromError(w, apperr.BadRequest("channel does not match this endpoint"), traceID)
		return
	}
	merchantID := principal.MerchantID
	if channel == domain.ChannelAdmin {
		merchantID = ""
	} else if merchantID == "" {
		apiresp.FromError(w, apperr.Forbidden("merchant id is required"), traceID)
		return
	}
	group, err := h.sodGroups.Create(r.Context(), app.CreateSodGroupCommand{
		Channel:        channel,
		MerchantID:     merchantID,
		Code:           strings.TrimSpace(req.Code),
		Name:           req.Name,
		Description:    req.Description,
		MaxActiveRoles: req.MaxActiveRoles,
		RoleIDs:        req.RoleIDs,
		Actor:          principal.SubjectID,
		Reason:         req.Reason,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSodGroupDTO(group), traceID)
}

// GetSodGroup GET /iam/sod-groups/{id}
func (h *Handler) GetSodGroup(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if h.sodGroups == nil {
		apiresp.FromError(w, apperr.Internal("iam sod group service is not configured", nil), traceID)
		return
	}
	channel, merchantID := h.channel, h.merchantScope(principal)
	if h.channel == domain.ChannelBiz && merchantID == "" {
		apiresp.FromError(w, apperr.Forbidden("merchant id is required"), traceID)
		return
	}
	group, err := h.sodGroups.Get(r.Context(), r.PathValue("id"), channel, merchantID)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSodGroupDTO(group), traceID)
}

// UpdateSodGroup PATCH /iam/sod-groups/{id}
func (h *Handler) UpdateSodGroup(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if h.sodGroups == nil {
		apiresp.FromError(w, apperr.Internal("iam sod group service is not configured", nil), traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionSodGroupUpdate) {
		return
	}
	var req UpdateSodGroupRequest
	if err := decodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	channel, merchantID := h.channel, h.merchantScope(principal)
	if h.channel == domain.ChannelBiz && merchantID == "" {
		apiresp.FromError(w, apperr.Forbidden("merchant id is required"), traceID)
		return
	}
	group, err := h.sodGroups.Update(r.Context(), app.UpdateSodGroupCommand{
		ID:             r.PathValue("id"),
		Channel:        channel,
		MerchantID:     merchantID,
		Name:           req.Name,
		Description:    req.Description,
		MaxActiveRoles: req.MaxActiveRoles,
		RoleIDs:        req.RoleIDs,
		Status:         req.Status,
		Version:        version,
		Actor:          principal.SubjectID,
		Reason:         req.Reason,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toSodGroupDTO(group), traceID)
}

// DeleteSodGroup DELETE /iam/sod-groups/{id}
func (h *Handler) DeleteSodGroup(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if h.sodGroups == nil {
		apiresp.FromError(w, apperr.Internal("iam sod group service is not configured", nil), traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionSodGroupDelete) {
		return
	}
	var req DeleteSodGroupRequest
	if err := decodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	channel, merchantID := h.channel, h.merchantScope(principal)
	if h.channel == domain.ChannelBiz && merchantID == "" {
		apiresp.FromError(w, apperr.Forbidden("merchant id is required"), traceID)
		return
	}
	if err := h.sodGroups.Delete(r.Context(), app.DeleteSodGroupCommand{
		ID:         r.PathValue("id"),
		Channel:    channel,
		MerchantID: merchantID,
		Version:    version,
		Actor:      principal.SubjectID,
		Reason:     req.Reason,
	}); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, map[string]string{"status": "deleted"}, traceID)
}

// ListSodGroupViolations GET /iam/sod-groups/{id}/violations
func (h *Handler) ListSodGroupViolations(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if h.sodGroups == nil {
		apiresp.FromError(w, apperr.Internal("iam sod group service is not configured", nil), traceID)
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			apiresp.FromError(w, apperr.BadRequest("limit must be an integer"), traceID)
			return
		}
		limit = parsed
	}
	channel, merchantID := h.channel, h.merchantScope(principal)
	if h.channel == domain.ChannelBiz && merchantID == "" {
		apiresp.FromError(w, apperr.Forbidden("merchant id is required"), traceID)
		return
	}
	violations, err := h.sodGroups.ListViolations(r.Context(), r.PathValue("id"), channel, merchantID, limit)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items := toSodViolationDTOs(violations)
	apiresp.OK(w, SodViolationListResponse{Items: items, Total: len(items)}, traceID)
}

// PreflightSodGroup POST /iam/sod-groups/preflight
func (h *Handler) PreflightSodGroup(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if h.sodGroups == nil {
		apiresp.FromError(w, apperr.Internal("iam sod group service is not configured", nil), traceID)
		return
	}
	var req PreflightSodGroupRequest
	if err := decodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	channel, merchantID := h.channel, h.merchantScope(principal)
	if h.channel == domain.ChannelBiz && merchantID == "" {
		apiresp.FromError(w, apperr.Forbidden("merchant id is required"), traceID)
		return
	}
	violations, err := h.sodGroups.Preflight(r.Context(), channel, merchantID, req.RoleIDs, req.MaxActiveRoles, 0)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items := toSodViolationDTOs(violations)
	apiresp.OK(w, SodViolationListResponse{Items: items, Total: len(items)}, traceID)
}
