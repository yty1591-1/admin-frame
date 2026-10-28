package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// guardCreateRole 通过用例创建一个 A 端平台角色；parentID 可选。
func guardCreateRole(t *testing.T, f *fixture, code string, parentID ...string) *domain.Role {
	t.Helper()
	cmd := app.CreateRoleCommand{
		Channel: domain.ChannelAdmin,
		Scope:   domain.RoleScopePlatform,
		Code:    code,
		Name:    code,
		Actor:   "1001",
	}
	if len(parentID) > 0 {
		cmd.ParentID = parentID[0]
	}
	role, err := f.roleSvc.CreateRole(context.Background(), cmd)
	if err != nil {
		t.Fatalf("CreateRole(%s): %v", code, err)
	}
	return role
}

// guardCreateSodGroup 直接落库一个互斥组（P3 才提供服务层 CRUD）。
func guardCreateSodGroup(t *testing.T, f *fixture, id, code string, roleIDs []string) *domain.SodGroup {
	t.Helper()
	group, err := domain.NewSodGroup(domain.NewSodGroupParams{
		ID:      id,
		Channel: domain.ChannelAdmin,
		Code:    code,
		Name:    code,
		RoleIDs: roleIDs,
		Actor:   "1001",
	})
	if err != nil {
		t.Fatalf("NewSodGroup(%s): %v", code, err)
	}
	if err := f.sodGroups.Save(context.Background(), group, 0); err != nil {
		t.Fatalf("SaveSodGroup(%s): %v", code, err)
	}
	return group
}

// guardBind 以 A 端员工身份绑定角色。
func guardBind(t *testing.T, f *fixture, subjectID, roleID string) (*domain.Binding, error) {
	t.Helper()
	return f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   subjectID,
		RoleID:      roleID,
		Actor:       "1001",
	})
}

func TestConstraintGuardBlocksSodConflict(t *testing.T) {
	f := newFixtureWithConstraintMode(t, app.ConstraintModeEnforce)
	ctx := context.Background()
	roleA := guardCreateRole(t, f, "sod_a")
	roleB := guardCreateRole(t, f, "sod_b")
	guardCreateSodGroup(t, f, "8101", "dual_control", []string{roleA.ID, roleB.ID})

	if _, err := guardBind(t, f, "2001", roleA.ID); err != nil {
		t.Fatalf("bind first role: %v", err)
	}
	_, err := guardBind(t, f, "2001", roleB.ID)
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want 40900", err)
	}
	var constraintErr *app.ConstraintError
	if !errors.As(err, &constraintErr) {
		t.Fatalf("err=%v, want *app.ConstraintError", err)
	}
	if len(constraintErr.Violations) != 1 ||
		constraintErr.Violations[0].Kind != domain.ConstraintKindSodConflict ||
		constraintErr.Violations[0].GroupCode != "dual_control" {
		t.Fatalf("violations=%+v", constraintErr.Violations)
	}

	// 被拒绝的绑定不得落库。
	list, err := f.bindings.ListBySubject(ctx, domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "2001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].RoleID != roleA.ID {
		t.Fatalf("bindings=%v", list)
	}
}

func TestConstraintGuardShadowModeAllowsViolation(t *testing.T) {
	f := newFixtureWithConstraintMode(t, app.ConstraintModeShadow)
	roleA := guardCreateRole(t, f, "shadow_a")
	roleB := guardCreateRole(t, f, "shadow_b")
	guardCreateSodGroup(t, f, "8102", "shadow_dual", []string{roleA.ID, roleB.ID})

	if _, err := guardBind(t, f, "2001", roleA.ID); err != nil {
		t.Fatalf("bind first role: %v", err)
	}
	if _, err := guardBind(t, f, "2001", roleB.ID); err != nil {
		t.Fatalf("shadow mode must not block: %v", err)
	}
}

