package domain

import (
	"regexp"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
)

// MenuNodeType 表示菜单节点的渲染类型。
type MenuNodeType string

// 菜单节点类型取值。
const (
	// MenuNodeCatalog 目录节点：仅用于分组，必须有 default redirect。
	MenuNodeCatalog MenuNodeType = "catalog"
	// MenuNodePage 页面节点：对应一个前端组件。
	MenuNodePage MenuNodeType = "menu"
	// MenuNodeIFrame 内嵌页面：通过 external_url 渲染 iframe。
	MenuNodeIFrame MenuNodeType = "iframe"
	// MenuNodeLink 外链：点击后跳转到 external_url。
	MenuNodeLink MenuNodeType = "link"
)

// MenuStatus 表示菜单生命周期状态。
type MenuStatus string

// 菜单状态取值。
const (
	// MenuStatusActive 生效，参与菜单树输出。
	MenuStatusActive MenuStatus = "active"
	// MenuStatusDisabled 停用，不返回给前端。
	MenuStatusDisabled MenuStatus = "disabled"
)

// menuCodePattern 约束菜单编码：小写字母开头，允许下划线分段与点分层级。
var menuCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// routeNamePattern 约束路由 name：PascalCase，端内唯一。
var routeNamePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

// Menu 是前端菜单 / 路由资源聚合根。
//
// 菜单只描述「有哪些入口」，可见性由 PermissionCode 推导：
// 空值表示登录即可见；非空时由 app 层按主体快照裁剪。
// 菜单不存放授权关系，也不参与接口鉴权。
type Menu struct {
	// ID 菜单主键。
	ID string
	// Channel 归属端别：admin / biz。
	Channel Channel
	// Code 端内唯一的菜单编码。
	Code string
	// ParentCode 父菜单编码；根节点为空串。
	ParentCode string
	// NodeType 渲染类型。
	NodeType MenuNodeType
	// RouteName 路由 name，PascalCase。
	RouteName string
	// RoutePath 路由 URL；子页面可为相对段。
	RoutePath string
	// Component 组件标识；catalog / link 为空。
	Component string
	// Redirect 目录默认跳转。
	Redirect string
	// Title 显示名；i18n 缺失时兜底。
	Title string
	// TitleKey i18n key；非空时前端优先。
	TitleKey string
	// Icon 图标注册表 key。
	Icon string
	// PermissionCode 可见所需权限点；空串表示登录即可见。
	PermissionCode iam.PermissionCode
	// Sort 同级升序。
	Sort int
	// HideInMenu 路由可达但不进侧边栏。
	HideInMenu bool
	// KeepAlive 页面缓存。
	KeepAlive bool
	// ExternalURL iframe / link 目标地址。
	ExternalURL string
	// Status 生命周期状态。
	Status MenuStatus
	// Version 乐观锁版本；每次写入自增。
	Version int64
	// UpdatedBy 最后维护人。
	UpdatedBy string
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate 校验菜单自身字段与节点类型约束。
func (m *Menu) Validate() error {
	if m.Channel != ChannelAdmin && m.Channel != ChannelBiz {
		return errInvalid(ErrInvalidMenu, "menu %q has invalid channel %q", m.Code, m.Channel)
	}
	if m.Code == "" || len(m.Code) > 128 || !menuCodePattern.MatchString(m.Code) {
		return errInvalid(ErrInvalidMenu, "menu code %q must be lowercase dotted segments (e.g. system.role)", m.Code)
	}
	if m.ParentCode == m.Code {
		return errInvalid(ErrInvalidMenu, "menu %q must not be its own parent", m.Code)
	}
	switch m.NodeType {
	case MenuNodeCatalog, MenuNodePage, MenuNodeIFrame, MenuNodeLink:
	default:
		return errInvalid(ErrInvalidMenu, "menu %q has invalid node_type %q", m.Code, m.NodeType)
	}
	if m.RouteName == "" || len(m.RouteName) > 64 || !routeNamePattern.MatchString(m.RouteName) {
		return errInvalid(ErrInvalidMenu, "menu %q route_name %q must be PascalCase", m.Code, m.RouteName)
	}
	if strings.TrimSpace(m.RoutePath) == "" {
		return errInvalid(ErrInvalidMenu, "menu %q route_path is required", m.Code)
	}
	if strings.TrimSpace(m.Title) == "" {
		return errInvalid(ErrInvalidMenu, "menu %q title is required", m.Code)
	}
	switch m.NodeType {
	case MenuNodeCatalog:
		if strings.TrimSpace(m.Redirect) == "" {
			return errInvalid(ErrInvalidMenu, "catalog %q requires redirect", m.Code)
		}
		if strings.TrimSpace(m.Component) != "" || strings.TrimSpace(m.ExternalURL) != "" {
			return errInvalid(ErrInvalidMenu, "catalog %q must not set component or external_url", m.Code)
		}
		// 目录只做分组：挂权限码既不生效（有可见子节点时祖先补链会绕过，
		// 无可见子节点时整枝被裁），又容易误以为能按目录授权。
		if m.PermissionCode != "" {
			return errInvalid(ErrInvalidMenu, "catalog %q must not set permission_code; gate leaf pages instead", m.Code)
		}
	case MenuNodePage:
		if strings.TrimSpace(m.Component) == "" {
			return errInvalid(ErrInvalidMenu, "menu %q requires component", m.Code)
		}
		if strings.TrimSpace(m.ExternalURL) != "" {
			return errInvalid(ErrInvalidMenu, "menu %q must not set external_url", m.Code)
		}
	case MenuNodeIFrame:
		if strings.TrimSpace(m.Component) == "" || strings.TrimSpace(m.ExternalURL) == "" {
			return errInvalid(ErrInvalidMenu, "iframe %q requires component and external_url", m.Code)
		}
	case MenuNodeLink:
		if strings.TrimSpace(m.ExternalURL) == "" {
			return errInvalid(ErrInvalidMenu, "link %q requires external_url", m.Code)
		}
		if strings.TrimSpace(m.Component) != "" {
			return errInvalid(ErrInvalidMenu, "link %q must not set component", m.Code)
		}
	}
	if m.PermissionCode != "" {
		if err := ValidatePermissionCode(m.PermissionCode); err != nil {
			return err
		}
	}
	if m.Status != MenuStatusActive && m.Status != MenuStatusDisabled {
		return errInvalid(ErrInvalidMenu, "menu %q has invalid status %q", m.Code, m.Status)
	}
	if m.Sort < 0 {
		return errInvalid(ErrInvalidMenu, "menu %q sort must not be negative", m.Code)
	}
	return nil
}

// IsVisibleTo 判断菜单是否对给定权限集合可见。
//
// 仅做权限判定，不负责祖先补链；祖先补齐由 app 层完成。
func (m *Menu) IsVisibleTo(has func(iam.PermissionCode) bool) bool {
	return m.PermissionCode == "" || has(m.PermissionCode)
}

// NewMenuParams 是创建菜单的入参；状态固定为 active，版本从 1 开始。
type NewMenuParams struct {
	ID             string
	Channel        Channel
	Code           string
	ParentCode     string
	NodeType       MenuNodeType
	RouteName      string
	RoutePath      string
	Component      string
	Redirect       string
	Title          string
	TitleKey       string
	Icon           string
	PermissionCode iam.PermissionCode
	Sort           int
	HideInMenu     bool
	KeepAlive      bool
	ExternalURL    string
	Actor          string
	Now            time.Time
}

// NewMenu 构造并校验菜单；文本字段统一去首尾空白。
func NewMenu(p NewMenuParams) (*Menu, error) {
	if strings.TrimSpace(p.Actor) == "" {
		return nil, errInvalid(ErrInvalidMenu, "menu actor is required")
	}
	now := p.Now.UTC()
	menu := &Menu{
		ID:             strings.TrimSpace(p.ID),
		Channel:        p.Channel,
		Code:           strings.TrimSpace(p.Code),
		ParentCode:     strings.TrimSpace(p.ParentCode),
		NodeType:       p.NodeType,
		RouteName:      strings.TrimSpace(p.RouteName),
		RoutePath:      strings.TrimSpace(p.RoutePath),
		Component:      strings.TrimSpace(p.Component),
		Redirect:       strings.TrimSpace(p.Redirect),
		Title:          strings.TrimSpace(p.Title),
		TitleKey:       strings.TrimSpace(p.TitleKey),
		Icon:           strings.TrimSpace(p.Icon),
		PermissionCode: iam.PermissionCode(strings.TrimSpace(string(p.PermissionCode))),
		Sort:           p.Sort,
		HideInMenu:     p.HideInMenu,
		KeepAlive:      p.KeepAlive,
		ExternalURL:    strings.TrimSpace(p.ExternalURL),
		Status:         MenuStatusActive,
		Version:        1,
		UpdatedBy:      strings.TrimSpace(p.Actor),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if menu.ID == "" {
		return nil, errInvalid(ErrInvalidMenu, "menu id is required")
	}
	if err := menu.Validate(); err != nil {
		return nil, err
	}
	return menu, nil
}

// MenuUpdateParams 描述菜单的可修改字段；nil 表示保持不变。
//
// code / channel 创建后不可修改，因此不在本结构中；状态变更走单独用例。
type MenuUpdateParams struct {
	ParentCode     *string
	NodeType       *MenuNodeType
	RouteName      *string
	RoutePath      *string
	Component      *string
	Redirect       *string
	Title          *string
	TitleKey       *string
	Icon           *string
	PermissionCode *iam.PermissionCode
	Sort           *int
	HideInMenu     *bool
	KeepAlive      *bool
	ExternalURL    *string
}

// ApplyUpdate 就地合并可变字段；调用方随后必须执行 Validate。
func (m *Menu) ApplyUpdate(p MenuUpdateParams) {
	if p.ParentCode != nil {
		m.ParentCode = strings.TrimSpace(*p.ParentCode)
	}
	if p.NodeType != nil {
		m.NodeType = *p.NodeType
	}
	if p.RouteName != nil {
		m.RouteName = strings.TrimSpace(*p.RouteName)
	}
	if p.RoutePath != nil {
		m.RoutePath = strings.TrimSpace(*p.RoutePath)
	}
	if p.Component != nil {
		m.Component = strings.TrimSpace(*p.Component)
	}
	if p.Redirect != nil {
		m.Redirect = strings.TrimSpace(*p.Redirect)
	}
	if p.Title != nil {
		m.Title = strings.TrimSpace(*p.Title)
	}
	if p.TitleKey != nil {
		m.TitleKey = strings.TrimSpace(*p.TitleKey)
	}
	if p.Icon != nil {
		m.Icon = strings.TrimSpace(*p.Icon)
	}
	if p.PermissionCode != nil {
		m.PermissionCode = iam.PermissionCode(strings.TrimSpace(string(*p.PermissionCode)))
	}
	if p.Sort != nil {
		m.Sort = *p.Sort
	}
	if p.HideInMenu != nil {
		m.HideInMenu = *p.HideInMenu
	}
	if p.KeepAlive != nil {
		m.KeepAlive = *p.KeepAlive
	}
	if p.ExternalURL != nil {
		m.ExternalURL = strings.TrimSpace(*p.ExternalURL)
	}
}
