package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/catalog"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// opCreateMenu / opUpdateMenu / opDisableMenu / opEnableMenu 是菜单审计的 operation 取值。
const (
	opCreateMenu  = "create"
	opUpdateMenu  = "update"
	opDisableMenu = "disable"
	opEnableMenu  = "enable"
)

// maxMenuDepth 限制菜单树深度，避免前端递归渲染与面包屑出现异常层级。
const maxMenuDepth = 5

// ComponentCatalog 是菜单可引用页面组件的校验端口。
type ComponentCatalog interface {
	// Allows 判断指定端别是否允许引用该 component。
	Allows(channel domain.Channel, component string) bool
	// Components 返回指定端别允许的 component 清单。
	Components(channel domain.Channel) []string
}

// MenuAdminService 是菜单资源的 A 端管理用例。
//
// 菜单只影响前端入口，不是鉴权边界；所有写入必须携带 version（乐观锁）并审计。
type MenuAdminService struct {
	menus      domain.MenuRepository
	recorder   ChangeRecorder
	components ComponentCatalog
	uow        UnitOfWork
	ids        id.Generator
	audit      auditBuilder
	log        *slog.Logger
}

// MenuAdminConfig 是 MenuAdminService 的装配参数。
type MenuAdminConfig struct {
	Menus      domain.MenuRepository
	Recorder   ChangeRecorder
	Components ComponentCatalog
	UnitOfWork UnitOfWork
	IDs        id.Generator
	Audit      AuditMeta
	Logger     *slog.Logger
}

// NewMenuAdminService 构造菜单管理用例；缺失必要依赖时返回错误（fail-closed）。
func NewMenuAdminService(cfg MenuAdminConfig) (*MenuAdminService, error) {
	if cfg.Menus == nil || cfg.Recorder == nil || cfg.Components == nil || cfg.IDs == nil {
		return nil, apperr.Internal("iam menu admin dependencies are required", nil)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	uow := cfg.UnitOfWork
	if uow == nil {
		uow = NoopUnitOfWork{}
	}
	return &MenuAdminService{
		menus:      cfg.Menus,
		recorder:   cfg.Recorder,
		components: cfg.Components,
		uow:        uow,
		ids:        cfg.IDs,
		audit:      newAuditBuilder(cfg.IDs, cfg.Audit),
		log:        logger,
	}, nil
}

// CreateMenuCommand 是创建菜单的入参。
type CreateMenuCommand struct {
	Channel        domain.Channel
	Code           string
	ParentCode     string
	NodeType       domain.MenuNodeType
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
}

// CreateMenu 创建 active 菜单并写审计。
func (s *MenuAdminService) CreateMenu(ctx context.Context, cmd CreateMenuCommand) (*domain.Menu, error) {
	if err := requireAdminChannel(cmd.Channel); err != nil {
		return nil, err
	}
	menuID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate menu id", err)
	}
	menu, err := domain.NewMenu(domain.NewMenuParams{
		ID:             menuID,
		Channel:        cmd.Channel,
		Code:           cmd.Code,
		ParentCode:     cmd.ParentCode,
		NodeType:       cmd.NodeType,
		RouteName:      cmd.RouteName,
		RoutePath:      cmd.RoutePath,
		Component:      cmd.Component,
		Redirect:       cmd.Redirect,
		Title:          cmd.Title,
		TitleKey:       cmd.TitleKey,
		Icon:           cmd.Icon,
		PermissionCode: cmd.PermissionCode,
		Sort:           cmd.Sort,
		HideInMenu:     cmd.HideInMenu,
		KeepAlive:      cmd.KeepAlive,
		ExternalURL:    cmd.ExternalURL,
		Actor:          cmd.Actor,
		Now:            time.Now(),
	})
	if err != nil {
		return nil, err
	}
	menus, err := s.menus.List(ctx, cmd.Channel, "")
	if err != nil {
		return nil, err
	}
	if err := s.validateRelations(menu, menus, true); err != nil {
		return nil, err
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.menus.Create(ctx, menu); err != nil {
			return err
		}
		return s.recordMenu(ctx, menu, opCreateMenu, cmd.Actor, "")
	}); err != nil {
		return nil, err
	}
	return menu, nil
}

// UpdateMenuCommand 是更新菜单的入参；Fields 中 nil 表示不修改。
type UpdateMenuCommand struct {
	ID      string
	Fields  domain.MenuUpdateParams
	Version int64
	Actor   string
	Reason  string
}

