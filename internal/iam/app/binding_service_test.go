package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// createRole 创建无数据依赖的角色（不触发 data_scopes 校验）。
func createRole(t *testing.T, f *fixture, code string) *domain.Role {
	t.Helper()
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		Code:        code,
		Name:        code,
		Permissions: []iam.PermissionCode{iam.PermRoleRead},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("create role %s: %v", code, err)
	}
	return role
}

func TestBindLoadsSnapshotAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	role := createRole(t, f, "ops")

	binding, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, Actor: "1001",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	snapshot, err := f.snapshots.Load(context.Background(), domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "2001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.HasPermission(iam.PermRoleRead) {
		t.Fatalf("snapshot permissions = %v", snapshot.Permissions)
	}
	if len(snapshot.Roles) != 1 || snapshot.Roles[0] != "ops" {
		t.Fatalf("roles = %v", snapshot.Roles)
	}
	if f.invalidator.count() != 1 {
		t.Fatalf("invalidations = %d, want 1", f.invalidator.count())
	}
	event := f.recorder.last(t)
	if event.Action != audit.ActionRBACBindingChange || event.Attrs["operation"] != "bind" {
		t.Fatalf("audit = %+v", event)
	}

	// 重复绑定幂等：返回既有记录，不新增额度、不重复审计。
	again, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, Actor: "1001",
	})
	if err != nil {
		t.Fatalf("Bind again: %v", err)
	}
	if again.ID != binding.ID {
		t.Fatalf("binding id = %s, want %s", again.ID, binding.ID)
	}
	if f.recorder.count() != 2 {
		t.Fatalf("audit events = %d, want 2 (create + bind)", f.recorder.count())
	}
}

func TestBindReplacesExpiredBindingAndSkipsQuota(t *testing.T) {
	f := newFixture(t)
	role := createRole(t, f, "ops")
	expired := time.Now().UTC().Add(-time.Minute)
	// 直接写仓储：模拟历史遗留的过期绑定（不占额度、不作为幂等结果）。
	if err := f.bindings.Save(context.Background(), &domain.Binding{
		ID: "expired-1", Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, ExpiresAt: &expired,
		GrantedBy: "1001", CreatedAt: expired.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	binding, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, Actor: "1001",
	})
	if err != nil {
		t.Fatalf("Bind over expired: %v", err)
	}
	if binding.ID != "expired-1" {
		t.Fatalf("binding id = %s, want persisted id expired-1", binding.ID)
	}
	if binding.ExpiresAt != nil || !binding.IsEffective(time.Now().UTC()) {
		t.Fatalf("expired binding must be refreshed, got %+v", binding)
	}
	snapshot, err := f.snapshots.Load(context.Background(), domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "2001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.HasPermission(iam.PermRoleRead) {
		t.Fatalf("rebound snapshot = %v", snapshot.Permissions)
	}
}

func TestBindRejectsCrossChannelAndDisabledRole(t *testing.T) {
	f := newFixture(t)
	role := createRole(t, f, "ops")

	_, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelBiz, SubjectType: domain.SubjectMerchantStaff,
		SubjectID: "2001", MerchantID: "m_001", RoleID: role.ID, Actor: "1001",
	})
	if bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("cross-channel err = %v, want 40000", err)
	}

	disabled := string(domain.RoleStatusDisabled)
	updated, err := f.roleSvc.UpdateRole(context.Background(), app.UpdateRoleCommand{
		ID: role.ID, Status: &disabled, Version: role.Version, Actor: "1001", Reason: "rotate",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, Actor: "1001",
	})
	if bizCode(err) != apperr.CodeConflict {
		t.Fatalf("disabled role err = %v (version %d), want 40900", err, updated.Version)
	}
}

func TestBindEnforcesSubjectLimit(t *testing.T) {
	f := newFixture(t)
	first := createRole(t, f, "first")
	second := createRole(t, f, "second")
	third := createRole(t, f, "third")

	for _, role := range []*domain.Role{first, second} {
		if _, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
			Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
			SubjectID: "2001", RoleID: role.ID, Actor: "1001",
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: third.ID, Actor: "1001",
	})
	if bizCode(err) != apperr.CodeTooManyRequests {
		t.Fatalf("limit err = %v, want 42900", err)
	}
}

func TestBindValidatesExpiry(t *testing.T) {
	f := newFixture(t)
	role := createRole(t, f, "ops")
	past := time.Now().Add(-time.Minute)
	_, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, ExpiresAt: &past, Actor: "1001",
	})
	if bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("past expiry err = %v, want 40000", err)
	}
}

func TestUnbindIsIdempotentAndAudited(t *testing.T) {
	f := newFixture(t)
	role := createRole(t, f, "ops")
	binding, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.bindingSvc.Unbind(context.Background(), app.UnbindCommand{
		ID: binding.ID, Actor: "1001", Reason: "offboarding",
	}); err != nil {
		t.Fatalf("Unbind: %v", err)
	}
	event := f.recorder.last(t)
	if event.Attrs["operation"] != "unbind" || event.Reason != "offboarding" {
		t.Fatalf("audit = %+v", event)
	}
	// 再次解绑：不存在的 ID 直接成功，且不写审计。
	before := f.recorder.count()
	if err := f.bindingSvc.Unbind(context.Background(), app.UnbindCommand{
		ID: binding.ID, Actor: "1001", Reason: "retry",
	}); err != nil {
		t.Fatalf("idempotent Unbind: %v", err)
	}
	if f.recorder.count() != before {
		t.Fatalf("idempotent unbind must not write audit")
	}
}

