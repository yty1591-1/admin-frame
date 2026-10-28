package app

import (
	"context"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MenuService 是按主体权限裁剪菜单树的只读用例。
//
// 安全边界仍是接口权限码：菜单裁剪只决定前端入口是否展示，
// 不能替代接口鉴权。
type MenuService struct {
	menus     domain.MenuRepository
	snapshots domain.SnapshotRepository
}

// NewMenuService 构造菜单用例；依赖缺失时返回错误。
func NewMenuService(menus domain.MenuRepository, snapshots domain.SnapshotRepository) (*MenuService, error) {
	if menus == nil || snapshots == nil {
		return nil, apperr.Internal("iam menu repositories are required", nil)
	}
	return &MenuService{menus: menus, snapshots: snapshots}, nil
}

// MenuQuery 是菜单查询条件。
type MenuQuery struct {
	Channel     domain.Channel
	SubjectType domain.SubjectType
	SubjectID   string
	MerchantID  string
}

// MenuNode 是裁剪后的菜单树节点。
type MenuNode struct {
	Menu *domain.Menu
	// EffectiveRedirect 是目录节点的实际跳转：首个可见后代的完整路由路径。
	//
	// 配置的 Menu.Redirect 可能指向已被权限裁剪的页面，前端点击目录会落到
	// 无权限路由，因此输出前按可见树重算；非目录节点等于 Menu.Redirect。
	EffectiveRedirect string
	Children          []*MenuNode
}

// Menus 返回指定端别下、当前主体可见的菜单树。
//
// 裁剪规则：
//  1. 只保留 permission_code 为空或主体已持有的节点；
//  2. 递归补齐可见节点的祖先链，否则子页面可见而父目录缺失，前端拼不出树；
//  3. 目录节点裁剪后没有可见子节点则整枝丢弃；
//  4. disabled 节点不加载，输出按 sort、code 升序；
//  5. 目录的 redirect 重算为首个可见后代的完整路径，避免跳向被裁剪的页面。
func (s *MenuService) Menus(ctx context.Context, query MenuQuery) ([]*MenuNode, error) {
	snapshot, err := s.snapshots.Load(ctx, domain.SubjectQuery{
		Channel:     query.Channel,
		SubjectType: query.SubjectType,
		SubjectID:   query.SubjectID,
		MerchantID:  query.MerchantID,
	})
	if err != nil {
		return nil, err
	}

	menus, err := s.menus.List(ctx, query.Channel, domain.MenuStatusActive)
	if err != nil {
		return nil, err
	}

	byCode := make(map[string]*domain.Menu, len(menus))
	for _, menu := range menus {
		byCode[menu.Code] = menu
	}

	visible := make(map[string]struct{}, len(menus))
	for _, menu := range menus {
		if menu.IsVisibleTo(snapshot.HasPermission) {
			visible[menu.Code] = struct{}{}
		}
	}
	// 祖先补齐：只要子节点可见，父目录就必须可见（父目录自身的权限点不再重复判定）。
	for _, menu := range menus {
		if _, ok := visible[menu.Code]; !ok {
			continue
		}
		cursor := menu
		for cursor.ParentCode != "" {
			parent, ok := byCode[cursor.ParentCode]
			if !ok {
				break
			}
			visible[parent.Code] = struct{}{}
			cursor = parent
		}
	}

	nodes := make(map[string]*MenuNode, len(visible))
	for code := range visible {
		nodes[code] = &MenuNode{Menu: byCode[code]}
	}
	roots := make([]*MenuNode, 0, len(nodes))
	for _, menu := range menus {
		node, ok := nodes[menu.Code]
		if !ok {
			continue
		}
		if menu.ParentCode == "" {
			roots = append(roots, node)
			continue
		}
		if parent, ok := nodes[menu.ParentCode]; ok {
			parent.Children = append(parent.Children, node)
			continue
		}
		// 父节点不存在或已被停用：按根节点处理，避免可见页面整枝丢失。
		roots = append(roots, node)
	}
	roots = pruneEmptyCatalogs(roots)
	applyEffectiveRedirects(roots, "")
	return roots, nil
}

// applyEffectiveRedirects 为目录节点计算实际跳转。
//
// 静态 redirect 可能指向已被权限裁剪的页面；这里改为取首个可见后代的完整路径。
func applyEffectiveRedirects(nodes []*MenuNode, parentPath string) {
	for _, node := range nodes {
		fullPath := joinRoutePath(parentPath, node.Menu.RoutePath)
		node.EffectiveRedirect = node.Menu.Redirect
		if node.Menu.NodeType == domain.MenuNodeCatalog {
			if target := firstVisiblePath(node, fullPath); target != "" {
				node.EffectiveRedirect = target
			}
		}
		applyEffectiveRedirects(node.Children, fullPath)
	}
}

// firstVisiblePath 返回节点自身或首个可见后代的完整路径；无可见后代时返回空串。
func firstVisiblePath(node *MenuNode, fullPath string) string {
	if node.Menu.NodeType != domain.MenuNodeCatalog {
		return fullPath
	}
	for _, child := range node.Children {
		if path := firstVisiblePath(child, joinRoutePath(fullPath, child.Menu.RoutePath)); path != "" {
			return path
		}
	}
	return ""
}

// joinRoutePath 拼接父子路由路径；子路径为绝对路径时直接使用。
func joinRoutePath(parentPath, routePath string) string {
	if parentPath == "" || strings.HasPrefix(routePath, "/") {
		return routePath
	}
	return strings.TrimSuffix(parentPath, "/") + "/" + routePath
}

// pruneEmptyCatalogs 递归丢弃没有可见子节点的目录节点。
func pruneEmptyCatalogs(nodes []*MenuNode) []*MenuNode {
	kept := make([]*MenuNode, 0, len(nodes))
	for _, node := range nodes {
		node.Children = pruneEmptyCatalogs(node.Children)
		if node.Menu.NodeType == domain.MenuNodeCatalog && len(node.Children) == 0 {
			continue
		}
		kept = append(kept, node)
	}
	return kept
}
