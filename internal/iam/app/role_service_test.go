package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	platformaudit "github.com/admin-frame/admin-frame/internal/platform/audit"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

func TestCreateRolePersistsPermissionsAndAudits(t *testing.T) {
	f := newFixture(t)
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		Code:        "ops_lead",
		Name:        "运营主管",
		Permissions: []iam.PermissionCode{iam.PermRoleWrite, iam.PermRoleRead},
		DataScopes:  []domain.DataScope{scope(t, domain.DataScopeMerchantSet)},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if role.Version != 1 {
		t.Fatalf("version = %d, want 1", role.Version)
	}
	if len(role.PermissionCodes()) != 2 {
		t.Fatalf("permissions = %v", role.PermissionCodes())
	}
	event := f.recorder.last(t)
	if event.Action != audit.ActionRBACRoleChange {
		t.Fatalf("action = %q", event.Action)
	}
	if event.Attrs["operation"] != "create" || event.Attrs["role_code"] != "ops_lead" {
		t.Fatalf("attrs = %v", event.Attrs)
	}
	if event.Channel != "admin" || event.ActorID != "1001" {
		t.Fatalf("event = %+v", event)
	}
}

func TestCreateRoleRejectsUnknownAndIncompatiblePermissions(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		cmd  app.CreateRoleCommand
	}{
		{
			name: "unknown code",
			cmd: app.CreateRoleCommand{
				Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
				Code: "ops", Name: "ops", Permissions: []iam.PermissionCode{"iam:delete"}, Actor: "1",
			},
		},
		{
			name: "admin-only code on biz",
			cmd: app.CreateRoleCommand{
				Channel: domain.ChannelBiz, Scope: domain.RoleScopeMerchant, MerchantID: "m_001",
				Code: "merchant_ops", Name: "ops", Permissions: []iam.PermissionCode{iam.PermAuditRead}, Actor: "1",
			},
		},
		{
			name: "missing data scope",
			cmd: app.CreateRoleCommand{
				Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
				Code: "reader", Name: "reader", Permissions: []iam.PermissionCode{iam.PermDataExport}, Actor: "1",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.roleSvc.CreateRole(context.Background(), tc.cmd)
			if bizCode(err) != apperr.CodeBadRequest {
				t.Fatalf("err = %v, want 40000", err)
			}
			if f.recorder.count() != 0 {
				t.Fatalf("audit must not be written on rejected input")
			}
		})
	}
}

func TestCreateRoleValidatesActorAndLimits(t *testing.T) {
	f := newFixture(t)
	_, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops", Name: "ops",
	})
	if bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("missing actor err = %v, want 40000", err)
	}

	tooMany := make([]iam.PermissionCode, 0, 201)
	tooMany = append(tooMany, iam.PermRoleRead)
	for len(tooMany) < 201 {
		tooMany = append(tooMany, iam.PermRoleRead)
	}
	_, err = f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops2", Name: "ops", Permissions: tooMany, Actor: "1",
	})
	if bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("limit err = %v, want 40000", err)
	}
}

func TestReplacePermissionsComputesDiffAndInvalidatesSubjects(t *testing.T) {
	f := newFixture(t)
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		Code:        "ops_lead",
		Name:        "运营主管",
		Permissions: []iam.PermissionCode{iam.PermRoleRead, iam.PermRoleWrite},
		DataScopes:  []domain.DataScope{scope(t, domain.DataScopeAll)},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, Actor: "1001",
	}); err != nil {
		t.Fatal(err)
	}

	updated, change, err := f.roleSvc.ReplacePermissions(context.Background(), app.ReplacePermissionsCommand{
		ID:          role.ID,
		Permissions: []iam.PermissionCode{iam.PermRoleRead},
		Version:     role.Version,
		Actor:       "1001",
		Reason:      "职责调整",
	})
	if err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}
	if updated.Version != role.Version+1 {
		t.Fatalf("version = %d, want %d", updated.Version, role.Version+1)
	}
	if len(change.Added) != 0 || len(change.Removed) != 1 || change.Removed[0] != iam.PermRoleWrite {
		t.Fatalf("change = %+v", change)
	}
	if f.invalidator.count() == 0 {
		t.Fatal("expected bound subject snapshot invalidation")
	}
	event := f.recorder.last(t)
	if event.Attrs["removed"] != string(iam.PermRoleWrite) || event.Reason != "职责调整" {
		t.Fatalf("event = %+v", event)
	}

	// 旧版本必须冲突，防止并发覆盖。
	if _, _, err := f.roleSvc.ReplacePermissions(context.Background(), app.ReplacePermissionsCommand{
		ID: role.ID, Permissions: nil, Version: role.Version, Actor: "1001", Reason: "retry",
	}); bizCode(err) != apperr.CodeConflict {
		t.Fatalf("stale version err = %v, want 40900", err)
	}
}

