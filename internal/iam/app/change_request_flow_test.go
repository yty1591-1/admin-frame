package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// createTestRole 在夹具中创建一个已落库的平台角色。
func createTestRole(t *testing.T, f *fixture, code string) *domain.Role {
	t.Helper()
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		Code:        code,
		Name:        "测试角色 " + code,
		Permissions: []iam.PermissionCode{iam.PermOrderRead},
		DataScopes:  []domain.DataScope{scope(t, domain.DataScopeMerchant)},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("CreateRole(%s): %v", code, err)
	}
	return role
}

// createReplacePermissionsRequest 构造并落库一张权限替换审批单。
func createReplacePermissionsRequest(t *testing.T, f *fixture, roleID, actor string) *domain.ChangeRequest {
	t.Helper()
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRolePermissionReplace,
		TargetID: roleID,
		Payload: []byte(`{"permissions":["order:read","order:write"],` +
			`"data_scopes":[{"resource":"order","scope_type":"merchant"}]}`),
		Reason: "补授写入权限",
		Actor:  actor,
	})
	if err != nil {
		t.Fatalf("Create change request: %v", err)
	}
	return request
}

func TestChangeRequestFullFlowAppliesPermissions(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request := createReplacePermissionsRequest(t, f, role.ID, "1001")
	if request.Status != domain.ChangeStatusPending || request.Version != 1 {
		t.Fatalf("created request = %+v", request)
	}

	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !approved.IsApproved() || approved.ApprovedBy != "1002" {
		t.Fatalf("approved = %+v", approved)
	}

	applied, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != domain.ChangeStatusApplied || applied.AppliedAt == nil {
		t.Fatalf("applied = %+v", applied)
	}

	updated, err := f.roles.FindByID(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !updated.HasPermission(iam.PermOrderWrite) {
		t.Fatalf("permissions = %v, want order:write granted", updated.PermissionCodes())
	}
	if updated.Version <= role.Version {
		t.Fatalf("role version = %d, want > %d", updated.Version, role.Version)
	}
	// 审批单本身不写审计：真实变更由 RoleService 记一条 rbac.role_change。
	if f.recorder.count() != 2 {
		t.Fatalf("audit events = %d, want 2 (create role + replace permissions)", f.recorder.count())
	}
	event := f.recorder.last(t)
	if event.Action != "rbac.role_change" || event.Attrs["operation"] != "replace_permissions" {
		t.Fatalf("audit event = %+v", event)
	}
}

func TestChangeRequestApproveEnforcesSeparationOfDuties(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request := createReplacePermissionsRequest(t, f, role.ID, "1001")

	_, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1001")
	if !errors.Is(err, domain.ErrSelfApproval) {
		t.Fatalf("err = %v, want self approval rejection", err)
	}
	if bizCode(err) != apperr.CodeForbidden {
		t.Fatalf("code = %d, want %d", bizCode(err), apperr.CodeForbidden)
	}
	stored, err := f.changes.FindByID(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !stored.IsPending() || stored.Version != 1 {
		t.Fatalf("stored request must stay pending, got %+v", stored)
	}
}

func TestChangeRequestApplyRequiresApproval(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request := createReplacePermissionsRequest(t, f, role.ID, "1001")

	_, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      request.ID,
		Version: request.Version,
		Actor:   "1002",
	})
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("err = %v, want conflict", err)
	}
	// 未审批通过时目标角色不得被修改。
	stored, err := f.roles.FindByID(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored.Version != role.Version {
		t.Fatalf("role must not change, version = %d", stored.Version)
	}
}

func TestChangeRequestApplyIsVersionGuarded(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request := createReplacePermissionsRequest(t, f, role.ID, "1001")
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	if _, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version + 5,
		Actor:   "1002",
	}); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("err = %v, want conflict", err)
	}
}

func TestChangeRequestApplyFailureKeepsApprovedState(t *testing.T) {
	f := newFixture(t)
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRolePermissionReplace,
		TargetID: "role-missing",
		Payload:  []byte(`{"permissions":["order:read"],"data_scopes":[{"resource":"order","scope_type":"merchant"}]}`),
		Reason:   "目标不存在",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
	}); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
	stored, err := f.changes.FindByID(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !stored.IsApproved() {
		t.Fatalf("status = %s, want approved (retryable)", stored.Status)
	}
}

func TestChangeRequestCancelRequiresRequester(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request := createReplacePermissionsRequest(t, f, role.ID, "1001")

	if _, err := f.changeSvc.Cancel(context.Background(), request.ID, request.Version, "1002"); !errors.Is(err, domain.ErrNotRequester) {
		t.Fatalf("err = %v", err)
	}
	cancelled, err := f.changeSvc.Cancel(context.Background(), request.ID, request.Version, "1001")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != domain.ChangeStatusCancelled {
		t.Fatalf("status = %s", cancelled.Status)
	}
}

