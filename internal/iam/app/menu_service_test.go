package app_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
)

// menuSnapshotStub 是返回固定权限集的快照仓储。
type menuSnapshotStub struct {
	codes []iam.PermissionCode
}

// Load 实现 domain.SnapshotRepository。
func (s menuSnapshotStub) Load(_ context.Context, query domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
	return &domain.SubjectSnapshot{
		Channel:     query.Channel,
		SubjectType: query.SubjectType,
		SubjectID:   query.SubjectID,
		MerchantID:  query.MerchantID,
		Permissions: s.codes,
	}, nil
}

// menuFixture 构造一棵 A 端菜单：system 目录下挂角色页与审计页，另有一个空目录。
func menuFixture(t *testing.T) []*domain.Menu {
	t.Helper()
	menus := []*domain.Menu{
		{
			ID: "1", Channel: domain.ChannelAdmin, Code: "system", NodeType: domain.MenuNodeCatalog,
			RouteName: "System", RoutePath: "/system", Redirect: "/system/role",
			Title: "系统管理", Sort: 10, Status: domain.MenuStatusActive,
		},
		{
			ID: "2", Channel: domain.ChannelAdmin, Code: "system.role", ParentCode: "system",
			NodeType: domain.MenuNodePage, RouteName: "SystemRole", RoutePath: "role",
			Component: "/system/role/index.vue", Title: "角色管理",
			PermissionCode: iam.PermRoleRead, Sort: 10, Status: domain.MenuStatusActive,
		},
		{
			ID: "3", Channel: domain.ChannelAdmin, Code: "system.audit", ParentCode: "system",
			NodeType: domain.MenuNodePage, RouteName: "SystemAudit", RoutePath: "audit",
			Component: "/system/audit/index.vue", Title: "审计日志",
			PermissionCode: iam.PermAuditRead, Sort: 20, Status: domain.MenuStatusActive,
		},
		{
			ID: "4", Channel: domain.ChannelAdmin, Code: "empty", NodeType: domain.MenuNodeCatalog,
			RouteName: "Empty", RoutePath: "/empty", Redirect: "/empty",
			Title: "空目录", Sort: 20, Status: domain.MenuStatusActive,
		},
	}
	for _, menu := range menus {
		if err := menu.Validate(); err != nil {
			t.Fatalf("fixture 菜单非法: %v", err)
		}
	}
	return menus
}

// newMenuService 用固定权限集装配菜单用例。
func newMenuService(t *testing.T, codes ...iam.PermissionCode) *app.MenuService {
	t.Helper()
	return newMenuServiceWith(t, menuFixture(t), codes...)
}

// newMenuServiceWith 用自定义菜单与权限集装配菜单用例。
func newMenuServiceWith(t *testing.T, menus []*domain.Menu, codes ...iam.PermissionCode) *app.MenuService {
	t.Helper()
	svc, err := app.NewMenuService(
		persistence.NewMemoryMenuRepository(menus...),
		menuSnapshotStub{codes: codes},
	)
	if err != nil {
		t.Fatalf("装配菜单用例失败: %v", err)
	}
	return svc
}

func queryAdminMenus(t *testing.T, svc *app.MenuService) []*app.MenuNode {
	t.Helper()
	nodes, err := svc.Menus(context.Background(), app.MenuQuery{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "staff-1",
	})
	if err != nil {
		t.Fatalf("查询菜单失败: %v", err)
	}
	return nodes
}

// TestMenuServiceNoPermissionReturnsEmpty 没有任何权限时，只有权限门禁的目录整枝丢弃。
func TestMenuServiceNoPermissionReturnsEmpty(t *testing.T) {
	if nodes := queryAdminMenus(t, newMenuService(t)); len(nodes) != 0 {
		t.Fatalf("期望空菜单，实际得到 %d 个根节点", len(nodes))
	}
}

// TestMenuServiceAddsAncestorChain 单页面权限必须自动补齐父目录。
func TestMenuServiceAddsAncestorChain(t *testing.T) {
	nodes := queryAdminMenus(t, newMenuService(t, iam.PermRoleRead))
	if len(nodes) != 1 || nodes[0].Menu.Code != "system" {
		t.Fatalf("期望唯一根目录 system，实际 %+v", nodes)
	}
	if len(nodes[0].Children) != 1 || nodes[0].Children[0].Menu.Code != "system.role" {
		t.Fatalf("期望 system 下只有 system.role，实际 %+v", nodes[0].Children)
	}
}

// TestMenuServicePrunesEmptyCatalog 无可见子节点的目录不得返回。
func TestMenuServicePrunesEmptyCatalog(t *testing.T) {
	nodes := queryAdminMenus(t, newMenuService(t, iam.PermRoleRead, iam.PermAuditRead))
	for _, node := range nodes {
		if node.Menu.Code == "empty" {
			t.Fatal("空目录不应出现在菜单树中")
		}
	}
	if len(nodes[0].Children) != 2 {
		t.Fatalf("期望 system 下有两个页面，实际 %d", len(nodes[0].Children))
	}
}