func TestReplacePermissionsRequiresReasonAndScopes(t *testing.T) {
	f := newFixture(t)
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops", Name: "ops",
		Permissions: []iam.PermissionCode{iam.PermDataExport},
		DataScopes:  []domain.DataScope{{Resource: "data", Type: domain.DataScopeAll}},
		Actor:       "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.roleSvc.ReplacePermissions(context.Background(), app.ReplacePermissionsCommand{
		ID: role.ID, Permissions: []iam.PermissionCode{iam.PermDataExport}, Version: role.Version, Actor: "1",
	}); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("missing reason err = %v, want 40000", err)
	}
	empty := make([]domain.DataScope, 0)
	if _, _, err := f.roleSvc.ReplacePermissions(context.Background(), app.ReplacePermissionsCommand{
		ID: role.ID, Permissions: []iam.PermissionCode{iam.PermDataExport}, DataScopes: &empty,
		Version: role.Version, Actor: "1", Reason: "clear scope",
	}); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("missing scope err = %v, want 40000", err)
	}
}

func TestUpdateRoleStatusAndDetails(t *testing.T) {
	f := newFixture(t)
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops", Name: "ops", Actor: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	name := "运营"
	disabled := string(domain.RoleStatusDisabled)
	updated, err := f.roleSvc.UpdateRole(context.Background(), app.UpdateRoleCommand{
		ID: role.ID, Name: &name, Status: &disabled, Version: role.Version, Actor: "1", Reason: "rotate",
	})
	if err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	if updated.Name != name || updated.Status != domain.RoleStatusDisabled {
		t.Fatalf("role = %+v", updated)
	}
	if updated.Version != role.Version+2 {
		t.Fatalf("version = %d, want %d", updated.Version, role.Version+2)
	}
	if f.recorder.last(t).Attrs["operation"] != "disable" {
		t.Fatalf("audit = %+v", f.recorder.last(t))
	}

	// 停用角色不允许继续授权。
	if _, _, err := f.roleSvc.ReplacePermissions(context.Background(), app.ReplacePermissionsCommand{
		ID: role.ID, Permissions: []iam.PermissionCode{iam.PermRoleRead},
		Version: updated.Version, Actor: "1", Reason: "grant",
	}); bizCode(err) != apperr.CodeConflict {
		t.Fatalf("disabled grant err = %v, want 40900", err)
	}
}

func TestCloneRoleCreatesDisabledCopy(t *testing.T) {
	f := newFixture(t)
	source, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops", Name: "ops", Permissions: []iam.PermissionCode{iam.PermRoleRead},
		DataScopes: []domain.DataScope{scope(t, domain.DataScopeAll)}, Actor: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	clone, err := f.roleSvc.CloneRole(context.Background(), app.CloneRoleCommand{
		SourceID: source.ID, Code: "ops_copy", Name: "ops copy", Actor: "1",
	})
	if err != nil {
		t.Fatalf("CloneRole: %v", err)
	}
	if clone.Status != domain.RoleStatusDisabled {
		t.Fatalf("status = %q, want disabled", clone.Status)
	}
	if len(clone.PermissionCodes()) != 1 {
		t.Fatalf("permissions = %v", clone.PermissionCodes())
	}
	if f.recorder.last(t).Attrs["operation"] != "clone" {
		t.Fatalf("audit = %+v", f.recorder.last(t))
	}
}