// UpdateMenu 以乐观锁更新菜单；code / channel 不可修改。
func (s *MenuAdminService) UpdateMenu(ctx context.Context, cmd UpdateMenuCommand) (*domain.Menu, error) {
	if cmd.Version <= 0 {
		return nil, apperr.BadRequest("version is required")
	}
	if strings.TrimSpace(cmd.Actor) == "" {
		return nil, apperr.BadRequest("actor is required")
	}
	menu, err := s.menus.Get(ctx, cmd.ID)
	if err != nil {
		return nil, err
	}
	before := *menu
	menu.ApplyUpdate(cmd.Fields)
	menu.UpdatedBy = strings.TrimSpace(cmd.Actor)
	if err := menu.Validate(); err != nil {
		return nil, err
	}
	menus, err := s.menus.List(ctx, menu.Channel, "")
	if err != nil {
		return nil, err
	}
	if err := s.validateRelations(menu, menus, false); err != nil {
		return nil, err
	}
	menu.Version = cmd.Version + 1
	menu.UpdatedAt = time.Now().UTC()
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.menus.Update(ctx, menu, cmd.Version); err != nil {
			return err
		}
		return s.recordMenu(ctx, menu, opUpdateMenu, cmd.Actor, cmd.Reason, changedMenuFields(&before, menu)...)
	}); err != nil {
		return nil, err
	}
	return menu, nil
}

// DisableMenuCommand 是停用菜单的入参。
type DisableMenuCommand struct {
	ID      string
	Version int64
	Actor   string
	Reason  string
}

// DisableMenu 停用菜单；存在启用子节点时拒绝，避免前端树断裂。
func (s *MenuAdminService) DisableMenu(ctx context.Context, cmd DisableMenuCommand) (*domain.Menu, error) {
	menu, err := s.loadForWrite(ctx, cmd.ID, cmd.Version, cmd.Actor)
	if err != nil {
		return nil, err
	}
	if menu.Status == domain.MenuStatusDisabled {
		return menu, nil
	}
	menus, err := s.menus.List(ctx, menu.Channel, "")
	if err != nil {
		return nil, err
	}
	for _, candidate := range menus {
		if candidate.ParentCode == menu.Code && candidate.Status == domain.MenuStatusActive {
			return nil, apperr.Conflict("menu has active children; move or disable them first")
		}
	}
	menu.Status = domain.MenuStatusDisabled
	return s.saveStatus(ctx, menu, cmd.Version, opDisableMenu, cmd.Actor, cmd.Reason)
}

// EnableMenuCommand 是启用菜单的入参。
type EnableMenuCommand struct {
	ID      string
	Version int64
	Actor   string
	Reason  string
}

// EnableMenu 重新启用菜单；父菜单必须处于启用状态，否则树会出现孤立节点。
func (s *MenuAdminService) EnableMenu(ctx context.Context, cmd EnableMenuCommand) (*domain.Menu, error) {
	menu, err := s.loadForWrite(ctx, cmd.ID, cmd.Version, cmd.Actor)
	if err != nil {
		return nil, err
	}
	if menu.Status == domain.MenuStatusActive {
		return menu, nil
	}
	if menu.ParentCode != "" {
		parent, parentErr := s.findByCode(ctx, menu.Channel, menu.ParentCode)
		if parentErr != nil {
			return nil, parentErr
		}
		if parent.Status != domain.MenuStatusActive {
			return nil, apperr.Conflict("parent menu is disabled; enable it first")
		}
	}
	menu.Status = domain.MenuStatusActive
	return s.saveStatus(ctx, menu, cmd.Version, opEnableMenu, cmd.Actor, cmd.Reason)
}

// MenuFilter 是菜单管理列表的过滤条件。
type MenuFilter struct {
	Channel    domain.Channel
	Status     domain.MenuStatus
	ParentCode string
	Keyword    string
	Limit      int
	Offset     int
}

// List 返回管理视图的平铺列表与总数，按 sort、code 升序。
func (s *MenuAdminService) List(ctx context.Context, filter MenuFilter) ([]*domain.Menu, int, error) {
	if err := requireAdminChannel(filter.Channel); err != nil {
		return nil, 0, err
	}
	menus, err := s.menus.List(ctx, filter.Channel, filter.Status)
	if err != nil {
		return nil, 0, err
	}
	keyword := strings.ToLower(strings.TrimSpace(filter.Keyword))
	filtered := make([]*domain.Menu, 0, len(menus))
	for _, menu := range menus {
		if filter.ParentCode != "" && menu.ParentCode != filter.ParentCode {
			continue
		}
		if keyword != "" && !menuMatchesKeyword(menu, keyword) {
			continue
		}
		filtered = append(filtered, menu)
	}
	total := len(filtered)
	offset := filter.Offset
	if offset > total {
		offset = total
	}
	end := offset + filter.Limit
	if filter.Limit <= 0 || end > total {
		end = total
	}
	return filtered[offset:end], total, nil
}

