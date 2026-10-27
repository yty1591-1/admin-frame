// Package rest 提供 A / B 端共用的 IAM HTTP 解析、DTO 转换与 Handler 实现。
//
// 通道差异（端别、权限点、租户来源）由 transport/admin 与 transport/biz 在
// 路由层注入，本包只负责「请求 → 用例 → 响应」的机械转换，避免两端各写一份
// 容易漂移的解析逻辑。
package rest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// HandlerConfig 是 A 端 IAM Handler 的装配参数。
//
// Channel 由 cmd 在装配期固定（本包同时被 A / B 端复用），transport 不接受
// 调用方传入的端别用于授权决策，避免在 HTTP 层被请求参数劫持（SEC-14）。
type HandlerConfig struct {
	Roles      *app.RoleService
	Bindings   *app.BindingService
	Query      *app.QueryService
	Changes    *app.ChangeRequestService
	Breakglass *app.BreakglassService
	// Templates 角色模板用例；为空时模板路由返回 50000（兼容仅启用角色管理的装配）。
	Templates *app.TemplateService
	// Menus 菜单用例；为空时菜单端点返回 50000。
	Menus *app.MenuService
	// MenuAdmin 菜单管理用例；为空时菜单管理端点返回 50000。
	MenuAdmin *app.MenuAdminService
	Channel   domain.Channel
	// RequireApproval 为 true 时拒绝角色 / 绑定的直写端点，强制走审批单
	// （iam.approval.enabled）。
	RequireApproval bool
}

// Handler 处理 IAM 管理端 HTTP 请求。
type Handler struct {
	roles           *app.RoleService
	bindings        *app.BindingService
	query           *app.QueryService
	changes         *app.ChangeRequestService
	breakglass      *app.BreakglassService
	templates       *app.TemplateService
	menus           *app.MenuService
	menuAdmin       *app.MenuAdminService
	channel         domain.Channel
	requireApproval bool
}

// NewHandler 构造 IAM Handler；缺失依赖时返回错误。
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Roles == nil || cfg.Bindings == nil || cfg.Query == nil {
		return nil, apperr.Internal("iam handler services are required", nil)
	}
	if cfg.Changes == nil {
		return nil, apperr.Internal("iam change request service is required", nil)
	}
	if err := cfg.Channel.Validate(); err != nil {
		return nil, err
	}
	return &Handler{
		roles:           cfg.Roles,
		bindings:        cfg.Bindings,
		query:           cfg.Query,
		changes:         cfg.Changes,
		breakglass:      cfg.Breakglass,
		templates:       cfg.Templates,
		menus:           cfg.Menus,
		menuAdmin:       cfg.MenuAdmin,
		channel:         cfg.Channel,
		requireApproval: cfg.RequireApproval,
	}, nil
}

// rejectDirectWrite 在「高风险直写强制转审批」开启时写出 40900 并返回 true。
//
// 只拦截 HTTP 直写；审批单 apply 走用例内部调用，不受影响。
func (h *Handler) rejectDirectWrite(w http.ResponseWriter, r *http.Request, action domain.ChangeAction) bool {
	if !h.requireApproval {
		return false
	}
	traceID := httpx.TraceIDFromContext(r.Context())
	base := "/admin/v1/iam"
	if h.channel == domain.ChannelBiz {
		base = "/biz/v1/iam"
	}
	httpx.FromError(w, apperr.Conflict(
		"high-risk change requires approval: submit action="+string(action)+" via POST "+base+"/change-requests",
	), traceID)
	return true
}