// TestMenuServiceSeparatesChannels 菜单按端别隔离，A 端查询不返回 B 端菜单。
func TestMenuServiceSeparatesChannels(t *testing.T) {
	nodes, err := newMenuService(t, iam.PermRoleRead).Menus(context.Background(), app.MenuQuery{
		Channel:     domain.ChannelBiz,
		SubjectType: domain.SubjectMerchantStaff,
		SubjectID:   "staff-1",
		MerchantID:  "m-1",
	})
	if err != nil {
		t.Fatalf("查询 B 端菜单失败: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("B 端不应看到 A 端菜单，实际 %+v", nodes)
	}
}

// TestMenuServiceRecomputesCatalogRedirect 目录跳转必须指向首个可见页面，
// 而不是配置中那个可能已被权限裁剪的页面。
func TestMenuServiceRecomputesCatalogRedirect(t *testing.T) {
	nodes := queryAdminMenus(t, newMenuService(t, iam.PermAuditRead))
	if len(nodes) != 1 || nodes[0].Menu.Code != "system" {
		t.Fatalf("期望唯一根目录 system，实际 %+v", nodes)
	}
	if len(nodes[0].Children) != 1 || nodes[0].Children[0].Menu.Code != "system.audit" {
		t.Fatalf("期望 system 下只有 system.audit，实际 %+v", nodes[0].Children)
	}
	if nodes[0].EffectiveRedirect != "/system/audit" {
		t.Fatalf("redirect = %q，期望 /system/audit（/system/role 已因缺少权限被裁剪）", nodes[0].EffectiveRedirect)
	}
}

// TestMenuServiceNestedCatalogRedirect 三级目录链上每层 redirect 都落到首个可见页面。
func TestMenuServiceNestedCatalogRedirect(t *testing.T) {
	menus := []*domain.Menu{
		{
			ID: "1", Channel: domain.ChannelAdmin, Code: "system", NodeType: domain.MenuNodeCatalog,
			RouteName: "System", RoutePath: "/system", Redirect: "/system/role",
			Title: "系统管理", Sort: 10, Status: domain.MenuStatusActive,
		},
		{
			ID: "2", Channel: domain.ChannelAdmin, Code: "system.role", ParentCode: "system",
			NodeType: domain.MenuNodePage, RouteName: "SystemRole", RoutePath: "role",
			Component: "/system/role/index.vue", Title: "角色管理",
			PermissionCode: iam.PermRoleRead, Sort: 10, Status: domain.MenuStatusActive,
		},
		{
			ID: "3", Channel: domain.ChannelAdmin, Code: "system.iam", ParentCode: "system",
			NodeType: domain.MenuNodeCatalog, RouteName: "SystemIam", RoutePath: "iam",
			Redirect: "/system/iam/permission", Title: "权限管理", Sort: 20, Status: domain.MenuStatusActive,
		},
		{
			ID: "4", Channel: domain.ChannelAdmin, Code: "system.iam.permission", ParentCode: "system.iam",
			NodeType: domain.MenuNodePage, RouteName: "SystemIamPermission", RoutePath: "permission",
			Component: "/system/role/index.vue", Title: "权限目录",
			PermissionCode: iam.PermPermissionRead, Sort: 10, Status: domain.MenuStatusActive,
		},
	}
	for _, menu := range menus {
		if err := menu.Validate(); err != nil {
			t.Fatalf("fixture 菜单非法: %v", err)
		}
	}

	nodes := queryAdminMenus(t, newMenuServiceWith(t, menus, iam.PermPermissionRead))
	if len(nodes) != 1 || nodes[0].Menu.Code != "system" {
		t.Fatalf("期望唯一根目录 system，实际 %+v", nodes)
	}
	if len(nodes[0].Children) != 1 || nodes[0].Children[0].Menu.Code != "system.iam" {
		t.Fatalf("期望 system 下只有 system.iam，实际 %+v", nodes[0].Children)
	}
	if nodes[0].EffectiveRedirect != "/system/iam/permission" {
		t.Fatalf("一级 redirect = %q，期望 /system/iam/permission", nodes[0].EffectiveRedirect)
	}
	iamNode := nodes[0].Children[0]
	if iamNode.EffectiveRedirect != "/system/iam/permission" {
		t.Fatalf("二级 redirect = %q，期望 /system/iam/permission", iamNode.EffectiveRedirect)
	}
	if len(iamNode.Children) != 1 || iamNode.Children[0].Menu.Code != "system.iam.permission" {
		t.Fatalf("期望 system.iam 下只有 system.iam.permission，实际 %+v", iamNode.Children)
	}
}