// Get 返回单条菜单详情。
func (s *MenuAdminService) Get(ctx context.Context, menuID string) (*domain.Menu, error) {
	return s.menus.Get(ctx, menuID)
}

// MenuAdminNode 是管理树节点。
type MenuAdminNode struct {
	Menu     *domain.Menu
	Children []*MenuAdminNode
}

// Tree 返回管理视图菜单树；includeDisabled 为 true 时包含停用节点。
func (s *MenuAdminService) Tree(ctx context.Context, channel domain.Channel, includeDisabled bool) ([]*MenuAdminNode, error) {
	if err := requireAdminChannel(channel); err != nil {
		return nil, err
	}
	status := domain.MenuStatusActive
	if includeDisabled {
		status = ""
	}
	menus, err := s.menus.List(ctx, channel, status)
	if err != nil {
		return nil, err
	}
	nodes := make(map[string]*MenuAdminNode, len(menus))
	for _, menu := range menus {
		nodes[menu.Code] = &MenuAdminNode{Menu: menu}
	}
	roots := make([]*MenuAdminNode, 0, len(nodes))
	for _, menu := range menus {
		node := nodes[menu.Code]
		if menu.ParentCode == "" {
			roots = append(roots, node)
			continue
		}
		if parent, ok := nodes[menu.ParentCode]; ok {
			parent.Children = append(parent.Children, node)
			continue
		}
		roots = append(roots, node)
	}
	return roots, nil
}

// Components 返回指定端别可引用的前端页面组件清单。
func (s *MenuAdminService) Components(channel domain.Channel) ([]string, error) {
	if err := requireAdminChannel(channel); err != nil {
		return nil, err
	}
	return s.components.Components(channel), nil
}

// loadForWrite 加载菜单并校验版本与操作人。
func (s *MenuAdminService) loadForWrite(ctx context.Context, menuID string, version int64, actor string) (*domain.Menu, error) {
	if version <= 0 {
		return nil, apperr.BadRequest("version is required")
	}
	if strings.TrimSpace(actor) == "" {
		return nil, apperr.BadRequest("actor is required")
	}
	menu, err := s.menus.Get(ctx, menuID)
	if err != nil {
		return nil, err
	}
	if err := requireAdminChannel(menu.Channel); err != nil {
		return nil, err
	}
	return menu, nil
}

// saveStatus 更新状态并写审计。
func (s *MenuAdminService) saveStatus(ctx context.Context, menu *domain.Menu, expectedVersion int64, operation, actor, reason string) (*domain.Menu, error) {
	menu.Version = expectedVersion + 1
	menu.UpdatedBy = strings.TrimSpace(actor)
	menu.UpdatedAt = time.Now().UTC()
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.menus.Update(ctx, menu, expectedVersion); err != nil {
			return err
		}
		return s.recordMenu(ctx, menu, operation, actor, reason)
	}); err != nil {
		return nil, err
	}
	return menu, nil
}

// recordMenu 写菜单变更审计；失败即失败关闭。
func (s *MenuAdminService) recordMenu(ctx context.Context, menu *domain.Menu, operation, actor, reason string, extra ...string) error {
	attrs := map[string]string{
		AttrOperation:     operation,
		"menu_code":       menu.Code,
		"node_type":       string(menu.NodeType),
		"route_name":      menu.RouteName,
		"permission_code": string(menu.PermissionCode),
	}
	if len(extra) > 0 {
		attrs["changed"] = strings.Join(extra, ",")
	}
	if err := s.audit.record(ctx, s.recorder, auditParams{
		action:       audit.ActionRBACMenuChange,
		actor:        strings.TrimSpace(actor),
		channel:      string(menu.Channel),
		resourceType: "iam_menu",
		resourceID:   menu.ID,
		outcome:      audit.OutcomeSuccess,
		reason:       strings.TrimSpace(reason),
		attrs:        attrs,
	}); err != nil {
		s.log.ErrorContext(ctx, "record menu audit", "menu_id", menu.ID, "err", err)
		return err
	}
	return nil
}

