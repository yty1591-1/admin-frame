package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// allowedComponents 是测试用组件清单。
type allowedComponents struct{}

func (allowedComponents) Allows(channel domain.Channel, component string) bool {
	return channel == domain.ChannelAdmin && component == "/system/role/index.vue"
}

func (allowedComponents) Components(channel domain.Channel) []string {
	if channel != domain.ChannelAdmin {
		return nil
	}
	return []string{"/system/role/index.vue"}
}

// adminMenuFixture 构造带版本号的 A 端菜单基线。
func adminMenuFixture() []*domain.Menu {
	return []*domain.Menu{
		{
			ID: "100", Channel: domain.ChannelAdmin, Code: "system", NodeType: domain.MenuNodeCatalog,
			RouteName: "System", RoutePath: "/system", Redirect: "/system/role",
			Title: "系统管理", Sort: 10, Status: domain.MenuStatusActive, Version: 1,
		},
		{
			ID: "200", Channel: domain.ChannelAdmin, Code: "system.role", ParentCode: "system",
			NodeType: domain.MenuNodePage, RouteName: "SystemRole", RoutePath: "role",
			Component: "/system/role/index.vue", Title: "角色管理",
			PermissionCode: iam.PermRoleRead, Sort: 10, Status: domain.MenuStatusActive, Version: 1,
		},
	}
}

// newMenuAdminFixture 装配内存仓储与捕获式审计。
func newMenuAdminFixture(t *testing.T) (*app.MenuAdminService, *persistence.MemoryMenuRepository, *capturedRecorder) {
	t.Helper()
	repo := persistence.NewMemoryMenuRepository(adminMenuFixture()...)
	rec := &capturedRecorder{}
	svc, err := app.NewMenuAdminService(app.MenuAdminConfig{
		Menus:      repo,
		Recorder:   rec,
		Components: allowedComponents{},
		IDs:        &seqIDs{},
		Audit:      app.AuditMeta{Service: "test", ActorType: "staff"},
	})
	if err != nil {
		t.Fatalf("NewMenuAdminService: %v", err)
	}
	return svc, repo, rec
}

// TestCreateMenuPersistsAndAudits 验证创建成功、版本从 1 开始并写审计。
func TestCreateMenuPersistsAndAudits(t *testing.T) {
	svc, _, rec := newMenuAdminFixture(t)
	menu, err := svc.CreateMenu(context.Background(), app.CreateMenuCommand{
		Channel:    domain.ChannelAdmin,
		Code:       "system.audit",
		ParentCode: "system",
		NodeType:   domain.MenuNodePage,
		RouteName:  "SystemAudit",
		RoutePath:  "audit",
		Component:  "/system/role/index.vue",
		Title:      "审计日志",
		Sort:       20,
		Actor:      "1001",
	})
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	if menu.Version != 1 || menu.Status != domain.MenuStatusActive {
		t.Fatalf("menu = %+v", menu)
	}
	event := rec.last(t)
	if event.Action != audit.ActionRBACMenuChange || event.Attrs[app.AttrOperation] != "create" {
		t.Fatalf("event = %+v", event)
	}
	if event.Attrs["menu_code"] != "system.audit" {
		t.Fatalf("attrs = %v", event.Attrs)
	}
}

