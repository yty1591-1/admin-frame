package app_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func TestSodGroupServiceCreateAndAudit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	roleA := guardCreateRole(t, f, "sod_svc_a")
	roleB := guardCreateRole(t, f, "sod_svc_b")

	group, err := f.sodSvc.Create(ctx, app.CreateSodGroupCommand{
		Channel:        domain.ChannelAdmin,
		Code:           "finance_dual",
		Name:           "财务双人复核",
		MaxActiveRoles: 1,
		RoleIDs:        []string{roleA.ID, roleB.ID},
		Actor:          "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if group.Version != 1 || group.Status != domain.SodGroupActive || len(group.RoleIDs) != 2 {
		t.Fatalf("group = %+v", group)
	}
	event := f.recorder.last(t)
	if event.Action != "rbac.sod_change" || event.Attrs["operation"] != "create" ||
		event.Attrs["group_code"] != "finance_dual" {
		t.Fatalf("audit event = %+v", event)
	}

	// 端内编码唯一。
	_, err = f.sodSvc.Create(ctx, app.CreateSodGroupCommand{
		Channel: domain.ChannelAdmin, Code: "finance_dual", Name: "重复",
		RoleIDs: []string{roleA.ID, roleB.ID}, Actor: "1001",
	})
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("duplicate code err=%v, want 40900", err)
	}
}

func TestSodGroupServiceRejectsExistingViolation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	roleA := guardCreateRole(t, f, "sod_violation_a")
	roleB := guardCreateRole(t, f, "sod_violation_b")
	if _, err := guardBind(t, f, "2001", roleA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := guardBind(t, f, "2001", roleB.ID); err != nil {
		t.Fatal(err)
	}

	_, err := f.sodSvc.Create(ctx, app.CreateSodGroupCommand{
		Channel: domain.ChannelAdmin, Code: "violation_dual", Name: "存量冲突",
		RoleIDs: []string{roleA.ID, roleB.ID}, Actor: "1001",
	})
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want 40900", err)
	}

	violations, err := f.sodSvc.Preflight(ctx, domain.ChannelAdmin, "",
		[]string{roleA.ID, roleB.ID}, 1, 50)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(violations) != 1 || violations[0].SubjectID != "2001" || len(violations[0].RoleIDs) != 2 {
		t.Fatalf("violations=%+v", violations)
	}
}

func TestSodGroupServiceUpdateRolesPreflight(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	roleA := guardCreateRole(t, f, "sod_update_a")
	roleB := guardCreateRole(t, f, "sod_update_b")
	roleC := guardCreateRole(t, f, "sod_update_c")
	roleD := guardCreateRole(t, f, "sod_update_d")

	group, err := f.sodSvc.Create(ctx, app.CreateSodGroupCommand{
		Channel: domain.ChannelAdmin, Code: "update_dual", Name: "改组预检",
		RoleIDs: []string{roleA.ID, roleB.ID}, Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 先绑定 C/D：此时它们不在组内，允许绑定。
	if _, err := guardBind(t, f, "2001", roleC.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := guardBind(t, f, "2001", roleD.ID); err != nil {
		t.Fatal(err)
	}

	nextRoles := []string{roleC.ID, roleD.ID}
	_, err = f.sodSvc.Update(ctx, app.UpdateSodGroupCommand{
		ID: group.ID, Channel: domain.ChannelAdmin,
		RoleIDs: &nextRoles, Version: group.Version, Actor: "1001", Reason: "改组",
	})
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want 40900 (existing subject would violate)", err)
	}

	// 只改名称不扩大约束面，必须放行。
	newName := "改组预检-改名"
	updated, err := f.sodSvc.Update(ctx, app.UpdateSodGroupCommand{
		ID: group.ID, Channel: domain.ChannelAdmin,
		Name: &newName, Version: group.Version, Actor: "1001", Reason: "改名",
	})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if updated.Name != newName {
		t.Fatalf("name=%s", updated.Name)
	}
}

func TestSodGroupServiceRoleValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	adminRole := guardCreateRole(t, f, "sod_validate_admin")
	parent := guardCreateRole(t, f, "sod_validate_parent")
	child := guardCreateRole(t, f, "sod_validate_child", parent.ID)

	bizRole, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelBiz, Scope: domain.RoleScopeMerchant, MerchantID: "m-1",
		Code: "sod_validate_biz", Name: "商户角色", Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		roleIDs []string
	}{
		{name: "cross tenant", roleIDs: []string{adminRole.ID, bizRole.ID}},
		{name: "ancestor and descendant", roleIDs: []string{parent.ID, child.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.sodSvc.Create(ctx, app.CreateSodGroupCommand{
				Channel: domain.ChannelAdmin, Code: "invalid_group", Name: "非法组",
				RoleIDs: tc.roleIDs, Actor: "1001",
			})
			if bizCode(err) != apperr.CodeBadRequest {
				t.Fatalf("err=%v, want 40000", err)
			}
		})
	}
}