func TestConstraintGuardHierarchyClosureConflict(t *testing.T) {
	f := newFixtureWithConstraintMode(t, app.ConstraintModeEnforce)
	parent := guardCreateRole(t, f, "closure_parent")
	child := guardCreateRole(t, f, "closure_child", parent.ID)
	other := guardCreateRole(t, f, "closure_other")
	guardCreateSodGroup(t, f, "8103", "closure_dual", []string{parent.ID, other.ID})

	// 子角色继承父角色权限：绑定子角色等价于持有父角色。
	if _, err := guardBind(t, f, "2001", child.ID); err != nil {
		t.Fatalf("bind child: %v", err)
	}
	_, err := guardBind(t, f, "2001", other.ID)
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want 40900 (inherited parent must count)", err)
	}
}

func TestConstraintGuardRoleCardinality(t *testing.T) {
	f := newFixtureWithConstraintMode(t, app.ConstraintModeEnforce)
	ctx := context.Background()
	limit := 1
	role, err := domain.NewRole(domain.NewRoleParams{
		ID: "7101", Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: "capped_role", Name: "限额角色", Actor: "1001", MaxBindings: &limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.roles.Save(ctx, role, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := guardBind(t, f, "2001", role.ID); err != nil {
		t.Fatalf("bind first subject: %v", err)
	}
	_, err = guardBind(t, f, "2002", role.ID)
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want 40900", err)
	}
	var constraintErr *app.ConstraintError
	if !errors.As(err, &constraintErr) || len(constraintErr.Violations) == 0 ||
		constraintErr.Violations[0].Kind != domain.ConstraintKindCardinality {
		t.Fatalf("err=%v violations=%+v", err, constraintErr)
	}
}

func TestConstraintGuardPrerequisite(t *testing.T) {
	f := newFixtureWithConstraintMode(t, app.ConstraintModeEnforce)
	ctx := context.Background()
	base := guardCreateRole(t, f, "prereq_base")
	gated := guardCreateRole(t, f, "prereq_gated")
	if err := f.roleConstraints.Replace(ctx, gated, []string{base.ID}, "1001"); err != nil {
		t.Fatal(err)
	}

	_, err := guardBind(t, f, "2001", gated.ID)
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want 40900", err)
	}
	var constraintErr *app.ConstraintError
	if !errors.As(err, &constraintErr) || len(constraintErr.Violations) == 0 ||
		constraintErr.Violations[0].Kind != domain.ConstraintKindPrerequisite {
		t.Fatalf("err=%v violations=%+v", err, constraintErr)
	}

	if _, err := guardBind(t, f, "2001", base.ID); err != nil {
		t.Fatalf("bind prerequisite: %v", err)
	}
	if _, err := guardBind(t, f, "2001", gated.ID); err != nil {
		t.Fatalf("bind gated after prerequisite: %v", err)
	}
}

func TestConstraintGuardPrerequisiteSatisfiedByAncestor(t *testing.T) {
	f := newFixtureWithConstraintMode(t, app.ConstraintModeEnforce)
	ctx := context.Background()
	base := guardCreateRole(t, f, "prereq_ancestor")
	child := guardCreateRole(t, f, "prereq_child", base.ID)
	gated := guardCreateRole(t, f, "prereq_descendant")
	if err := f.roleConstraints.Replace(ctx, gated, []string{base.ID}, "1001"); err != nil {
		t.Fatal(err)
	}

	// 持有子角色即通过继承持有 base，前置条件视为满足。
	if _, err := guardBind(t, f, "2001", child.ID); err != nil {
		t.Fatalf("bind child: %v", err)
	}
	if _, err := guardBind(t, f, "2001", gated.ID); err != nil {
		t.Fatalf("ancestor must satisfy prerequisite: %v", err)
	}
}

func TestConstraintGuardBlocksStructuralHierarchyConflict(t *testing.T) {
	f := newFixtureWithConstraintMode(t, app.ConstraintModeEnforce)
	ctx := context.Background()
	roleA := guardCreateRole(t, f, "hierarchy_a")
	roleB := guardCreateRole(t, f, "hierarchy_b")
	guardCreateSodGroup(t, f, "8104", "hierarchy_dual", []string{roleA.ID, roleB.ID})

	parentRef := roleA.ID
	_, err := f.roleSvc.UpdateRole(ctx, app.UpdateRoleCommand{
		ID: roleB.ID, ParentID: &parentRef, Version: roleB.Version, Actor: "1001", Reason: "调整层级",
	})
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want 40900", err)
	}
	var constraintErr *app.ConstraintError
	if !errors.As(err, &constraintErr) || len(constraintErr.Violations) == 0 ||
		constraintErr.Violations[0].Kind != domain.ConstraintKindHierarchy {
		t.Fatalf("err=%v violations=%+v", err, constraintErr)
	}
}