func TestUnbindRequiresReason(t *testing.T) {
	f := newFixture(t)
	role := createRole(t, f, "ops")
	binding, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.bindingSvc.Unbind(context.Background(), app.UnbindCommand{
		ID: binding.ID, Actor: "1001",
	}); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("err = %v, want 40000", err)
	}
}

func TestUnbindHidesCrossMerchantBinding(t *testing.T) {
	f := newFixture(t)
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel:     domain.ChannelBiz,
		Scope:       domain.RoleScopeMerchant,
		MerchantID:  "m_001",
		Code:        "merchant_ops",
		Name:        "merchant ops",
		Permissions: []iam.PermissionCode{iam.PermRoleRead},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelBiz, SubjectType: domain.SubjectMerchantStaff,
		SubjectID: "2001", MerchantID: "m_001", RoleID: role.ID, Actor: "1001",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.bindingSvc.Unbind(context.Background(), app.UnbindCommand{
		ID: binding.ID, MerchantID: "m_002", Actor: "1001", Reason: "cross tenant",
	})
	if bizCode(err) != apperr.CodeNotFound {
		t.Fatalf("cross-merchant err = %v, want 40400", err)
	}
}

func TestSimulateAuthorizeUsesSnapshot(t *testing.T) {
	f := newFixture(t)
	role := createRole(t, f, "ops")
	if _, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", RoleID: role.ID, Actor: "1001",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.querySvc.Simulate(context.Background(), app.SimulateQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", Permission: iam.PermRoleRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Allowed || result.Reason != app.ReasonAllowed {
		t.Fatalf("result = %+v", result)
	}
	if len(result.MatchedRoles) != 1 {
		t.Fatalf("roles = %v", result.MatchedRoles)
	}

	denied, err := f.querySvc.Simulate(context.Background(), app.SimulateQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", Permission: iam.PermOrderRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.Reason != app.ReasonPermissionNotHeld {
		t.Fatalf("result = %+v", denied)
	}

	unknown, err := f.querySvc.Simulate(context.Background(), app.SimulateQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", Permission: "order:delete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Allowed || unknown.Reason != app.ReasonUnknownPermission {
		t.Fatalf("result = %+v", unknown)
	}

	empty, err := f.querySvc.Simulate(context.Background(), app.SimulateQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "9999", Permission: iam.PermRoleRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Allowed || empty.Reason != app.ReasonEmptySnapshot {
		t.Fatalf("result = %+v", empty)
	}

	if _, err := f.querySvc.Simulate(context.Background(), app.SimulateQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "2001", MerchantID: "m_001", Permission: iam.PermRoleRead,
	}); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("admin with merchant err = %v, want 40000", err)
	}
}

func TestQueryRoleAndList(t *testing.T) {
	f := newFixture(t)
	role := createRole(t, f, "ops")
	detail, err := f.querySvc.GetRole(context.Background(), role.ID, domain.ChannelAdmin, "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Role.ID != role.ID || detail.BindingCount != 0 {
		t.Fatalf("detail = %+v", detail)
	}
	if _, err := f.querySvc.GetRole(context.Background(), role.ID, domain.ChannelBiz, "m_001"); bizCode(err) != apperr.CodeNotFound {
		t.Fatalf("cross-channel get err = %v, want 40400", err)
	}
	roles, err := f.querySvc.ListRoles(context.Background(), domain.RoleFilter{Channel: domain.ChannelAdmin, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 {
		t.Fatalf("roles = %d", len(roles))
	}
	if _, err := f.querySvc.ListRoles(context.Background(), domain.RoleFilter{Limit: 101}); bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("limit err = %v, want 40000", err)
	}
}

func TestPermissionCatalogQueries(t *testing.T) {
	f := newFixture(t)
	adminOnly := f.querySvc.Permissions(app.PermissionFilter{Channel: domain.ChannelAdmin})
	if len(adminOnly) == 0 {
		t.Fatal("expected permissions for admin")
	}
	bizOnly := f.querySvc.Permissions(app.PermissionFilter{Channel: domain.ChannelBiz, Keyword: "order"})
	if len(bizOnly) != 2 {
		t.Fatalf("biz order permissions = %d, want 2", len(bizOnly))
	}
	tree := f.querySvc.PermissionTree(app.PermissionFilter{})
	if len(tree) != len(f.querySvc.Permissions(app.PermissionFilter{})) {
		t.Fatalf("tree roots = %d; baseline catalog has no parents", len(tree))
	}
}

func TestSnapshotRepositoryRejectsInvalidQuery(t *testing.T) {
	f := newFixture(t)
	_, err := f.snapshots.Load(context.Background(), domain.SubjectQuery{
		Channel: domain.ChannelBiz, SubjectType: domain.SubjectMerchantStaff, SubjectID: "1",
	})
	if bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("err = %v, want 40000", err)
	}
}
