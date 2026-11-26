package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
)

// adminScope 是 A 端审批操作的作用域（平台级，无商户）。
var adminScope = app.ChangeScope{Channel: domain.ChannelAdmin}

// createTestRole 在夹具中创建一个已落库的平台角色。
func createTestRole(t *testing.T, f *fixture, code string) *domain.Role {
	t.Helper()
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		Code:        code,
		Name:        "测试角色 " + code,
		Permissions: []iam.PermissionCode{iam.PermRoleRead},
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
		Payload: []byte(`{"permissions":["iam:role:read","iam:role:write"],` +
			`"data_scopes":[{"resource":"iam","scope_type":"merchant"}]}`),
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

	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002", "", adminScope)
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
		Scope:   adminScope,
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
	if !updated.HasPermission(iam.PermRoleWrite) {
		t.Fatalf("permissions = %v, want iam:role:write granted", updated.PermissionCodes())
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

// createTemplate 在夹具中创建一个 A 端模板基线。
func createTemplate(t *testing.T, f *fixture, code string) *domain.RoleTemplate {
	t.Helper()
	template, err := f.templateSvc.CreateTemplate(context.Background(), app.CreateTemplateCommand{
		Channel:     domain.ChannelAdmin,
		Code:        code,
		Name:        "模板 " + code,
		Permissions: []iam.PermissionCode{iam.PermRoleRead},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("CreateTemplate(%s): %v", code, err)
	}
	return template
}

// applyChangeRequest 走完审批与应用，返回最终审批单。
func applyChangeRequest(t *testing.T, f *fixture, request *domain.ChangeRequest) *domain.ChangeRequest {
	t.Helper()
	approved, err := f.changeSvc.Approve(
		context.Background(), request.ID, request.Version, "1002", "", adminScope,
	)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	applied, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
		Scope:   adminScope,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return applied
}

// TestChangeRequestAppliesRoleTemplateCreate 验证模板创建可通过审批单落地并回填目标 ID。
func TestChangeRequestAppliesRoleTemplateCreate(t *testing.T) {
	f := newFixture(t)
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel: domain.ChannelAdmin,
		Action:  domain.ChangeActionRoleTemplateCreate,
		Payload: []byte(`{"channel":"admin","code":"tpl_ops","name":"运维基线","permissions":["iam:role:read"]}`),
		Reason:  "新增运维模板",
		Actor:   "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if request.TargetType != domain.ChangeTargetIAMRoleTemplate {
		t.Fatalf("target type = %q, want %q", request.TargetType, domain.ChangeTargetIAMRoleTemplate)
	}
	applied := applyChangeRequest(t, f, request)
	if applied.Status != domain.ChangeStatusApplied || applied.TargetID == "" {
		t.Fatalf("applied = %+v", applied)
	}
	template, err := f.templates.FindByID(context.Background(), applied.TargetID)
	if err != nil {
		t.Fatalf("FindByID template: %v", err)
	}
	if template.Code != "tpl_ops" || !template.Permissions.Contains(iam.PermRoleRead) {
		t.Fatalf("template = %+v", template)
	}
}

// TestChangeRequestAppliesRoleTemplatePermissionReplace 验证模板基线变更走审批后生效。
func TestChangeRequestAppliesRoleTemplatePermissionReplace(t *testing.T) {
	f := newFixture(t)
	template := createTemplate(t, f, "tpl_ops")
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleTemplatePermissionReplace,
		TargetID: template.ID,
		Payload:  []byte(`{"permissions":["iam:role:read","iam:role:write"]}`),
		Reason:   "模板补授写权限",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	applyChangeRequest(t, f, request)
	updated, err := f.templates.FindByID(context.Background(), template.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !updated.Permissions.Contains(iam.PermRoleWrite) {
		t.Fatalf("template permissions = %v", updated.PermissionCodes())
	}
	if updated.Version <= template.Version {
		t.Fatalf("template version = %d, want > %d", updated.Version, template.Version)
	}
}

// TestChangeRequestAppliesRoleTemplateSync 验证模板同步必须经审批，且能收敛漂移实例。
func TestChangeRequestAppliesRoleTemplateSync(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	template := createTemplate(t, f, "tpl_ops")
	role, err := f.templateSvc.Instantiate(ctx, app.InstantiateTemplateCommand{
		TemplateID: template.ID,
		Channel:    domain.ChannelAdmin,
		Scope:      domain.RoleScopePlatform,
		Code:       "ops_reader",
		Name:       "运维只读实例",
		Actor:      "1002",
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	// 先通过审批把模板基线扩到写权限，实例产生漂移。
	if _, err := f.changeSvc.Create(ctx, app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleTemplatePermissionReplace,
		TargetID: template.ID,
		Payload:  []byte(`{"permissions":["iam:role:read","iam:role:write"]}`),
		Reason:   "模板补授写权限",
		Actor:    "1001",
	}); err != nil {
		t.Fatalf("Create replace: %v", err)
	}
	pending, err := f.changes.List(ctx, domain.ChangeRequestFilter{Channel: domain.ChannelAdmin})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending requests = %d, want 1", len(pending))
	}
	applyChangeRequest(t, f, pending[0])

	syncRequest, err := f.changeSvc.Create(ctx, app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleTemplateSync,
		TargetID: template.ID,
		Payload:  []byte(`{}`),
		Reason:   "收敛模板漂移",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create sync: %v", err)
	}
	applyChangeRequest(t, f, syncRequest)

	stored, err := f.roles.FindByID(ctx, role.ID)
	if err != nil {
		t.Fatalf("FindByID role: %v", err)
	}
	if !stored.Permissions.Contains(iam.PermRoleWrite) {
		t.Fatalf("synced permissions = %v", stored.PermissionCodes())
	}
	if stored.TemplateVersion != 2 {
		t.Fatalf("template_version = %d, want 2", stored.TemplateVersion)
	}
}

// TestChangeRequestAppliesRoleTemplateInstantiate 验证实例化经审批后返回并回填新角色 ID。
func TestChangeRequestAppliesRoleTemplateInstantiate(t *testing.T) {
	f := newFixture(t)
	template := createTemplate(t, f, "tpl_ops")
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel: domain.ChannelAdmin,
		Action:  domain.ChangeActionRoleTemplateInstantiate,
		Payload: []byte(`{"template_id":"` + template.ID + `","code":"ops_reader","name":"运维只读实例"}`),
		Reason:  "按模板创建角色",
		Actor:   "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	applied := applyChangeRequest(t, f, request)
	if applied.TargetID == "" {
		t.Fatal("instantiated role id must be bound to the change request")
	}
	role, err := f.roles.FindByID(context.Background(), applied.TargetID)
	if err != nil {
		t.Fatalf("FindByID role: %v", err)
	}
	if role.TemplateID != template.ID || !role.Permissions.Contains(iam.PermRoleRead) {
		t.Fatalf("role = %+v", role)
	}
}

// TestChangeRequestTemplateDeleteRequiresTargetID 验证模板变更类动作必须携带目标 ID。
func TestChangeRequestTemplateDeleteRequiresTargetID(t *testing.T) {
	f := newFixture(t)
	_, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel: domain.ChannelAdmin,
		Action:  domain.ChangeActionRoleTemplateDelete,
		Payload: []byte(`{}`),
		Reason:  "删除模板",
		Actor:   "1001",
	})
	if !errors.Is(err, domain.ErrInvalidChangeTarget) {
		t.Fatalf("err = %v, want invalid change target", err)
	}
	if bizCode(err) != apperr.CodeBadRequest {
		t.Fatalf("code = %d, want %d", bizCode(err), apperr.CodeBadRequest)
	}
}

func TestChangeRequestApproveEnforcesSeparationOfDuties(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request := createReplacePermissionsRequest(t, f, role.ID, "1001")

	_, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1001", "", adminScope)
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

func TestChangeRequestApproveAllowsSystemAdminSelfReview(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request := createReplacePermissionsRequest(t, f, role.ID, "1001")
	ctx := authz.WithSnapshot(context.Background(), authz.Snapshot{
		Channel:   "admin",
		SubjectID: "1001",
		Roles:     []string{authz.RoleCodeSystemAdmin},
	})
	approved, err := f.changeSvc.Approve(ctx, request.ID, request.Version, "1001", "", adminScope)
	if err != nil {
		t.Fatalf("system_admin self approve: %v", err)
	}
	if !approved.IsApproved() || approved.ApprovedBy != "1001" {
		t.Fatalf("got %+v", approved)
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
		Scope:   adminScope,
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
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002", "", adminScope)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	if _, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version + 5,
		Actor:   "1002",
		Scope:   adminScope,
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
		Payload:  []byte(`{"permissions":["iam:role:read"],"data_scopes":[{"resource":"iam","scope_type":"merchant"}]}`),
		Reason:   "目标不存在",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002", "", adminScope)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
		Scope:   adminScope,
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

	if _, err := f.changeSvc.Cancel(context.Background(), request.ID, request.Version, "1002", adminScope); !errors.Is(err, domain.ErrNotRequester) {
		t.Fatalf("err = %v", err)
	}
	cancelled, err := f.changeSvc.Cancel(context.Background(), request.ID, request.Version, "1001", adminScope)
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

	rejected, err := f.changeSvc.Reject(context.Background(), request.ID, request.Version, "1002", "rejected in test", adminScope)
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if rejected.Status != domain.ChangeStatusRejected || rejected.ApprovedBy != "1002" {
		t.Fatalf("rejected = %+v", rejected)
	}
	if _, err := f.changeSvc.Approve(context.Background(), rejected.ID, rejected.Version, "1003", "", adminScope); !errors.Is(err, domain.ErrChangeNotPending) {
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
		t.Fatalf("biz channel without merchant err = %v, want forbidden", err)
	}

	// B 端审批默认关闭：即使作用域自洽也必须拒绝，避免半开状态下写入无主单据。
	if _, err := f.changeSvc.Create(ctx, app.CreateChangeRequestCommand{
		Channel:    domain.ChannelBiz,
		MerchantID: "m-1",
		Action:     domain.ChangeActionRoleCreate,
		Payload:    []byte(`{"code":"owner","name":"owner"}`),
		Reason:     "B 端自助",
		Actor:      "m_001",
	}); !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("biz channel disabled err = %v, want forbidden", err)
	}

	// A 端不得携带商户：否则审批单与平台级目标的作用域不自洽。
	if _, err := f.changeSvc.Create(ctx, app.CreateChangeRequestCommand{
		Channel:    domain.ChannelAdmin,
		MerchantID: "m-1",
		Action:     domain.ChangeActionRoleUpdate,
		TargetID:   "role-1",
		Reason:     "越权尝试",
		Actor:      "1001",
	}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("admin with merchant err = %v, want bad request", err)
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
			`"permissions":["iam:role:read"],"data_scopes":[{"resource":"iam","scope_type":"merchant"}]}`),
		Reason: "新建审计角色",
		Actor:  "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002", "", adminScope)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	applied, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
		Scope:   adminScope,
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
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002", "", adminScope)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	applied, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
		Scope:   adminScope,
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
		Payload: []byte(`{"permission":["iam:role:read"]}`),
		Reason:  "拼写错误",
		Actor:   "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002", "", adminScope)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
		Scope:   adminScope,
	}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("err = %v, want bad request", err)
	}
}