func TestChangeRequestRejectFlow(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request := createReplacePermissionsRequest(t, f, role.ID, "1001")

	rejected, err := f.changeSvc.Reject(context.Background(), request.ID, request.Version, "1002")
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if rejected.Status != domain.ChangeStatusRejected || rejected.ApprovedBy != "1002" {
		t.Fatalf("rejected = %+v", rejected)
	}
	if _, err := f.changeSvc.Approve(context.Background(), rejected.ID, rejected.Version, "1003"); !errors.Is(err, domain.ErrChangeNotPending) {
		t.Fatalf("err = %v", err)
	}
}

func TestChangeRequestCreateGuardsChannelAndBreakglass(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.changeSvc.Create(ctx, app.CreateChangeRequestCommand{
		Channel: domain.ChannelBiz,
		Action:  domain.ChangeActionRoleCreate,
		Payload: []byte(`{"code":"owner","name":"owner"}`),
		Reason:  "B 端自助",
		Actor:   "m_001",
	}); !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("biz channel err = %v, want forbidden", err)
	}

	if _, err := f.changeSvc.Create(ctx, app.CreateChangeRequestCommand{
		Channel: domain.ChannelAdmin,
		Action:  domain.ChangeActionBreakglassRequest,
		Payload: []byte(`{}`),
		Reason:  "紧急排障",
		Actor:   "1001",
	}); !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("breakglass err = %v, want forbidden", err)
	}

	if _, err := f.changeSvc.Create(ctx, app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleUpdate,
		TargetID: "role-1",
		Actor:    "1001",
	}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("missing reason err = %v, want bad request", err)
	}
}

func TestChangeRequestRoleCreateBindsTargetOnApply(t *testing.T) {
	f := newFixture(t)
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel: domain.ChannelAdmin,
		Action:  domain.ChangeActionRoleCreate,
		Payload: []byte(`{"scope_type":"platform","code":"auditor","name":"审计员",` +
			`"permissions":["order:read"],"data_scopes":[{"resource":"order","scope_type":"merchant"}]}`),
		Reason: "新建审计角色",
		Actor:  "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	applied, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.TargetID == "" || applied.TargetType != domain.ChangeTargetIAMRole {
		t.Fatalf("target = %q/%q", applied.TargetType, applied.TargetID)
	}
	role, err := f.roles.FindByID(context.Background(), applied.TargetID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if role.Code != "auditor" || role.CreatedBy != "1002" {
		t.Fatalf("role = %+v", role)
	}
}

func TestChangeRequestBindingCreateApplies(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")

	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel: domain.ChannelAdmin,
		Action:  domain.ChangeActionBindingCreate,
		Payload: []byte(`{"role_id":"` + role.ID + `","subject_type":"staff","subject_id":"2001"}`),
		Reason:  "新人入职授权",
		Actor:   "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	applied, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.TargetID == "" || applied.TargetType != domain.ChangeTargetIAMBbinding {
		t.Fatalf("target = %q/%q", applied.TargetType, applied.TargetID)
	}
	binding, err := f.bindings.FindByID(context.Background(), applied.TargetID)
	if err != nil {
		t.Fatalf("FindByID binding: %v", err)
	}
	if binding.SubjectID != "2001" || binding.RoleID != role.ID {
		t.Fatalf("binding = %+v", binding)
	}
}

func TestChangeRequestRejectsUnknownPayloadFields(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")

	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRolePermissionReplace,
		TargetID: role.ID,
		// 字段名拼错（permission 而非 permissions）：必须报错而不是静默变成空权限。
		Payload: []byte(`{"permission":["order:read"]}`),
		Reason:  "拼写错误",
		Actor:   "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
	}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("err = %v, want bad request", err)
	}
}

func TestChangeRequestListFiltersAndIsolatesChannel(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request := createReplacePermissionsRequest(t, f, role.ID, "1001")

	list, err := f.changeSvc.List(context.Background(), domain.ChangeRequestFilter{
		Channel: domain.ChannelAdmin,
		Status:  domain.ChangeStatusPending,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != request.ID {
		t.Fatalf("list = %d", len(list))
	}
	if _, err := f.changeSvc.Get(context.Background(), request.ID, domain.ChannelBiz); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross channel err = %v", err)
	}
	if _, err := f.changeSvc.List(context.Background(), domain.ChangeRequestFilter{Limit: 101}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("limit err = %v", err)
	}
}