func TestDeleteRoleRequiresNoBindingsAndReason(t *testing.T) {
	f := newFixture(t)
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops", Name: "ops", Actor: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, Actor: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.roleSvc.DeleteRole(context.Background(), app.DeleteRoleCommand{
		ID: role.ID, Version: role.Version, Actor: "1", Reason: "cleanup",
	}); bizCode(err) != apperr.CodeConflict {
		t.Fatalf("bound delete err = %v, want 40900", err)
	}
	if err := f.bindingSvc.Unbind(context.Background(), app.UnbindCommand{
		ID: binding.ID, Actor: "1", Reason: "cleanup",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.roleSvc.DeleteRole(context.Background(), app.DeleteRoleCommand{
		ID: role.ID, Version: role.Version, Actor: "1",
	}); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("missing reason err = %v, want 40000", err)
	}
	if err := f.roleSvc.DeleteRole(context.Background(), app.DeleteRoleCommand{
		ID: role.ID, Version: role.Version, Actor: "1", Reason: "cleanup",
	}); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if f.recorder.last(t).Attrs["operation"] != "delete" {
		t.Fatalf("audit = %+v", f.recorder.last(t))
	}
	if _, err := f.roles.FindByID(context.Background(), role.ID); err != nil {
		t.Fatalf("soft-deleted role must remain queryable: %v", err)
	}
}

