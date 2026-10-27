package rest

import (
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// RoleTemplateDTO 是角色模板响应（含权限与数据范围基线）。
type RoleTemplateDTO struct {
	ID          string         `json:"id"`
	Channel     string         `json:"channel"`
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Status      string         `json:"status"`
	Version     int64          `json:"version"`
	Permissions []string       `json:"permissions"`
	DataScopes  []DataScopeDTO `json:"data_scopes"`
	CreatedBy   string         `json:"created_by,omitempty"`
	UpdatedBy   string         `json:"updated_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// RoleTemplateListResponse 是模板列表响应。
type RoleTemplateListResponse struct {
	Items []*RoleTemplateDTO `json:"items"`
	Total int                `json:"total"`
}

// CreateRoleTemplateRequest 是创建模板请求体。
type CreateRoleTemplateRequest struct {
	Channel     string         `json:"channel"`
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Permissions []string       `json:"permissions"`
	DataScopes  []DataScopeDTO `json:"data_scopes"`
}

// UpdateRoleTemplateRequest 是修改模板请求体；nil 字段表示不修改。
type UpdateRoleTemplateRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	Version     int64   `json:"version"`
}

// ReplaceTemplatePermissionsRequest 是全量替换模板基线请求体。
type ReplaceTemplatePermissionsRequest struct {
	Permissions []string        `json:"permissions"`
	DataScopes  *[]DataScopeDTO `json:"data_scopes"`
	Version     int64           `json:"version"`
}

// InstantiateTemplateRequest 是按模板实例化角色请求体。
type InstantiateTemplateRequest struct {
	Channel   string `json:"channel"`
	ScopeType string `json:"scope_type"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Reason    string `json:"reason"`
}

// TemplateDriftDTO 是模板漂移实例。
type TemplateDriftDTO struct {
	RoleID          string `json:"role_id"`
	RoleCode        string `json:"role_code"`
	RoleVersion     int64  `json:"role_version"`
	TemplateVersion int64  `json:"template_version"`
}

// SyncTemplateRequest 是同步模板请求体。
type SyncTemplateRequest struct {
	DryRun bool   `json:"dry_run"`
	Reason string `json:"reason"`
}

// SyncTemplateResponse 是同步结果：返回被同步（或待同步）的实例。
type SyncTemplateResponse struct {
	Items []*TemplateDriftDTO `json:"items"`
	Total int                 `json:"total"`
}

// ListRoleTemplates GET /iam/role-templates
func (h *Handler) ListRoleTemplates(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.templates == nil {
		httpx.FromError(w, apperr.Internal("iam role templates are not configured", nil), traceID)
		return
	}
	page, err := pagination.Parse(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	filter := domain.RoleTemplateFilter{
		Channel: domain.Channel(strings.TrimSpace(query.Get("channel"))),
		Status:  domain.RoleTemplateStatus(strings.TrimSpace(query.Get("status"))),
		Keyword: strings.TrimSpace(query.Get("keyword")),
		Limit:   page.Limit(),
		Offset:  page.Offset(),
	}
	if h.channel == domain.ChannelBiz {
		// B 端只能看到本端模板：跨端模板会把 A 端专属权限基线暴露给商户。
		filter.Channel = domain.ChannelBiz
	}
	templates, err := h.templates.ListTemplates(r.Context(), filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	total, err := h.templates.CountTemplates(r.Context(), filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items := make([]*RoleTemplateDTO, 0, len(templates))
	for _, template := range templates {
		items = append(items, toRoleTemplateDTO(template))
	}
	httpx.OK(w, RoleTemplateListResponse{Items: items, Total: total}, traceID)
}

// CreateRoleTemplate POST /iam/role-templates
func (h *Handler) CreateRoleTemplate(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.templates == nil {
		httpx.FromError(w, apperr.Internal("iam role templates are not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req CreateRoleTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	channel := h.templateChannel(req.Channel)
	if channel == "" {
		httpx.FromError(w, apperr.BadRequest("channel does not match this endpoint"), traceID)
		return
	}
	scopes, err := toDataScopes(req.DataScopes)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	template, err := h.templates.CreateTemplate(r.Context(), app.CreateTemplateCommand{
		Channel:     channel,
		Code:        req.Code,
		Name:        req.Name,
		Description: req.Description,
		Permissions: toPermissionCodes(req.Permissions),
		DataScopes:  scopes,
		Actor:       principal.SubjectID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toRoleTemplateDTO(template), traceID)
}

// GetRoleTemplate GET /iam/role-templates/{id}
func (h *Handler) GetRoleTemplate(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.templates == nil {
		httpx.FromError(w, apperr.Internal("iam role templates are not configured", nil), traceID)
		return
	}
	template, err := h.templates.GetTemplate(r.Context(), r.PathValue("id"), h.templateVisibilityChannel())
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toRoleTemplateDTO(template), traceID)
}

// UpdateRoleTemplate PATCH /iam/role-templates/{id}
func (h *Handler) UpdateRoleTemplate(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.templates == nil {
		httpx.FromError(w, apperr.Internal("iam role templates are not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req UpdateRoleTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	template, err := h.templates.UpdateTemplate(r.Context(), app.UpdateTemplateCommand{
		ID:          r.PathValue("id"),
		Name:        req.Name,
		Description: req.Description,
		Status:      req.Status,
		Version:     version,
		Actor:       principal.SubjectID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toRoleTemplateDTO(template), traceID)
}

// ReplaceRoleTemplatePermissions PUT /iam/role-templates/{id}/permissions
func (h *Handler) ReplaceRoleTemplatePermissions(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.templates == nil {
		httpx.FromError(w, apperr.Internal("iam role templates are not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req ReplaceTemplatePermissionsRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var scopes *[]domain.DataScope
	if req.DataScopes != nil {
		converted, convErr := toDataScopes(*req.DataScopes)
		if convErr != nil {
			httpx.FromError(w, convErr, traceID)
			return
		}
		scopes = &converted
	}
	template, err := h.templates.ReplaceTemplatePermissions(r.Context(), app.ReplaceTemplatePermissionsCommand{
		ID:          r.PathValue("id"),
		Permissions: toPermissionCodes(req.Permissions),
		DataScopes:  scopes,
		Version:     version,
		Actor:       principal.SubjectID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toRoleTemplateDTO(template), traceID)
}

// DeleteRoleTemplate DELETE /iam/role-templates/{id}
func (h *Handler) DeleteRoleTemplate(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.templates == nil {
		httpx.FromError(w, apperr.Internal("iam role templates are not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req DeleteRoleRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			httpx.FromError(w, err, traceID)
			return
		}
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if err := h.templates.DeleteTemplate(r.Context(), app.DeleteTemplateCommand{
		ID:      r.PathValue("id"),
		Version: version,
		Actor:   principal.SubjectID,
		Reason:  req.Reason,
	}); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, map[string]string{"id": r.PathValue("id")}, traceID)
}

// GetTemplateDrift GET /iam/role-templates/{id}/drift
func (h *Handler) GetTemplateDrift(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.templates == nil {
		httpx.FromError(w, apperr.Internal("iam role templates are not configured", nil), traceID)
		return
	}
	drift, err := h.templates.Drift(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, SyncTemplateResponse{Items: toTemplateDriftDTOs(drift), Total: len(drift)}, traceID)
}

// SyncRoleTemplate POST /iam/role-templates/{id}/sync
func (h *Handler) SyncRoleTemplate(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.templates == nil {
		httpx.FromError(w, apperr.Internal("iam role templates are not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req SyncTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	drift, err := h.templates.SyncTemplate(r.Context(), app.SyncTemplateCommand{
		TemplateID: r.PathValue("id"),
		Actor:      principal.SubjectID,
		Reason:     req.Reason,
		DryRun:     req.DryRun,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, SyncTemplateResponse{Items: toTemplateDriftDTOs(drift), Total: len(drift)}, traceID)
}

// InstantiateRoleTemplate POST /iam/role-templates/{id}/instantiate
func (h *Handler) InstantiateRoleTemplate(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	if h.templates == nil {
		httpx.FromError(w, apperr.Internal("iam role templates are not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req InstantiateTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	channel := h.effectiveChannel(req.Channel)
	if channel == "" {
		httpx.FromError(w, apperr.BadRequest("channel does not match this endpoint"), traceID)
		return
	}
	scope := domain.RoleScopeType(strings.TrimSpace(req.ScopeType))
	merchantID := h.merchantScope(principal)
	if channel == domain.ChannelBiz {
		// B 端只能实例化本商户的 merchant 角色，载荷不能把它提升为 platform。
		scope = domain.RoleScopeMerchant
	} else if scope == "" {
		scope = domain.RoleScopePlatform
		merchantID = ""
	}
	role, err := h.templates.Instantiate(r.Context(), app.InstantiateTemplateCommand{
		TemplateID: r.PathValue("id"),
		Channel:    channel,
		Scope:      scope,
		MerchantID: merchantID,
		Code:       req.Code,
		Name:       req.Name,
		Actor:      principal.SubjectID,
		Reason:     req.Reason,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toRoleDTO(role), traceID)
}

// templateChannel 解析模板管理请求的端别。
//
// A 端平台管理两端模板（平台定义、商户实例化的来源），默认本端但允许显式指定；
// B 端只能维护 / 实例化本端模板，请求体端别一律被覆盖，防止越端读取平台基线。
func (h *Handler) templateChannel(requested string) domain.Channel {
	channel := domain.Channel(strings.TrimSpace(requested))
	if h.channel == domain.ChannelBiz {
		return domain.ChannelBiz
	}
	if channel == "" {
		return h.channel
	}
	if channel != domain.ChannelAdmin && channel != domain.ChannelBiz {
		return ""
	}
	return channel
}

// templateVisibilityChannel 返回模板详情可见的端别：A 端可看两端，B 端仅本端。
func (h *Handler) templateVisibilityChannel() domain.Channel {
	if h.channel == domain.ChannelBiz {
		return domain.ChannelBiz
	}
	return ""
}

// toRoleTemplateDTO 转换模板聚合。
func toRoleTemplateDTO(template *domain.RoleTemplate) *RoleTemplateDTO {
	codes := template.PermissionCodes()
	permissions := make([]string, 0, len(codes))
	for _, code := range codes {
		permissions = append(permissions, string(code))
	}
	return &RoleTemplateDTO{
		ID:          template.ID,
		Channel:     string(template.Channel),
		Code:        template.Code,
		Name:        template.Name,
		Description: template.Description,
		Status:      string(template.Status),
		Version:     template.Version,
		Permissions: permissions,
		DataScopes:  toDataScopeDTOs(template.SortedDataScopes()),
		CreatedBy:   template.CreatedBy,
		UpdatedBy:   template.UpdatedBy,
		CreatedAt:   template.CreatedAt,
		UpdatedAt:   template.UpdatedAt,
	}
}

// toTemplateDriftDTOs 转换漂移列表。
func toTemplateDriftDTOs(items []app.TemplateDrift) []*TemplateDriftDTO {
	out := make([]*TemplateDriftDTO, 0, len(items))
	for _, item := range items {
		out = append(out, &TemplateDriftDTO{
			RoleID:          item.RoleID,
			RoleCode:        item.RoleCode,
			RoleVersion:     item.RoleVersion,
			TemplateVersion: item.TemplateVersion,
		})
	}
	return out
}