func TestChangeRequestListFiltersAndIsolatesChannel(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request := createReplacePermissionsRequest(t, f, role.ID, "1001")

	list, err := f.changeSvc.List(context.Background(), adminScope, domain.ChangeRequestFilter{
		Status: domain.ChangeStatusPending,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != request.ID {
		t.Fatalf("list = %d", len(list))
	}
	if _, err := f.changeSvc.Get(context.Background(), request.ID,
		app.ChangeScope{Channel: domain.ChannelBiz, MerchantID: "m-1"}); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross channel err = %v", err)
	}
	if _, err := f.changeSvc.List(context.Background(), adminScope,
		domain.ChangeRequestFilter{Limit: 101}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("limit err = %v", err)
	}
}

// approveAndApply 走完审批双人流程并返回执行后的单据。
func approveAndApply(t *testing.T, f *fixture, request *domain.ChangeRequest) *domain.ChangeRequest {
	t.Helper()
	approved, err := f.changeSvc.Approve(context.Background(), request.ID, request.Version, "1002", "", adminScope)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	applied, err := f.changeSvc.Apply(context.Background(), app.ApplyChangeRequestCommand{
		ID:      approved.ID,
		Version: approved.Version,
		Actor:   "1002",
		Scope:   adminScope,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return applied
}

// TestChangeRequestAppliesRoleClone 覆盖 role_clone 审批执行分支。
func TestChangeRequestAppliesRoleClone(t *testing.T) {
	f := newFixture(t)
	source := createTestRole(t, f, "ops_lead")
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleClone,
		TargetID: source.ID,
		Payload:  []byte(`{"code":"ops_lead_clone","name":"运维负责人副本"}`),
		Reason:   "复制角色基线",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	applied := approveAndApply(t, f, request)
	if applied.TargetID != source.ID {
		t.Fatalf("clone request target = %s, want source %s", applied.TargetID, source.ID)
	}
	role, err := f.roles.FindByCode(context.Background(), domain.ChannelAdmin, "", "ops_lead_clone")
	if err != nil {
		t.Fatalf("FindByCode: %v", err)
	}
	if role.ID == source.ID || role.Code != "ops_lead_clone" {
		t.Fatalf("cloned role = %+v", role)
	}
}

// TestChangeRequestAppliesRoleDelete 覆盖 role_delete 审批执行分支。
func TestChangeRequestAppliesRoleDelete(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleDelete,
		TargetID: role.ID,
		Reason:   "角色下线",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approveAndApply(t, f, request)
	deleted, err := f.roles.FindByID(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if deleted.DeletedAt == nil {
		t.Fatalf("role not soft deleted: %+v", deleted)
	}
}

// TestChangeRequestAppliesRoleTemplateUpdate 覆盖 role_template_update 审批执行分支。
func TestChangeRequestAppliesRoleTemplateUpdate(t *testing.T) {
	f := newFixture(t)
	template := createTemplate(t, f, "tpl_ops")
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleTemplateUpdate,
		TargetID: template.ID,
		Payload:  []byte(`{"name":"运维基线 v2"}`),
		Reason:   "模板改名",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approveAndApply(t, f, request)
	updated, err := f.templates.FindByID(context.Background(), template.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if updated.Name != "运维基线 v2" || updated.Version <= template.Version {
		t.Fatalf("template = %+v, want renamed and version bumped", updated)
	}
}

// TestChangeRequestAppliesRoleTemplateDelete 覆盖 role_template_delete 审批执行分支。
func TestChangeRequestAppliesRoleTemplateDelete(t *testing.T) {
	f := newFixture(t)
	template := createTemplate(t, f, "tpl_ops")
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleTemplateDelete,
		TargetID: template.ID,
		Payload:  []byte(`{}`),
		Reason:   "模板下线",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approveAndApply(t, f, request)
	deleted, err := f.templates.FindByID(context.Background(), template.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if deleted.DeletedAt == nil {
		t.Fatalf("template not soft deleted: %+v", deleted)
	}
}

// TestChangeRequestAppliesSodGroupUpdate 覆盖 sod_group_update 审批执行分支。
func TestChangeRequestAppliesSodGroupUpdate(t *testing.T) {
	f := newFixture(t)
	roleA := createTestRole(t, f, "finance_maker")
	roleB := createTestRole(t, f, "finance_checker")
	group, err := f.sodSvc.Create(context.Background(), app.CreateSodGroupCommand{
		Channel:        domain.ChannelAdmin,
		Code:           "finance_dual",
		Name:           "财务双人复核",
		MaxActiveRoles: 1,
		RoleIDs:        []string{roleA.ID, roleB.ID},
		Actor:          "1001",
		Reason:         "建立互斥基线",
	})
	if err != nil {
		t.Fatalf("Create sod group: %v", err)
	}
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionSodGroupUpdate,
		TargetID: group.ID,
		Payload:  []byte(`{"name":"财务双人复核 v2"}`),
		Reason:   "互斥组改名",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create change request: %v", err)
	}
	approveAndApply(t, f, request)
	updated, err := f.sodGroups.FindByID(context.Background(), group.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if updated.Name != "财务双人复核 v2" || updated.Version <= group.Version {
		t.Fatalf("group = %+v, want renamed and version bumped", updated)
	}
}

// TestChangeRequestAppliesSodGroupDelete 覆盖 sod_group_delete 审批执行分支。
func TestChangeRequestAppliesSodGroupDelete(t *testing.T) {
	f := newFixture(t)
	roleA := createTestRole(t, f, "finance_maker")
	roleB := createTestRole(t, f, "finance_checker")
	group, err := f.sodSvc.Create(context.Background(), app.CreateSodGroupCommand{
		Channel:        domain.ChannelAdmin,
		Code:           "finance_dual",
		Name:           "财务双人复核",
		MaxActiveRoles: 1,
		RoleIDs:        []string{roleA.ID, roleB.ID},
		Actor:          "1001",
		Reason:         "建立互斥基线",
	})
	if err != nil {
		t.Fatalf("Create sod group: %v", err)
	}
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionSodGroupDelete,
		TargetID: group.ID,
		Reason:   "互斥组下线",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create change request: %v", err)
	}
	approveAndApply(t, f, request)
	deleted, err := f.sodGroups.FindByID(context.Background(), group.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if deleted.DeletedAt == nil {
		t.Fatalf("sod group not soft deleted: %+v", deleted)
	}
}

// TestChangeRequestAppliesBindingDelete 覆盖 binding_delete 审批执行分支。
func TestChangeRequestAppliesBindingDelete(t *testing.T) {
	f := newFixture(t)
	role := createTestRole(t, f, "ops_lead")
	binding, err := f.bindingSvc.Bind(context.Background(), app.BindCommand{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "2001",
		RoleID:      role.ID,
		Actor:       "1001",
		Reason:      "临时授权",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	request, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionBindingDelete,
		TargetID: binding.ID,
		Reason:   "授权到期",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approveAndApply(t, f, request)
	revoked, err := f.bindings.FindByID(context.Background(), binding.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatalf("binding not revoked: %+v", revoked)
	}
}