// ListPermissions GET /iam/permissions
func (h *Handler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	filter := app.PermissionFilter{
		Channel: domain.Channel(strings.TrimSpace(r.URL.Query().Get("channel"))),
		Keyword: r.URL.Query().Get("keyword"),
	}
	if h.channel == domain.ChannelBiz {
		// B 端只能看到本端可授予的权限点，避免把 A 端专属权限暴露给商户管理员。
		filter.Channel = domain.ChannelBiz
	}
	entries := h.query.Permissions(filter)
	total := len(entries)

	page, err := pagination.Parse(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	offset, limit := page.Offset(), page.Limit()
	if offset > len(entries) {
		offset = len(entries)
	}
	end := offset + limit
	if end > len(entries) {
		end = len(entries)
	}
	items := make([]*PermissionDTO, 0, end-offset)
	for _, entry := range entries[offset:end] {
		items = append(items, toPermissionDTO(entry))
	}
	httpx.OK(w, PermissionListResponse{Items: items, Total: total}, traceID)
}

// ListRoles GET /iam/roles
func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
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
	filter := domain.RoleFilter{
		Channel:    domain.Channel(strings.TrimSpace(query.Get("channel"))),
		MerchantID: strings.TrimSpace(query.Get("merchant_id")),
		Status:     domain.RoleStatus(strings.TrimSpace(query.Get("status"))),
		Keyword:    strings.TrimSpace(query.Get("keyword")),
		Limit:      page.Limit(),
		Offset:     page.Offset(),
	}
	if h.channel == domain.ChannelBiz {
		// B 端只能查看本商户角色，merchant_id 只从 Principal 取。
		if principal.MerchantID == "" {
			httpx.FromError(w, apperr.Forbidden("merchant id is required"), traceID)
			return
		}
		filter.Channel = domain.ChannelBiz
		filter.MerchantID = principal.MerchantID
	}
	roles, err := h.query.ListRoles(r.Context(), filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	total, err := h.query.CountRoles(r.Context(), filter)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items := make([]*RoleDTO, 0, len(roles))
	for _, role := range roles {
		items = append(items, toRoleDTO(role))
	}
	httpx.OK(w, RoleListResponse{Items: items, Total: total}, traceID)
}

// CreateRole POST /iam/roles
func (h *Handler) CreateRole(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionRoleCreate) {
		return
	}
	var req CreateRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	channel := h.effectiveChannel(req.Channel)
	if channel == "" {
		httpx.FromError(w, apperr.BadRequest("channel does not match this endpoint"), traceID)
		return
	}
	merchantID := principal.MerchantID
	if channel == domain.ChannelAdmin {
		merchantID = ""
	}
	scopes, err := toDataScopes(req.DataScopes)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	role, err := h.roles.CreateRole(r.Context(), app.CreateRoleCommand{
		Channel:     channel,
		Scope:       domain.RoleScopeType(strings.TrimSpace(req.ScopeType)),
		MerchantID:  merchantID,
		ParentID:    strings.TrimSpace(req.ParentID),
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
	httpx.OK(w, toRoleDTO(role), traceID)
}

// GetRole GET /iam/roles/{id}
func (h *Handler) GetRole(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	detail, err := h.query.GetRole(r.Context(), r.PathValue("id"), h.channel, h.merchantScope(principal))
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	dto := toRoleDTO(detail.Role)
	dto.BindingCount = detail.BindingCount
	httpx.OK(w, dto, traceID)
}

// UpdateRole PATCH /iam/roles/{id}
func (h *Handler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionRoleUpdate) {
		return
	}
	var req UpdateRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	role, err := h.roles.UpdateRole(r.Context(), app.UpdateRoleCommand{
		ID:          r.PathValue("id"),
		Name:        req.Name,
		Description: req.Description,
		Status:      req.Status,
		ParentID:    req.ParentID,
		Version:     version,
		Actor:       principal.SubjectID,
		Reason:      req.Reason,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toRoleDTO(role), traceID)
}

// ReplacePermissions PUT /iam/roles/{id}/permissions
func (h *Handler) ReplacePermissions(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionRolePermissionReplace) {
		return
	}
	var req ReplacePermissionsRequest
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
	role, change, err := h.roles.ReplacePermissions(r.Context(), app.ReplacePermissionsCommand{
		ID:          r.PathValue("id"),
		Permissions: toPermissionCodes(req.Permissions),
		DataScopes:  scopes,
		Version:     version,
		Actor:       principal.SubjectID,
		Reason:      req.Reason,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	dto := toRoleDTO(role)
	httpx.OK(w, ReplacePermissionsResponse{
		Role:    dto,
		Added:   permissionCodeStrings(change.Added),
		Removed: permissionCodeStrings(change.Removed),
	}, traceID)
}

// CloneRole POST /iam/roles/{id}/clone
func (h *Handler) CloneRole(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionRoleClone) {
		return
	}
	var req CloneRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	role, err := h.roles.CloneRole(r.Context(), app.CloneRoleCommand{
		SourceID: r.PathValue("id"),
		Code:     req.Code,
		Name:     req.Name,
		Actor:    principal.SubjectID,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toRoleDTO(role), traceID)
}

// DeleteRole DELETE /iam/roles/{id}
func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionRoleDelete) {
		return
	}
	var req DeleteRoleRequest
	// DELETE 允许空 body（version 由 If-Match 提供）。
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
	if err := h.roles.DeleteRole(r.Context(), app.DeleteRoleCommand{
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

// ListBindings GET /iam/bindings?subject_type=&subject_id=&role_id=
func (h *Handler) ListBindings(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	roleID := strings.TrimSpace(query.Get("role_id"))
	var bindings []*domain.Binding
	if roleID != "" {
		bindings, err = h.query.ListBindingsByRole(r.Context(), roleID)
	} else {
		subjectQuery, buildErr := h.subjectQuery(query.Get("subject_type"), query.Get("subject_id"), principal)
		if buildErr != nil {
			httpx.FromError(w, buildErr, traceID)
			return
		}
		bindings, err = h.query.ListBindings(r.Context(), subjectQuery)
	}
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items := make([]*BindingDTO, 0, len(bindings))
	for _, binding := range bindings {
		if h.channel == domain.ChannelBiz && binding.MerchantID != principal.MerchantID {
			continue
		}
		items = append(items, toBindingDTO(binding))
	}
	httpx.OK(w, BindingListResponse{Items: items, Total: len(items)}, traceID)
}

// CreateBinding POST /iam/bindings
func (h *Handler) CreateBinding(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionBindingCreate) {
		return
	}
	var req CreateBindingRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if strings.TrimSpace(req.RoleID) == "" {
		httpx.FromError(w, apperr.BadRequest("role_id is required"), traceID)
		return
	}
	binding, err := h.bindings.Bind(r.Context(), app.BindCommand{
		Channel:     h.channel,
		SubjectType: domain.SubjectType(strings.TrimSpace(req.SubjectType)),
		SubjectID:   strings.TrimSpace(req.SubjectID),
		MerchantID:  h.merchantScope(principal),
		RoleID:      req.RoleID,
		ExpiresAt:   req.ExpiresAt,
		Actor:       principal.SubjectID,
		Reason:      req.Reason,
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toBindingDTO(binding), traceID)
}

// DeleteBinding DELETE /iam/bindings/{id}
func (h *Handler) DeleteBinding(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.rejectDirectWrite(w, r, domain.ChangeActionBindingDelete) {
		return
	}
	var req UnbindRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			httpx.FromError(w, err, traceID)
			return
		}
	}
	if err := h.bindings.Unbind(r.Context(), app.UnbindCommand{
		ID:         r.PathValue("id"),
		MerchantID: h.merchantScope(principal),
		Actor:      principal.SubjectID,
		Reason:     req.Reason,
	}); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, map[string]string{"id": r.PathValue("id")}, traceID)
}

// SubjectPermissions GET /iam/subjects/{subject_type}/{subject_id}/permissions
func (h *Handler) SubjectPermissions(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	subjectQuery, err := h.subjectQuery(r.PathValue("subject_type"), r.PathValue("subject_id"), principal)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	snapshot, err := h.query.SubjectSnapshot(r.Context(), subjectQuery)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toSubjectPermissionsResponse(snapshot), traceID)
}