func TestSodGroupServiceTenantIsolation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bizRoleA, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelBiz, Scope: domain.RoleScopeMerchant, MerchantID: "m-1",
		Code: "tenant_role_a", Name: "商户角色A", Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	bizRoleB, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel: domain.ChannelBiz, Scope: domain.RoleScopeMerchant, MerchantID: "m-1",
		Code: "tenant_role_b", Name: "商户角色B", Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	group, err := f.sodSvc.Create(ctx, app.CreateSodGroupCommand{
		Channel: domain.ChannelBiz, MerchantID: "m-1", Code: "tenant_dual", Name: "商户互斥",
		RoleIDs: []string{bizRoleA.ID, bizRoleB.ID}, Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.sodSvc.Get(ctx, group.ID, domain.ChannelBiz, "m-1"); err != nil {
		t.Fatalf("own merchant: %v", err)
	}
	if _, err := f.sodSvc.Get(ctx, group.ID, domain.ChannelBiz, "m-2"); bizCode(err) != apperr.CodeNotFound {
		t.Fatalf("cross merchant err=%v, want 40400", err)
	}
	if _, err := f.sodSvc.Get(ctx, group.ID, domain.ChannelAdmin, ""); bizCode(err) != apperr.CodeNotFound {
		t.Fatalf("cross channel err=%v, want 40400", err)
	}
}

func TestSodGroupServiceDisableAndDelete(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	roleA := guardCreateRole(t, f, "sod_delete_a")
	roleB := guardCreateRole(t, f, "sod_delete_b")
	group, err := f.sodSvc.Create(ctx, app.CreateSodGroupCommand{
		Channel: domain.ChannelAdmin, Code: "delete_group", Name: "待删除",
		RoleIDs: []string{roleA.ID, roleB.ID}, Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}

	disabled := "disabled"
	updated, err := f.sodSvc.Update(ctx, app.UpdateSodGroupCommand{
		ID: group.ID, Channel: domain.ChannelAdmin, Status: &disabled,
		Version: group.Version, Actor: "1001", Reason: "暂停约束",
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := f.sodSvc.Delete(ctx, app.DeleteSodGroupCommand{
		ID: group.ID, Channel: domain.ChannelAdmin, Version: updated.Version,
		Actor: "1001", Reason: "清理",
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.sodSvc.Get(ctx, group.ID, domain.ChannelAdmin, ""); bizCode(err) != apperr.CodeNotFound {
		t.Fatalf("deleted group must be not found, err=%v", err)
	}
	groups, err := f.sodSvc.List(ctx, domain.SodGroupFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("deleted group leaked into list: %v", groups)
	}
}

func TestSodGroupServiceEnableValidatesMembers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	roleA := guardCreateRole(t, f, "sod_enable_a")
	roleB := guardCreateRole(t, f, "sod_enable_b")
	group, err := f.sodSvc.Create(ctx, app.CreateSodGroupCommand{
		Channel: domain.ChannelAdmin, Code: "enable_group", Name: "启用校验",
		RoleIDs: []string{roleA.ID, roleB.ID}, Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled := "disabled"
	updated, err := f.sodSvc.Update(ctx, app.UpdateSodGroupCommand{
		ID: group.ID, Channel: domain.ChannelAdmin, Status: &disabled,
		Version: group.Version, Actor: "1001", Reason: "暂停",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.roleSvc.DeleteRole(ctx, app.DeleteRoleCommand{
		ID: roleB.ID, Version: roleB.Version, Actor: "1001", Reason: "清理",
	}); err != nil {
		t.Fatalf("delete member role: %v", err)
	}

	active := "active"
	_, err = f.sodSvc.Update(ctx, app.UpdateSodGroupCommand{
		ID: group.ID, Channel: domain.ChannelAdmin, Status: &active,
		Version: updated.Version, Actor: "1001", Reason: "恢复",
	})
	if bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("err=%v, want 40000 (deleted member must block enable)", err)
	}
}

func TestChangeRequestSodGroupCreateFlow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	roleA := guardCreateRole(t, f, "sod_flow_a")
	roleB := guardCreateRole(t, f, "sod_flow_b")

	payload := fmt.Sprintf(
		`{"code":"flow_dual","name":"流程互斥","max_active_roles":1,"role_ids":[%q,%q]}`,
		roleA.ID, roleB.ID)
	request, err := f.changeSvc.Create(ctx, app.CreateChangeRequestCommand{
		Channel: domain.ChannelAdmin,
		Action:  domain.ChangeActionSodGroupCreate,
		Payload: []byte(payload),
		Reason:  "建立互斥",
		Actor:   "1001",
	})
	if err != nil {
		t.Fatalf("Create change request: %v", err)
	}
	if request.TargetType != domain.ChangeTargetIAMSodGroup {
		t.Fatalf("target type=%s", request.TargetType)
	}

	approved, err := f.changeSvc.Approve(ctx, request.ID, request.Version, "1002", "", adminScope)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	applied, err := f.changeSvc.Apply(ctx, app.ApplyChangeRequestCommand{
		ID: approved.ID, Version: approved.Version, Actor: "1002", Scope: adminScope,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != domain.ChangeStatusApplied || applied.TargetID == "" {
		t.Fatalf("applied=%+v", applied)
	}
	group, err := f.sodGroups.FindByID(ctx, applied.TargetID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if group.Code != "flow_dual" || len(group.RoleIDs) != 2 {
		t.Fatalf("group=%+v", group)
	}
	if f.recorder.last(t).Action != "rbac.sod_change" {
		t.Fatalf("audit event = %+v", f.recorder.last(t))
	}
}