// TestCreateMenuRejectsInvalidInput 覆盖端别、组件、权限码、父节点与重复编码。
func TestCreateMenuRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		cmd  app.CreateMenuCommand
	}{
		{
			name: "biz channel",
			cmd:  baseCreateMenuCommand(func(c *app.CreateMenuCommand) { c.Channel = domain.ChannelBiz }),
		},
		{
			name: "component not registered",
			cmd:  baseCreateMenuCommand(func(c *app.CreateMenuCommand) { c.Component = "/system/unknown/index.vue" }),
		},
		{
			name: "unknown permission",
			cmd: baseCreateMenuCommand(func(c *app.CreateMenuCommand) {
				c.PermissionCode = iam.PermissionCode("iam:menu:nope")
			}),
		},
		{
			name: "biz-only permission",
			cmd: baseCreateMenuCommand(func(c *app.CreateMenuCommand) {
				c.PermissionCode = iam.PermChangeReview
			}),
		},
		{
			name: "parent missing",
			cmd:  baseCreateMenuCommand(func(c *app.CreateMenuCommand) { c.ParentCode = "nope" }),
		},
		{
			name: "parent is not catalog",
			cmd:  baseCreateMenuCommand(func(c *app.CreateMenuCommand) { c.ParentCode = "system.role" }),
		},
		{
			name: "catalog with permission code",
			cmd: baseCreateMenuCommand(func(c *app.CreateMenuCommand) {
				c.Code = "system.settings"
				c.RouteName = "SystemSettings"
				c.NodeType = domain.MenuNodeCatalog
				c.Component = ""
				c.Redirect = "/system/settings"
				c.PermissionCode = iam.PermRoleRead
			}),
		},
		{
			name: "duplicate code",
			cmd:  baseCreateMenuCommand(func(c *app.CreateMenuCommand) { c.Code = "system.role" }),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newMenuAdminFixture(t)
			if _, err := svc.CreateMenu(context.Background(), tc.cmd); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

// baseCreateMenuCommand 返回可被各用例覆盖的最小合法命令。
func baseCreateMenuCommand(mutate func(*app.CreateMenuCommand)) app.CreateMenuCommand {
	cmd := app.CreateMenuCommand{
		Channel:    domain.ChannelAdmin,
		Code:       "system.audit",
		ParentCode: "system",
		NodeType:   domain.MenuNodePage,
		RouteName:  "SystemAudit",
		RoutePath:  "audit",
		Component:  "/system/role/index.vue",
		Title:      "审计日志",
		Sort:       20,
		Actor:      "1001",
	}
	if mutate != nil {
		mutate(&cmd)
	}
	return cmd
}

// TestUpdateMenuRejectsNonCatalogWithChildren 已挂子节点的目录不能再改成页面 / link / iframe。
func TestUpdateMenuRejectsNonCatalogWithChildren(t *testing.T) {
	svc, _, _ := newMenuAdminFixture(t)
	nodeType := domain.MenuNodePage
	component := "/system/role/index.vue"
	_, err := svc.UpdateMenu(context.Background(), app.UpdateMenuCommand{
		ID: "100", Version: 1, Actor: "1001",
		Fields: domain.MenuUpdateParams{NodeType: &nodeType, Component: &component},
	})
	if bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("err = %v, want 40000", err)
	}
}

// TestUpdateMenuUsesOptimisticLock 验证版本自增与冲突返回 40900。
func TestUpdateMenuUsesOptimisticLock(t *testing.T) {
	svc, _, rec := newMenuAdminFixture(t)
	title := "角色与权限"
	updated, err := svc.UpdateMenu(context.Background(), app.UpdateMenuCommand{
		ID: "200", Version: 1, Actor: "1001",
		Fields: domain.MenuUpdateParams{Title: &title},
	})
	if err != nil {
		t.Fatalf("UpdateMenu: %v", err)
	}
	if updated.Version != 2 || updated.Title != title {
		t.Fatalf("updated = %+v", updated)
	}
	if event := rec.last(t); event.Attrs["changed"] != "display" {
		t.Fatalf("attrs = %v", event.Attrs)
	}
	_, err = svc.UpdateMenu(context.Background(), app.UpdateMenuCommand{
		ID: "200", Version: 1, Actor: "1001",
		Fields: domain.MenuUpdateParams{Title: &title},
	})
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err = %v, want 40900", err)
	}
}

