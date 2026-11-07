package rest

import (
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// MenuAdminDTO 是菜单管理视图的响应项。
type MenuAdminDTO struct {
	ID             string    `json:"id"`
	Channel        string    `json:"channel"`
	Code           string    `json:"code"`
	ParentCode     string    `json:"parent_code,omitempty"`
	NodeType       string    `json:"node_type"`
	RouteName      string    `json:"route_name"`
	RoutePath      string    `json:"route_path"`
	Component      string    `json:"component,omitempty"`
	Redirect       string    `json:"redirect,omitempty"`
	Title          string    `json:"title"`
	TitleKey       string    `json:"title_key,omitempty"`
	Icon           string    `json:"icon,omitempty"`
	PermissionCode string    `json:"permission_code,omitempty"`
	Sort           int       `json:"sort"`
	HideInMenu     bool      `json:"hide_in_menu"`
	KeepAlive      bool      `json:"keep_alive"`
	ExternalURL    string    `json:"external_url,omitempty"`
	Status         string    `json:"status"`
	Version        int64     `json:"version"`
	UpdatedBy      string    `json:"updated_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// MenuAdminListResponse 是菜单管理列表响应。
type MenuAdminListResponse struct {
	Items []*MenuAdminDTO `json:"items"`
	Total int             `json:"total"`
}

// MenuAdminTreeNode 是管理视图树节点。
type MenuAdminTreeNode struct {
	MenuAdminDTO
	Children []*MenuAdminTreeNode `json:"children,omitempty"`
}

// CreateMenuRequest 是创建菜单请求体。
type CreateMenuRequest struct {
	Channel        string `json:"channel"`
	Code           string `json:"code"`
	ParentCode     string `json:"parent_code"`
	NodeType       string `json:"node_type"`
	RouteName      string `json:"route_name"`
	RoutePath      string `json:"route_path"`
	Component      string `json:"component"`
	Redirect       string `json:"redirect"`
	Title          string `json:"title"`
	TitleKey       string `json:"title_key"`
	Icon           string `json:"icon"`
	PermissionCode string `json:"permission_code"`
	Sort           int    `json:"sort"`
	HideInMenu     bool   `json:"hide_in_menu"`
	KeepAlive      bool   `json:"keep_alive"`
	ExternalURL    string `json:"external_url"`
	Reason         string `json:"reason"`
}

// UpdateMenuRequest 是 PATCH 菜单请求体；nil 字段表示不修改。
type UpdateMenuRequest struct {
	ParentCode     *string `json:"parent_code"`
	NodeType       *string `json:"node_type"`
	RouteName      *string `json:"route_name"`
	RoutePath      *string `json:"route_path"`
	Component      *string `json:"component"`
	Redirect       *string `json:"redirect"`
	Title          *string `json:"title"`
	TitleKey       *string `json:"title_key"`
	Icon           *string `json:"icon"`
	PermissionCode *string `json:"permission_code"`
	Sort           *int    `json:"sort"`
	HideInMenu     *bool   `json:"hide_in_menu"`
	KeepAlive      *bool   `json:"keep_alive"`
	ExternalURL    *string `json:"external_url"`
	Version        int64   `json:"version"`
	Reason         string  `json:"reason"`
}

// MenuStatusRequest 是启用 / 停用请求体；停用也允许用 If-Match 提供版本。
type MenuStatusRequest struct {
	Version int64  `json:"version"`
	Reason  string `json:"reason"`
}

// ListMenus GET /admin/v1/iam/menus
func (h *Handler) ListMenus(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.menuAdmin == nil {
		apiresp.FromError(w, apperr.Internal("iam menu admin is not configured", nil), traceID)
		return
	}
	page, err := pagination.Parse(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	menus, total, err := h.menuAdmin.List(r.Context(), app.MenuFilter{
		Channel:    menuChannel(query.Get("channel")),
		Status:     domain.MenuStatus(strings.TrimSpace(query.Get("status"))),
		ParentCode: strings.TrimSpace(query.Get("parent_code")),
		Keyword:    query.Get("keyword"),
		Limit:      page.Limit(),
		Offset:     page.Offset(),
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	items := make([]*MenuAdminDTO, 0, len(menus))
	for _, menu := range menus {
		items = append(items, toMenuAdminDTO(menu))
	}
	apiresp.OK(w, MenuAdminListResponse{Items: items, Total: total}, traceID)
}

// GetMenu GET /admin/v1/iam/menus/{id}
func (h *Handler) GetMenu(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.menuAdmin == nil {
		apiresp.FromError(w, apperr.Internal("iam menu admin is not configured", nil), traceID)
		return
	}
	menu, err := h.menuAdmin.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toMenuAdminDTO(menu), traceID)
}

// CreateMenu POST /admin/v1/iam/menus
func (h *Handler) CreateMenu(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.menuAdmin == nil {
		apiresp.FromError(w, apperr.Internal("iam menu admin is not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var req CreateMenuRequest
	if err := decodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	menu, err := h.menuAdmin.CreateMenu(r.Context(), app.CreateMenuCommand{
		Channel:        menuChannel(req.Channel),
		Code:           req.Code,
		ParentCode:     req.ParentCode,
		NodeType:       domain.MenuNodeType(strings.TrimSpace(req.NodeType)),
		RouteName:      req.RouteName,
		RoutePath:      req.RoutePath,
		Component:      req.Component,
		Redirect:       req.Redirect,
		Title:          req.Title,
		TitleKey:       req.TitleKey,
		Icon:           req.Icon,
		PermissionCode: iam.PermissionCode(strings.TrimSpace(req.PermissionCode)),
		Sort:           req.Sort,
		HideInMenu:     req.HideInMenu,
		KeepAlive:      req.KeepAlive,
		ExternalURL:    req.ExternalURL,
		Actor:          principal.SubjectID,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toMenuAdminDTO(menu), traceID)
}

// UpdateMenu PATCH /admin/v1/iam/menus/{id}
func (h *Handler) UpdateMenu(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.menuAdmin == nil {
		apiresp.FromError(w, apperr.Internal("iam menu admin is not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var req UpdateMenuRequest
	if err := decodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	fields := domain.MenuUpdateParams{
		ParentCode:  req.ParentCode,
		RouteName:   req.RouteName,
		RoutePath:   req.RoutePath,
		Component:   req.Component,
		Redirect:    req.Redirect,
		Title:       req.Title,
		TitleKey:    req.TitleKey,
		Icon:        req.Icon,
		Sort:        req.Sort,
		HideInMenu:  req.HideInMenu,
		KeepAlive:   req.KeepAlive,
		ExternalURL: req.ExternalURL,
	}
	if req.NodeType != nil {
		nodeType := domain.MenuNodeType(strings.TrimSpace(*req.NodeType))
		fields.NodeType = &nodeType
	}
	if req.PermissionCode != nil {
		code := iam.PermissionCode(strings.TrimSpace(*req.PermissionCode))
		fields.PermissionCode = &code
	}
	menu, err := h.menuAdmin.UpdateMenu(r.Context(), app.UpdateMenuCommand{
		ID:      r.PathValue("id"),
		Fields:  fields,
		Version: version,
		Actor:   principal.SubjectID,
		Reason:  req.Reason,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toMenuAdminDTO(menu), traceID)
}

// DisableMenu DELETE /admin/v1/iam/menus/{id}
//
// 语义是停用而不是物理删除：保留审计与父子引用，恢复走 enable。
func (h *Handler) DisableMenu(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.menuAdmin == nil {
		apiresp.FromError(w, apperr.Internal("iam menu admin is not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var req MenuStatusRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			apiresp.FromError(w, err, traceID)
			return
		}
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	menu, err := h.menuAdmin.DisableMenu(r.Context(), app.DisableMenuCommand{
		ID:      r.PathValue("id"),
		Version: version,
		Actor:   principal.SubjectID,
		Reason:  req.Reason,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toMenuAdminDTO(menu), traceID)
}

// EnableMenu POST /admin/v1/iam/menus/{id}/enable
func (h *Handler) EnableMenu(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.menuAdmin == nil {
		apiresp.FromError(w, apperr.Internal("iam menu admin is not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var req MenuStatusRequest
	if err := decodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	version, err := versionFrom(r, req.Version)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	menu, err := h.menuAdmin.EnableMenu(r.Context(), app.EnableMenuCommand{
		ID:      r.PathValue("id"),
		Version: version,
		Actor:   principal.SubjectID,
		Reason:  req.Reason,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toMenuAdminDTO(menu), traceID)
}

// MenuTree GET /admin/v1/iam/menus/tree
func (h *Handler) MenuTree(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.menuAdmin == nil {
		apiresp.FromError(w, apperr.Internal("iam menu admin is not configured", nil), traceID)
		return
	}
	query := r.URL.Query()
	includeDisabled := strings.EqualFold(strings.TrimSpace(query.Get("include_disabled")), "true")
	nodes, err := h.menuAdmin.Tree(r.Context(), menuChannel(query.Get("channel")), includeDisabled)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toMenuAdminTreeDTOs(nodes), traceID)
}

// MenuComponents GET /admin/v1/iam/menus/components
func (h *Handler) MenuComponents(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.menuAdmin == nil {
		apiresp.FromError(w, apperr.Internal("iam menu admin is not configured", nil), traceID)
		return
	}
	components, err := h.menuAdmin.Components(menuChannel(r.URL.Query().Get("channel")))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, components, traceID)
}

// menuChannel 把查询 / 请求体中的端别归一为 admin；缺省也按 admin 处理。
func menuChannel(raw string) domain.Channel {
	channel := strings.TrimSpace(raw)
	if channel == "" {
		return domain.ChannelAdmin
	}
	return domain.Channel(channel)
}

// toMenuAdminDTO 转换单条菜单。
func toMenuAdminDTO(menu *domain.Menu) *MenuAdminDTO {
	return &MenuAdminDTO{
		ID:             menu.ID,
		Channel:        string(menu.Channel),
		Code:           menu.Code,
		ParentCode:     menu.ParentCode,
		NodeType:       string(menu.NodeType),
		RouteName:      menu.RouteName,
		RoutePath:      menu.RoutePath,
		Component:      menu.Component,
		Redirect:       menu.Redirect,
		Title:          menu.Title,
		TitleKey:       menu.TitleKey,
		Icon:           menu.Icon,
		PermissionCode: string(menu.PermissionCode),
		Sort:           menu.Sort,
		HideInMenu:     menu.HideInMenu,
		KeepAlive:      menu.KeepAlive,
		ExternalURL:    menu.ExternalURL,
		Status:         string(menu.Status),
		Version:        menu.Version,
		UpdatedBy:      menu.UpdatedBy,
		CreatedAt:      menu.CreatedAt,
		UpdatedAt:      menu.UpdatedAt,
	}
}

// toMenuAdminTreeDTOs 递归转换管理树。
func toMenuAdminTreeDTOs(nodes []*app.MenuAdminNode) []*MenuAdminTreeNode {
	out := make([]*MenuAdminTreeNode, 0, len(nodes))
	for _, node := range nodes {
		item := &MenuAdminTreeNode{MenuAdminDTO: *toMenuAdminDTO(node.Menu)}
		item.Children = toMenuAdminTreeDTOs(node.Children)
		out = append(out, item)
	}
	return out
}