// validateRelations 校验父节点、成环、深度、唯一性与权限码 / 组件可授予性。
func (s *MenuAdminService) validateRelations(menu *domain.Menu, menus []*domain.Menu, creating bool) error {
	for _, existing := range menus {
		if creating && existing.Code == menu.Code {
			return apperr.Conflict("menu code already exists in this channel")
		}
		if existing.ID != menu.ID && existing.RouteName == menu.RouteName {
			return apperr.Conflict("menu route name already exists in this channel")
		}
	}
	if menu.ParentCode != "" {
		byCode := make(map[string]*domain.Menu, len(menus))
		for _, existing := range menus {
			byCode[existing.Code] = existing
		}
		parent, ok := byCode[menu.ParentCode]
		if !ok {
			return apperr.BadRequest("parent menu does not exist in this channel")
		}
		if parent.Status != domain.MenuStatusActive {
			return apperr.BadRequest("parent menu is disabled")
		}
		depth := 1
		cursor := menu.ParentCode
		visited := map[string]struct{}{menu.Code: {}}
		for cursor != "" {
			if _, seen := visited[cursor]; seen {
				return apperr.BadRequest("menu parent chain contains a cycle")
			}
			visited[cursor] = struct{}{}
			parent, ok := byCode[cursor]
			if !ok {
				break
			}
			depth++
			if depth > maxMenuDepth {
				return apperr.BadRequest("menu depth exceeds limit")
			}
			cursor = parent.ParentCode
		}
	}
	if !creating && (menu.NodeType == domain.MenuNodeLink || menu.NodeType == domain.MenuNodeIFrame) {
		for _, existing := range menus {
			if existing.ParentCode == menu.Code {
				return apperr.BadRequest("menu with children cannot be converted to link or iframe")
			}
		}
	}
	if err := validateMenuPermission(menu.Channel, menu.PermissionCode); err != nil {
		return err
	}
	switch menu.NodeType {
	case domain.MenuNodePage:
		if !s.components.Allows(menu.Channel, menu.Component) {
			return apperr.BadRequest("component is not registered in this channel")
		}
	case domain.MenuNodeIFrame:
		if menu.Component != "IFrameView" {
			return apperr.BadRequest("iframe menu component must be IFrameView")
		}
	}
	return nil
}

// findByCode 在同一端别内按编码查找菜单。
func (s *MenuAdminService) findByCode(ctx context.Context, channel domain.Channel, code string) (*domain.Menu, error) {
	menus, err := s.menus.List(ctx, channel, "")
	if err != nil {
		return nil, err
	}
	for _, menu := range menus {
		if menu.Code == code {
			return menu, nil
		}
	}
	return nil, apperr.BadRequest("parent menu does not exist in this channel")
}

// requireAdminChannel 拒绝非 A 端菜单写入。
func requireAdminChannel(channel domain.Channel) error {
	if channel != domain.ChannelAdmin {
		return apperr.BadRequest("menu management is only available on the admin channel")
	}
	return nil
}

// validateMenuPermission 校验 permission_code 存在且可在本端授予，且未弃用。
func validateMenuPermission(channel domain.Channel, code iam.PermissionCode) error {
	if code == "" {
		return nil
	}
	entry, ok := catalog.ByCode(code)
	if !ok {
		return apperr.BadRequest("permission code does not exist in catalog")
	}
	if entry.Status == catalog.StatusDeprecated {
		return apperr.BadRequest("permission code is deprecated and cannot gate a new menu")
	}
	if entry.Channel != catalog.ChannelBoth && string(entry.Channel) != string(channel) {
		return apperr.BadRequest("permission code is not grantable in this channel")
	}
	return nil
}

// menuMatchesKeyword 按 code / title / route_path 做大小写不敏感匹配。
func menuMatchesKeyword(menu *domain.Menu, keyword string) bool {
	return strings.Contains(strings.ToLower(menu.Code), keyword) ||
		strings.Contains(strings.ToLower(menu.Title), keyword) ||
		strings.Contains(strings.ToLower(menu.RoutePath), keyword)
}

// changedMenuFields 返回发生变化的字段名，用于审计。
func changedMenuFields(before, after *domain.Menu) []string {
	changed := make([]string, 0, 8)
	if before.ParentCode != after.ParentCode {
		changed = append(changed, "parent_code")
	}
	if before.NodeType != after.NodeType {
		changed = append(changed, "node_type")
	}
	if before.RouteName != after.RouteName {
		changed = append(changed, "route_name")
	}
	if before.RoutePath != after.RoutePath {
		changed = append(changed, "route_path")
	}
	if before.Component != after.Component {
		changed = append(changed, "component")
	}
	if before.Redirect != after.Redirect {
		changed = append(changed, "redirect")
	}
	if before.Title != after.Title || before.TitleKey != after.TitleKey || before.Icon != after.Icon {
		changed = append(changed, "display")
	}
	if before.PermissionCode != after.PermissionCode {
		changed = append(changed, "permission_code")
	}
	if before.Sort != after.Sort {
		changed = append(changed, "sort")
	}
	if before.HideInMenu != after.HideInMenu || before.KeepAlive != after.KeepAlive {
		changed = append(changed, "flags")
	}
	if before.ExternalURL != after.ExternalURL {
		changed = append(changed, "external_url")
	}
	return changed
}
