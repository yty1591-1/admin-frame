package app_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// intPtr 返回整数指针，便于构造 max_bindings。
func intPtr(value int) *int { return &value }

// updateRoleConstraint 以 A 端 staff 身份调用约束更新。
func updateRoleConstraint(
	t *testing.T, f *fixture, roleID string, required []string, maxBindings *int, version int64,
) (*app.RoleConstraintView, error) {
	t.Helper()
	return f.roleConstraintSvc.Update(context.Background(), app.UpdateRoleConstraintCommand{
		RoleID:          roleID,
		Channel:         domain.ChannelAdmin,
		RequiredRoleIDs: required,
		MaxBindings:     maxBindings,
		Version:         version,
		Actor:           "1001",
		Reason:          "test constraint",
	})
}

func TestRoleConstraintUpdatePersistsAndAudits(t *testing.T) {
	f := newFixture(t)
	target := guardCreateRole(t, f, "constraint_target")
	prereq := guardCreateRole(t, f, "constraint_prereq")

	view, err := updateRoleConstraint(t, f, target.ID, []string{prereq.ID}, intPtr(5), target.Version)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if view.MaxBindings == nil || *view.MaxBindings != 5 {
		t.Fatalf("max_bindings=%v, want 5", view.MaxBindings)
	}
	if len(view.RequiredRoleIDs) != 1 || view.RequiredRoleIDs[0] != prereq.ID {
		t.Fatalf("required=%v, want [%s]", view.RequiredRoleIDs, prereq.ID)
	}

	persisted, err := f.roles.FindByID(context.Background(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.MaxBindings == nil || *persisted.MaxBindings != 5 {
		t.Fatalf("persisted max_bindings=%v", persisted.MaxBindings)
	}
	constraint, err := f.roleConstraints.ListByRole(context.Background(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraint.RequiredRoleIDs) != 1 || constraint.RequiredRoleIDs[0] != prereq.ID {
		t.Fatalf("persisted constraint=%v", constraint.RequiredRoleIDs)
	}

	event := f.recorder.last(t)
	if event.Action != audit.ActionRBACRoleChange {
		t.Fatalf("action=%s", event.Action)
	}
	if event.Attrs["operation"] != "update_constraints" {
		t.Fatalf("operation=%s", event.Attrs["operation"])
	}
	if event.Attrs["max_bindings"] != "5" {
		t.Fatalf("attrs max_bindings=%s", event.Attrs["max_bindings"])
	}
	if event.Attrs["role_ids"] != prereq.ID {
		t.Fatalf("attrs role_ids=%s", event.Attrs["role_ids"])
	}

	got, err := f.roleConstraintSvc.Get(context.Background(), target.ID, domain.ChannelAdmin, "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != view.Version || len(got.RequiredRoleIDs) != 1 {
		t.Fatalf("Get=%+v, want %+v", got, view)
	}

	// 清空前置与上限。
	cleared, err := updateRoleConstraint(t, f, target.ID, nil, nil, view.Version)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.MaxBindings != nil || len(cleared.RequiredRoleIDs) != 0 {
		t.Fatalf("cleared=%+v", cleared)
	}
}

func TestRoleConstraintRejectsSelfAndCycle(t *testing.T) {
	f := newFixture(t)
	a := guardCreateRole(t, f, "cycle_a")
	b := guardCreateRole(t, f, "cycle_b")

	if _, err := updateRoleConstraint(t, f, a.ID, []string{a.ID}, nil, a.Version); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("self prerequisite err=%v, want 40000", err)
	}
	if _, err := updateRoleConstraint(t, f, a.ID, []string{b.ID}, nil, a.Version); err != nil {
		t.Fatalf("a requires b: %v", err)
	}
	if _, err := updateRoleConstraint(t, f, b.ID, []string{a.ID}, nil, b.Version); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("cycle err=%v, want 40000", err)
	}
}

func TestRoleConstraintRejectsSodConflict(t *testing.T) {
	f := newFixture(t)
	a := guardCreateRole(t, f, "constraint_sod_a")
	b := guardCreateRole(t, f, "constraint_sod_b")
	guardCreateSodGroup(t, f, "8201", "constraint_dual", []string{a.ID, b.ID})

	if _, err := updateRoleConstraint(t, f, a.ID, []string{b.ID}, nil, a.Version); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("err=%v, want 40000", err)
	}
}

func TestRoleConstraintRejectsMaxBindingsBelowEffective(t *testing.T) {
	f := newFixture(t)
	role := guardCreateRole(t, f, "constraint_cardinality")
	for _, subjectID := range []string{"3001", "3002"} {
		if _, err := guardBind(t, f, subjectID, role.ID); err != nil {
			t.Fatalf("bind %s: %v", subjectID, err)
		}
	}
	if _, err := updateRoleConstraint(t, f, role.ID, nil, intPtr(1), role.Version); bizCode(err) != apperr.CodeConflict {
		t.Fatalf("err=%v, want 40900", err)
	}
	if _, err := updateRoleConstraint(t, f, role.ID, nil, intPtr(0), role.Version); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("max=0 err=%v, want 40000", err)
	}
	if _, err := updateRoleConstraint(t, f, role.ID, nil, intPtr(2), role.Version); err != nil {
		t.Fatalf("max=2: %v", err)
	}
}

func TestRoleConstraintTenantIsolation(t *testing.T) {
	f := newFixture(t)
	role := guardCreateRole(t, f, "constraint_tenant")

	if _, err := f.roleConstraintSvc.Get(context.Background(), role.ID, domain.ChannelBiz, "m_001"); bizCode(err) != apperr.CodeNotFound {
		t.Fatalf("cross tenant get err=%v, want 40400", err)
	}
	_, err := f.roleConstraintSvc.Update(context.Background(), app.UpdateRoleConstraintCommand{
		RoleID:     role.ID,
		Channel:    domain.ChannelBiz,
		MerchantID: "m_001",
		Version:    role.Version,
		Actor:      "1001",
		Reason:     "test",
	})
	if bizCode(err) != apperr.CodeNotFound {
		t.Fatalf("cross tenant update err=%v, want 40400", err)
	}
}

func TestRoleConstraintUpdateThroughApproval(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target := guardCreateRole(t, f, "constraint_approved")
	prereq := guardCreateRole(t, f, "constraint_approved_prereq")

	payload, err := json.Marshal(map[string]any{
		"required_role_ids": []string{prereq.ID},
		"max_bindings":      3,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := f.changeSvc.Create(ctx, app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleConstraintUpdate,
		TargetID: target.ID,
		Payload:  payload,
		Reason:   "tighten role constraints",
		Actor:    "2001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.changeSvc.Approve(ctx, request.ID, request.Version, "2002", "", app.ChangeScope{
		Channel: domain.ChannelAdmin,
	}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	applied, err := f.changeSvc.Apply(ctx, app.ApplyChangeRequestCommand{
		ID:      request.ID,
		Version: 2,
		Actor:   "2002",
		Scope:   app.ChangeScope{Channel: domain.ChannelAdmin},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != domain.ChangeStatusApplied {
		t.Fatalf("status=%s", applied.Status)
	}

	view, err := f.roleConstraintSvc.Get(ctx, target.ID, domain.ChannelAdmin, "")
	if err != nil {
		t.Fatal(err)
	}
	if view.MaxBindings == nil || *view.MaxBindings != 3 {
		t.Fatalf("max_bindings=%v, want 3", view.MaxBindings)
	}
	if len(view.RequiredRoleIDs) != 1 || view.RequiredRoleIDs[0] != prereq.ID {
		t.Fatalf("required=%v", view.RequiredRoleIDs)
	}
}