func TestConstraintGuardShadowModeAllowsHierarchyConflict(t *testing.T) {
	f := newFixtureWithConstraintMode(t, app.ConstraintModeShadow)
	ctx := context.Background()
	roleA := guardCreateRole(t, f, "shadow_hierarchy_a")
	roleB := guardCreateRole(t, f, "shadow_hierarchy_b")
	guardCreateSodGroup(t, f, "8105", "shadow_hierarchy_dual", []string{roleA.ID, roleB.ID})

	parentRef := roleA.ID
	if _, err := f.roleSvc.UpdateRole(ctx, app.UpdateRoleCommand{
		ID: roleB.ID, ParentID: &parentRef, Version: roleB.Version, Actor: "1001", Reason: "调整层级",
	}); err != nil {
		t.Fatalf("shadow mode must not block reparent: %v", err)
	}
}

func TestConstraintGuardBlocksDeleteReferencedRole(t *testing.T) {
	f := newFixtureWithConstraintMode(t, app.ConstraintModeEnforce)
	ctx := context.Background()
	roleA := guardCreateRole(t, f, "delete_a")
	roleB := guardCreateRole(t, f, "delete_b")
	group := guardCreateSodGroup(t, f, "8106", "delete_dual", []string{roleA.ID, roleB.ID})

	err := f.roleSvc.DeleteRole(ctx, app.DeleteRoleCommand{
		ID: roleA.ID, Version: roleA.Version, Actor: "1001", Reason: "清理",
	})
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want 40900", err)
	}

	loaded, err := f.sodGroups.FindByID(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Disable("1001", loaded.Version); err != nil {
		t.Fatal(err)
	}
	if err := f.sodGroups.Save(ctx, loaded, group.Version); err != nil {
		t.Fatal(err)
	}
	if err := f.roleSvc.DeleteRole(ctx, app.DeleteRoleCommand{
		ID: roleA.ID, Version: roleA.Version, Actor: "1001", Reason: "清理",
	}); err != nil {
		t.Fatalf("disabled sod group must not block delete: %v", err)
	}
}

func TestConstraintGuardBlocksDeletePrerequisiteRole(t *testing.T) {
	f := newFixtureWithConstraintMode(t, app.ConstraintModeEnforce)
	ctx := context.Background()
	base := guardCreateRole(t, f, "delete_base")
	gated := guardCreateRole(t, f, "delete_gated")
	if err := f.roleConstraints.Replace(ctx, gated, []string{base.ID}, "1001"); err != nil {
		t.Fatal(err)
	}

	err := f.roleSvc.DeleteRole(ctx, app.DeleteRoleCommand{
		ID: base.ID, Version: base.Version, Actor: "1001", Reason: "清理",
	})
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want 40900", err)
	}
	if err := f.roleConstraints.Replace(ctx, gated, nil, "1001"); err != nil {
		t.Fatal(err)
	}
	if err := f.roleSvc.DeleteRole(ctx, app.DeleteRoleCommand{
		ID: base.ID, Version: base.Version, Actor: "1001", Reason: "清理",
	}); err != nil {
		t.Fatalf("delete after prerequisite removed: %v", err)
	}
}