func TestUpdateRoleValidatesReasonAndInvalidatesSubjects(t *testing.T) {
	f := newFixture(t)
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops", Name: "ops", Actor: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, Actor: "1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.roleSvc.UpdateRole(context.Background(), app.UpdateRoleCommand{
		ID: role.ID, Version: role.Version, Actor: "1",
	}); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("empty patch err = %v, want 40000", err)
	}
	disabled := string(domain.RoleStatusDisabled)
	if _, err := f.roleSvc.UpdateRole(context.Background(), app.UpdateRoleCommand{
		ID: role.ID, Status: &disabled, Version: role.Version, Actor: "1",
	}); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("disable without reason err = %v, want 40000", err)
	}
	if _, err := f.roleSvc.UpdateRole(context.Background(), app.UpdateRoleCommand{
		ID: role.ID, Status: &disabled, Version: role.Version, Actor: "1", Reason: "rotate",
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if f.invalidator.count() != 2 {
		// 1 次来自 bind，1 次来自停用后的主动失效。
		t.Fatalf("invalidations = %d, want 2", f.invalidator.count())
	}
	if operation := f.recorder.last(t).Attrs["operation"]; operation != "disable" {
		t.Fatalf("audit operation = %q, want disable", operation)
	}
}

func TestDeleteRoleIgnoresExpiredBindings(t *testing.T) {
	f := newFixture(t)
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops", Name: "ops", Actor: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().UTC().Add(-time.Minute)
	// 直接写仓储：绕过 NewBinding 的「过期时间必须在未来」校验，模拟历史遗留记录。
	if err := f.bindings.Save(context.Background(), &domain.Binding{
		ID: "expired-1", Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, ExpiresAt: &expired,
		GrantedBy: "1", CreatedAt: expired.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.roleSvc.DeleteRole(context.Background(), app.DeleteRoleCommand{
		ID: role.ID, Version: role.Version, Actor: "1", Reason: "cleanup",
	}); err != nil {
		t.Fatalf("expired binding must not block delete: %v", err)
	}
}

func TestAuditFailureFailsClosed(t *testing.T) {
	f := newFixture(t)
	f.recorder.err = errors.New("outbox unavailable")
	_, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops", Name: "ops", Actor: "1",
	})
	if bizCode(err) != apperr.CodeInternal {
		t.Fatalf("err = %v, want 50000", err)
	}
}

func TestNewRoleServiceRequiresDependencies(t *testing.T) {
	if _, err := app.NewRoleService(app.RoleServiceConfig{}); err == nil {
		t.Fatal("expected error for missing dependencies")
	}
	if _, err := app.NewBindingService(app.BindingServiceConfig{}); err == nil {
		t.Fatal("expected error for missing dependencies")
	}
	if _, err := app.NewQueryService(nil, nil, nil); err == nil {
		t.Fatal("expected error for missing dependencies")
	}
}

func TestSlogRecorderAndNopRecorder(t *testing.T) {
	event := audit.Event{
		EventID: "1", Action: audit.ActionRBACRoleChange, ActorID: "1", Channel: "admin",
		ResourceType: app.ResourceTypeRole, ResourceID: "r1", Outcome: audit.OutcomeSuccess,
		OccurredAt: time.Now().UTC(), TraceID: "t1", Service: "test",
	}
	if err := (app.NopRecorder{}).Record(context.Background(), event); err != nil {
		t.Fatalf("NopRecorder: %v", err)
	}
	if err := (app.SlogRecorder{}).Record(context.Background(), event); bizCode(err) != apperr.CodeInternal {
		t.Fatalf("SlogRecorder without emitter err = %v, want 50000", err)
	}
	recorder := app.SlogRecorder{Emitter: platformaudit.SlogEmitter{}}
	if err := recorder.Record(context.Background(), event); err != nil {
		t.Fatalf("SlogRecorder: %v", err)
	}
}

func TestRoleHierarchyExpandsPermissionsAndScopes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	parent, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		Code:        "base_reader",
		Name:        "基础只读",
		Permissions: []iam.PermissionCode{iam.PermRoleRead, iam.PermDataExport},
		DataScopes:  []domain.DataScope{{Resource: "data", Type: domain.DataScopeAll}},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		ParentID:    parent.ID,
		Code:        "order_reader",
		Name:        "订单只读",
		Permissions: []iam.PermissionCode{iam.PermRoleGrant},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.ParentID != parent.ID {
		t.Fatalf("parent_id = %q, want %q", child.ParentID, parent.ID)
	}
	if _, err := f.bindingSvc.Bind(ctx, app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: child.ID, Actor: "1001",
	}); err != nil {
		t.Fatalf("bind child: %v", err)
	}

	snapshot, err := f.snapshots.Load(ctx, domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "2001",
	})
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	for _, code := range []iam.PermissionCode{iam.PermRoleRead, iam.PermDataExport, iam.PermRoleGrant} {
		if !snapshot.HasPermission(code) {
			t.Fatalf("permission %s not expanded: %v", code, snapshot.Permissions)
		}
	}
	if len(snapshot.Roles) != 2 || snapshot.Roles[0] != "base_reader" || snapshot.Roles[1] != "order_reader" {
		t.Fatalf("roles = %v, want both bound and ancestor codes", snapshot.Roles)
	}
	inherited, ok := snapshot.ScopeFor("data")
	if !ok || inherited.Type != domain.DataScopeAll {
		t.Fatalf("inherited scope = %+v, ok=%v", inherited, ok)
	}
}

func TestRoleHierarchyRejectsCycleCrossTenantAndMissingParent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	root, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "hierarchy_root", Name: "根角色", Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		ParentID: root.ID, Code: "hierarchy_child", Name: "子角色", Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	bizRole, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelBiz, Scope: domain.RoleScopeMerchant, MerchantID: "m-1",
		Code: "merchant_role", Name: "商户角色", Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}

	pointer := func(value string) *string { return &value }
	cases := []struct {
		name     string
		roleID   string
		version  int64
		parentID string
	}{
		{name: "self parent", roleID: root.ID, version: root.Version, parentID: root.ID},
		{name: "cycle back to descendant", roleID: root.ID, version: root.Version, parentID: child.ID},
		{name: "cross channel parent", roleID: bizRole.ID, version: bizRole.Version, parentID: root.ID},
		{name: "missing parent", roleID: child.ID, version: child.Version, parentID: "987654321"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, updateErr := f.roleSvc.UpdateRole(ctx, app.UpdateRoleCommand{
				ID: tc.roleID, ParentID: pointer(tc.parentID), Version: tc.version,
				Actor: "1001", Reason: "调整层级",
			})
			if bizCode(updateErr) != apperr.CodeBadRequest {
				t.Fatalf("err = %v, want 40000", updateErr)
			}
		})
	}
}