// MePermissions GET /iam/me/permissions
func (h *Handler) MePermissions(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	subjectType := domain.SubjectStaff
	if h.channel == domain.ChannelBiz {
		subjectType = domain.SubjectMerchantStaff
	}
	snapshot, err := h.query.SubjectSnapshot(r.Context(), domain.SubjectQuery{
		Channel:     h.channel,
		SubjectType: subjectType,
		SubjectID:   principal.SubjectID,
		MerchantID:  h.merchantScope(principal),
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toSubjectPermissionsResponse(snapshot), traceID)
}

// Authorize POST /iam/authorize
func (h *Handler) Authorize(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	var req AuthorizeRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	subjectQuery, err := h.subjectQuery(req.SubjectType, req.SubjectID, principal)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	result, err := h.query.Simulate(r.Context(), app.SimulateQuery{
		Channel:     subjectQuery.Channel,
		SubjectType: subjectQuery.SubjectType,
		SubjectID:   subjectQuery.SubjectID,
		MerchantID:  subjectQuery.MerchantID,
		Permission:  iam.PermissionCode(strings.TrimSpace(req.Permission)),
	})
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	resp := AuthorizeResponse{
		Allowed:         result.Allowed,
		Reason:          result.Reason,
		MatchedRoles:    result.MatchedRoles,
		SnapshotVersion: result.SnapshotVersion,
	}
	if result.DataScope != nil {
		scope := toDataScopeDTO(*result.DataScope)
		resp.DataScope = &scope
	}
	httpx.OK(w, resp, traceID)
}

// principal 读取认证主体；缺失时返回 40100。
func (h *Handler) principal(r *http.Request) (auth.Principal, error) {
	principal, ok := auth.FromContext(r.Context())
	if !ok || strings.TrimSpace(principal.SubjectID) == "" {
		return auth.Principal{}, apperr.Unauthorized("authentication required")
	}
	if principal.Channel != auth.Channel(h.channel) {
		return auth.Principal{}, apperr.Unauthorized("channel mismatch")
	}
	return principal, nil
}

// merchantScope 返回本次请求的商户作用域：A 端恒为空，B 端取自 Principal。
func (h *Handler) merchantScope(principal auth.Principal) string {
	if h.channel == domain.ChannelBiz {
		return principal.MerchantID
	}
	return ""
}

// effectiveChannel 解析并校验请求体端别；空串按本 transport 端别处理。
func (h *Handler) effectiveChannel(requested string) domain.Channel {
	channel := domain.Channel(strings.TrimSpace(requested))
	if channel == "" {
		return h.channel
	}
	if channel != h.channel {
		return ""
	}
	return channel
}

// subjectQuery 构造主体查询；B 端强制使用 Principal 的商户，忽略请求参数。
func (h *Handler) subjectQuery(subjectType, subjectID string, principal auth.Principal) (domain.SubjectQuery, error) {
	subjectType = strings.TrimSpace(subjectType)
	subjectID = strings.TrimSpace(subjectID)
	if subjectType == "" || subjectID == "" {
		return domain.SubjectQuery{}, apperr.BadRequest("subject_type and subject_id are required")
	}
	query := domain.SubjectQuery{
		Channel:     h.channel,
		SubjectType: domain.SubjectType(subjectType),
		SubjectID:   subjectID,
		MerchantID:  h.merchantScope(principal),
	}
	if h.channel == domain.ChannelBiz && query.MerchantID == "" {
		return domain.SubjectQuery{}, apperr.Forbidden("merchant id is required")
	}
	return query, nil
}

// decodeJSON 解码请求体并拒绝未知字段，避免拼写错误被静默忽略。
func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return apperr.BadRequest("invalid json body")
	}
	return nil
}

// versionFrom 解析写入版本：body.version 与 If-Match 二选一，冲突返回 40000。
func versionFrom(r *http.Request, bodyVersion int64) (int64, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		if bodyVersion <= 0 {
			return 0, apperr.BadRequest("version is required (body.version or If-Match)")
		}
		return bodyVersion, nil
	}
	raw = strings.Trim(raw, `"`)
	headerVersion, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || headerVersion <= 0 {
		return 0, apperr.BadRequest("invalid If-Match header")
	}
	if bodyVersion > 0 && bodyVersion != headerVersion {
		return 0, apperr.BadRequest("If-Match and version do not match")
	}
	return headerVersion, nil
}