// TestDisableMenuBlocksActiveChildrenAndEnableChecksParent 验证停用 / 启用的树约束。
func TestDisableMenuBlocksActiveChildrenAndEnableChecksParent(t *testing.T) {
	svc, repo, _ := newMenuAdminFixture(t)
	_, err := svc.DisableMenu(context.Background(), app.DisableMenuCommand{ID: "100", Version: 1, Actor: "1001"})
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("disable parent err = %v, want 40900", err)
	}
	disabled, err := svc.DisableMenu(context.Background(), app.DisableMenuCommand{ID: "200", Version: 1, Actor: "1001"})
	if err != nil {
		t.Fatalf("DisableMenu child: %v", err)
	}
	if disabled.Status != domain.MenuStatusDisabled || disabled.Version != 2 {
		t.Fatalf("disabled = %+v", disabled)
	}
	if _, err := svc.EnableMenu(context.Background(), app.EnableMenuCommand{ID: "200", Version: 2, Actor: "1001"}); err != nil {
		t.Fatalf("EnableMenu: %v", err)
	}
	// 再次停用子节点，随后停用父目录，再启用子节点必须被拒绝。
	if _, err := svc.DisableMenu(context.Background(), app.DisableMenuCommand{ID: "200", Version: 3, Actor: "1001"}); err != nil {
		t.Fatalf("DisableMenu child again: %v", err)
	}
	stored, err := repo.Get(context.Background(), "100")
	if err != nil {
		t.Fatal(err)
	}
	stored.Status = domain.MenuStatusDisabled
	stored.Version = 2
	if err := repo.Update(context.Background(), stored, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnableMenu(context.Background(), app.EnableMenuCommand{ID: "200", Version: 4, Actor: "1001"}); bizCode(err) != apperr.CodeConflict {
		t.Fatalf("enable under disabled parent err = %v, want 40900", err)
	}
}

func TestEnableMenuRejectsUnregisteredComponent(t *testing.T) {
	svc, repo, _ := newMenuAdminFixture(t)
	ctx := context.Background()
	stored, err := repo.Get(ctx, "200")
	if err != nil {
		t.Fatal(err)
	}
	stored.Status = domain.MenuStatusDisabled
	stored.Component = "/retired/index.vue"
	stored.Version = 2
	if err := repo.Update(ctx, stored, 1); err != nil {
		t.Fatal(err)
	}

	_, err = svc.EnableMenu(ctx, app.EnableMenuCommand{ID: "200", Version: 2, Actor: "1001"})
	if bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("enable retired component: %v, want bad request", err)
	}
	stored, err = repo.Get(ctx, "200")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.MenuStatusDisabled || stored.Version != 2 {
		t.Fatalf("rejected enable changed status or version: %+v", stored)
	}
}

// TestListAndTreeIncludeFilters 验证关键字过滤、分页与停用节点开关。
func TestListAndTreeIncludeFilters(t *testing.T) {
	svc, repo, _ := newMenuAdminFixture(t)
	menus, total, err := svc.List(context.Background(), app.MenuFilter{
		Channel: domain.ChannelAdmin, Keyword: "role",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(menus) != 1 || !strings.Contains(menus[0].Code, "role") {
		t.Fatalf("list = %+v, total=%d", menus, total)
	}

	stored, err := repo.Get(context.Background(), "200")
	if err != nil {
		t.Fatal(err)
	}
	stored.Status = domain.MenuStatusDisabled
	stored.Version = 2
	if err := repo.Update(context.Background(), stored, 1); err != nil {
		t.Fatal(err)
	}
	activeTree, err := svc.Tree(context.Background(), domain.ChannelAdmin, false)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(activeTree) != 1 || len(activeTree[0].Children) != 0 {
		t.Fatalf("active tree = %+v", activeTree)
	}
	fullTree, err := svc.Tree(context.Background(), domain.ChannelAdmin, true)
	if err != nil {
		t.Fatalf("Tree include disabled: %v", err)
	}
	if len(fullTree) != 1 || len(fullTree[0].Children) != 1 {
		t.Fatalf("full tree = %+v", fullTree)
	}
}