func TestParentPermissionChangeInvalidatesDescendantSubjects(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	parent, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "invalidate_parent", Name: "父角色",
		Permissions: []iam.PermissionCode{iam.PermRoleRead}, Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		ParentID: parent.ID, Code: "invalidate_child", Name: "子角色", Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.bindingSvc.Bind(ctx, app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "3001", RoleID: child.ID, Actor: "1001",
	}); err != nil {
		t.Fatal(err)
	}
	before := f.invalidator.count()
	if _, _, err := f.roleSvc.ReplacePermissions(ctx, app.ReplacePermissionsCommand{
		ID: parent.ID, Permissions: []iam.PermissionCode{iam.PermRoleRead, iam.PermRoleWrite},
		Version: parent.Version, Actor: "1001", Reason: "扩充父角色权限",
	}); err != nil {
		t.Fatalf("ReplacePermissions(parent): %v", err)
	}
	if f.invalidator.count() <= before {
		t.Fatal("expected descendant-bound subject snapshot invalidation")
	}
	subjects := f.invalidator.subjects
	found := false
	for _, subject := range subjects {
		if subject == "admin||staff|3001" {
			found = true
		}
	}
	if !found {
		t.Fatalf("descendant subject not invalidated: %v", subjects)
	}
}

func TestDeleteRoleRejectsExistingChildren(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	parent, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "delete_parent", Name: "父角色", Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		ParentID: parent.ID, Code: "delete_child", Name: "子角色", Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.roleSvc.DeleteRole(ctx, app.DeleteRoleCommand{
		ID: parent.ID, Version: parent.Version, Actor: "1001", Reason: "清理",
	}); bizCode(err) != apperr.CodeConflict {
		t.Fatalf("delete parent err = %v, want 40900", err)
	}
	if err := f.roleSvc.DeleteRole(ctx, app.DeleteRoleCommand{
		ID: child.ID, Version: child.Version, Actor: "1001", Reason: "清理",
	}); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	if err := f.roleSvc.DeleteRole(ctx, app.DeleteRoleCommand{
		ID: parent.ID, Version: parent.Version, Actor: "1001", Reason: "清理",
	}); err != nil {
		t.Fatalf("delete parent after child removed: %v", err)
	}
}

func TestCreateRoleEnforcesMerchantQuota(t *testing.T) {
	f := newFixture(t)
	svc, err := app.NewRoleService(app.RoleServiceConfig{
		Roles: f.roles, Bindings: f.bindings, Recorder: f.recorder, Invalidator: f.invalidator,
		IDs:   &seqIDs{},
		Audit: app.AuditMeta{Service: "test", ActorType: "merchant_staff"},
		Limits: app.RoleLimits{
			MaxPermissionsPerRole:     200,
			MaxCustomRolesPerMerchant: 2,
		},
	})
	if err != nil {
		t.Fatalf("NewRoleService: %v", err)
	}
	ctx := context.Background()
	create := func(merchantID, code string) error {
		_, err := svc.CreateRole(ctx, app.CreateRoleCommand{
			Channel: domain.ChannelBiz, Scope: domain.RoleScopeMerchant,
			MerchantID: merchantID, Code: code, Name: code, Actor: "1001",
		})
		return err
	}
	for _, code := range []string{"merchant_viewer", "merchant_operator"} {
		if err := create("m-1", code); err != nil {
			t.Fatalf("create %s: %v", code, err)
		}
	}
	if err := create("m-1", "merchant_third"); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("quota err=%v, want 40000", err)
	}
	// 配额按商户隔离：另一个商户仍可创建。
	if err := create("m-2", "merchant_viewer"); err != nil {
		t.Fatalf("other merchant must not be blocked: %v", err)
	}
	// 平台角色不占商户配额。
	if _, err := svc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "ops_lead_quota", Name: "运营主管", Actor: "1001",
	}); err != nil {
		t.Fatalf("platform role must bypass merchant quota: %v", err)
	}
}
