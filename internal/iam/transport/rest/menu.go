package rest

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MenuMetaDTO 是前端路由 meta，字段名与 vben 后端菜单契约一致。
type MenuMetaDTO struct {
	Title      string `json:"title"`
	TitleKey   string `json:"titleKey,omitempty"`
	Icon       string `json:"icon,omitempty"`
	Order      int    `json:"order"`
	HideInMenu bool   `json:"hideInMenu,omitempty"`
	KeepAlive  bool   `json:"keepAlive,omitempty"`
	IFrameSrc  string `json:"iframeSrc,omitempty"`
	Link       string `json:"link,omitempty"`
}

// MenuTreeNodeDTO 是菜单树节点响应。
type MenuTreeNodeDTO struct {
	Name      string             `json:"name"`
	Path      string             `json:"path"`
	Component string             `json:"component,omitempty"`
	Redirect  string             `json:"redirect,omitempty"`
	Meta      MenuMetaDTO        `json:"meta"`
	Children  []*MenuTreeNodeDTO `json:"children,omitempty"`
	Status    string             `json:"status"`
}

// Menus GET {channel}/v1/iam/me/menus
//
// 菜单只决定前端入口是否可见，接口安全边界仍是权限点中间件；
// 本端点仅需认证，不额外要求权限点。
func (h *Handler) Menus(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.menus == nil {
		apiresp.FromError(w, apperr.Internal("iam menus are not configured", nil), traceID)
		return
	}
	principal, err := h.principal(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	subjectType := domain.SubjectStaff
	if h.channel == domain.ChannelBiz {
		subjectType = domain.SubjectMerchantStaff
	}
	nodes, err := h.menus.Menus(r.Context(), app.MenuQuery{
		Channel:     h.channel,
		SubjectType: subjectType,
		SubjectID:   principal.SubjectID,
		MerchantID:  h.merchantScope(principal),
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, toMenuTreeDTOs(nodes), traceID)
}

// toMenuTreeDTOs 递归转换菜单树。
func toMenuTreeDTOs(nodes []*app.MenuNode) []*MenuTreeNodeDTO {
	out := make([]*MenuTreeNodeDTO, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, toMenuTreeDTO(node))
	}
	return out
}

// toMenuTreeDTO 转换单个菜单节点，并把节点类型映射为前端 meta 字段。
func toMenuTreeDTO(node *app.MenuNode) *MenuTreeNodeDTO {
	menu := node.Menu
	dto := &MenuTreeNodeDTO{
		Name:      menu.RouteName,
		Path:      menu.RoutePath,
		Component: menu.Component,
		// 目录跳转由 MenuService 按可见树重算，避免指向被裁掉的页面。
		Redirect: node.EffectiveRedirect,
		Meta: MenuMetaDTO{
			Title:      menu.Title,
			TitleKey:   menu.TitleKey,
			Icon:       menu.Icon,
			Order:      menu.Sort,
			HideInMenu: menu.HideInMenu,
			KeepAlive:  menu.KeepAlive,
		},
		Status: string(menu.Status),
	}
	switch menu.NodeType {
	case domain.MenuNodeIFrame:
		dto.Meta.IFrameSrc = menu.ExternalURL
	case domain.MenuNodeLink:
		dto.Meta.Link = menu.ExternalURL
	}
	if len(node.Children) > 0 {
		dto.Children = toMenuTreeDTOs(node.Children)
	}
	return dto
}
