package rest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/iam/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/httpx"
)

// seqIDs 是确定性 ID 生成器，便于断言与排障。
type seqIDs struct {
	mu sync.Mutex
	n  int64
}

func (s *seqIDs) Next() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return s.n, nil
}

func (s *seqIDs) NextString() (string, error) {
	id, err := s.Next()
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

// noopRecorder 满足 app.ChangeRecorder；本包测试只关心 HTTP 层映射。
type noopRecorder struct{}

func (noopRecorder) Record(context.Context, audit.Event) error { return nil }

// noopInvalidator 满足 app.SnapshotInvalidator。
type noopInvalidator struct{}

func (noopInvalidator) InvalidateSubject(context.Context, string, string, string, string) error {
	return nil
}

// noopCertRevoker 满足 app.CertificationRevoker；REST 测试不关心回收细节。
type noopCertRevoker struct{}

func (noopCertRevoker) Revoke(context.Context, string, string, string) error { return nil }

// restFixture 是 IAM HTTP 层的端到端装配（真实用例 + 内存仓储）。
type restFixture struct {
	roles      *persistence.MemoryRoleRepository
	sodGroups  *persistence.MemorySodGroupRepository
	constrains *persistence.MemoryRoleConstraintRepository
	roleSvc    *app.RoleService
	bindingSvc *app.BindingService
	querySvc   *app.QueryService
	sodSvc     *app.SodGroupService
	changeSvc  *app.ChangeRequestService
	certSvc    *app.CertificationService
	handler    *rest.Handler
}

func newRestFixture(t *testing.T, channel domain.Channel, requireApproval bool) *restFixture {
	t.Helper()
	roles := persistence.NewMemoryRoleRepository()
	bindings := persistence.NewMemoryBindingRepository()
	sodGroups := persistence.NewMemorySodGroupRepository()
	constraints := persistence.NewMemoryRoleConstraintRepository()
	sodViolations := persistence.NewMemorySodViolationRepository(roles, bindings)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	recorder := noopRecorder{}
	ids := &seqIDs{}
	auditMeta := app.AuditMeta{Service: "test", ActorType: "staff"}

	guard, err := app.NewConstraintGuard(app.ConstraintGuardConfig{
		Mode:         app.ConstraintModeShadow,
		Roles:        roles,
		Bindings:     bindings,
		SodGroups:    sodGroups,
		Restrictions: constraints,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("NewConstraintGuard: %v", err)
	}

	roleSvc, err := app.NewRoleService(app.RoleServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    recorder,
		Invalidator: noopInvalidator{},
		IDs:         ids,
		Audit:       auditMeta,
		Limits:      app.RoleLimits{MaxPermissionsPerRole: 200, RequireReason: true},
		Constraints: guard,
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("NewRoleService: %v", err)
	}
	bindingSvc, err := app.NewBindingService(app.BindingServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    recorder,
		Invalidator: noopInvalidator{},
		IDs:         ids,
		Audit:       auditMeta,
		Limits:      app.BindingLimits{MaxBindingsPerSubject: 2, RequireReason: true},
		Constraints: guard,
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("NewBindingService: %v", err)
	}
	querySvc, err := app.NewQueryService(roles, bindings, persistence.NewMemorySnapshotRepository(roles, bindings))
	if err != nil {
		t.Fatalf("NewQueryService: %v", err)
	}
	sodSvc, err := app.NewSodGroupService(app.SodGroupServiceConfig{
		Groups:     sodGroups,
		Roles:      roles,
		Violations: sodViolations,
		Recorder:   recorder,
		IDs:        ids,
		Audit:      auditMeta,
		Limits:     app.SodGroupLimits{RequireReason: true},
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("NewSodGroupService: %v", err)
	}
	constraintSvc, err := app.NewRoleConstraintService(app.RoleConstraintServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Constraints: constraints,
		SodGroups:   sodGroups,
		Recorder:    recorder,
		IDs:         ids,
		Audit:       auditMeta,
		Limits:      app.RoleConstraintLimits{RequireReason: true},
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("NewRoleConstraintService: %v", err)
	}
	templateSvc, err := app.NewTemplateService(app.TemplateServiceConfig{
		Templates: persistence.NewMemoryRoleTemplateRepository(),
		Roles:     roleSvc,
		RoleRepo:  roles,
		Recorder:  recorder,
		IDs:       ids,
		Audit:     auditMeta,
		Limits:    app.RoleLimits{MaxPermissionsPerRole: 200, RequireReason: true},
		Logger:    logger,
	})
	if err != nil {
		t.Fatalf("NewTemplateService: %v", err)
	}
	applicator, err := app.NewChangeApplicator(roleSvc, bindingSvc, sodSvc, constraintSvc, templateSvc)
	if err != nil {
		t.Fatalf("NewChangeApplicator: %v", err)
	}
	changeSvc, err := app.NewChangeRequestService(app.ChangeRequestServiceConfig{
		Requests:   persistence.NewMemoryChangeRequestRepository(),
		Applicator: applicator,
		IDs:        ids,
		Limits:     app.ChangeLimits{RequireReason: true},
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("NewChangeRequestService: %v", err)
	}
	menuSvc, err := app.NewMenuService(
		persistence.NewMemoryMenuRepository(menuCandidate("system.monitor", "SystemMonitor", "https://example.com/iframe", domain.MenuNodeIFrame)...),
		persistence.NewMemorySnapshotRepository(roles, bindings),
	)
	if err != nil {
		t.Fatalf("NewMenuService: %v", err)
	}

	certSvc, err := app.NewCertificationService(app.CertificationServiceConfig{
		Campaigns: persistence.NewMemoryCertificationCampaignRepository(),
		Tasks:     persistence.NewMemoryCertificationTaskRepository(),
		Bindings:  bindings,
		Roles:     roles,
		Revoker:   noopCertRevoker{},
		Recorder:  recorder,
		IDs:       ids,
		Audit:     auditMeta,
		Limits:    app.CertificationLimits{RequireReason: true, AllowBizChannel: true},
	})
	if err != nil {
		t.Fatalf("NewCertificationService: %v", err)
	}

	handler, err := rest.NewHandler(rest.HandlerConfig{
		Roles:           roleSvc,
		Bindings:        bindingSvc,
		Query:           querySvc,
		Changes:         changeSvc,
		Templates:       templateSvc,
		Menus:           menuSvc,
		SodGroups:       sodSvc,
		RoleConstraints: constraintSvc,
		Certification:   certSvc,
		Channel:         channel,
		RequireApproval: requireApproval,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return &restFixture{
		roles:      roles,
		sodGroups:  sodGroups,
		constrains: constraints,
		roleSvc:    roleSvc,
		bindingSvc: bindingSvc,
		querySvc:   querySvc,
		sodSvc:     sodSvc,
		changeSvc:  changeSvc,
		certSvc:    certSvc,
		handler:    handler,
	}
}

// createRole 直接通过用例落库测试角色。
func (f *restFixture) createRole(t *testing.T, channel domain.Channel, code string) *domain.Role {
	t.Helper()
	role, err := f.roleSvc.CreateRole(context.Background(), app.CreateRoleCommand{
		Channel:     channel,
		Scope:       domain.RoleScopePlatform,
		Code:        code,
		Name:        "测试角色 " + code,
		Permissions: []iam.PermissionCode{iam.PermRoleRead},
		DataScopes:  []domain.DataScope{{Resource: "iam", Type: domain.DataScopeMerchant}},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("CreateRole(%s): %v", code, err)
	}
	return role
}

// principalOf 构造与 handler 端别一致的认证主体。
func principalOf(channel domain.Channel, subjectID, merchantID string) auth.Principal {
	return auth.Principal{Channel: auth.Channel(channel), SubjectID: subjectID, MerchantID: merchantID}
}

// call 执行一次 HTTP 调用并返回录制的响应。
func call(t *testing.T, handler http.HandlerFunc, r *http.Request, withPrincipal bool, principal auth.Principal) *httptest.ResponseRecorder {
	t.Helper()
	if withPrincipal {
		r = r.WithContext(auth.WithPrincipal(r.Context(), principal))
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

// newJSONRequest 构造 JSON 请求；pathValue 用于填充 {id} 路由参数。
func newJSONRequest(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	r := httptest.NewRequest(method, target, bytes.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// decodeEnvelope 解析统一响应信封。
func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder) httpx.Envelope {
	t.Helper()
	var envelope httpx.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, w.Body.String())
	}
	return envelope
}

// TestNewHandlerValidation 覆盖装配期依赖校验。
func TestNewHandlerValidation(t *testing.T) {
	if _, err := rest.NewHandler(rest.HandlerConfig{}); err == nil {
		t.Fatal("expected error for missing core services")
	}
	f := newRestFixture(t, domain.ChannelAdmin, false)
	if _, err := rest.NewHandler(rest.HandlerConfig{
		Roles:    f.roleSvc,
		Bindings: f.bindingSvc,
		Query:    f.querySvc,
		Changes:  f.changeSvc,
		Channel:  domain.Channel("unknown"),
	}); err == nil {
		t.Fatal("expected error for invalid channel")
	}
}

// TestSodGroupHTTPFlow 覆盖互斥组列表 / 详情 / 创建 / 更新 / 预检 / 违规 / 删除。
func TestSodGroupHTTPFlow(t *testing.T) {
	f := newRestFixture(t, domain.ChannelAdmin, false)
	roleA := f.createRole(t, domain.ChannelAdmin, "finance_maker")
	roleB := f.createRole(t, domain.ChannelAdmin, "finance_checker")
	principal := principalOf(domain.ChannelAdmin, "1001", "")

	created := call(t, f.handler.CreateSodGroup, newJSONRequest(t, http.MethodPost, "/iam/sod-groups", map[string]any{
		"code":             "finance_dual",
		"name":             "财务双人复核",
		"max_active_roles": 1,
		"role_ids":         []string{roleA.ID, roleB.ID},
		"reason":           "建立互斥基线",
	}), true, principal)
	if created.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	createdEnvelope := decodeEnvelope(t, created)
	groupID := dataID(t, createdEnvelope)

	listed := call(t, f.handler.ListSodGroups, httptest.NewRequest(http.MethodGet, "/iam/sod-groups?limit=10", http.NoBody), true, principal)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listed.Code, listed.Body.String())
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/iam/sod-groups/"+groupID, http.NoBody)
	detailReq.SetPathValue("id", groupID)
	detail := call(t, f.handler.GetSodGroup, detailReq, true, principal)
	if detail.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", detail.Code, detail.Body.String())
	}

	updatedReq := newJSONRequest(t, http.MethodPatch, "/iam/sod-groups/"+groupID, map[string]any{
		"name":    "财务双人复核 v2",
		"version": 1,
		"reason":  "互斥组改名",
	})
	updatedReq.SetPathValue("id", groupID)
	updated := call(t, f.handler.UpdateSodGroup, updatedReq, true, principal)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updated.Code, updated.Body.String())
	}

	preflight := call(t, f.handler.PreflightSodGroup, newJSONRequest(t, http.MethodPost, "/iam/sod-groups/preflight", map[string]any{
		"role_ids":         []string{roleA.ID, roleB.ID},
		"max_active_roles": 1,
	}), true, principal)
	if preflight.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, body = %s", preflight.Code, preflight.Body.String())
	}

	violationsReq := httptest.NewRequest(http.MethodGet, "/iam/sod-groups/"+groupID+"/violations?limit=10", http.NoBody)
	violationsReq.SetPathValue("id", groupID)
	violations := call(t, f.handler.ListSodGroupViolations, violationsReq, true, principal)
	if violations.Code != http.StatusOK {
		t.Fatalf("violations status = %d, body = %s", violations.Code, violations.Body.String())
	}

	deleteReq := newJSONRequest(t, http.MethodDelete, "/iam/sod-groups/"+groupID, map[string]any{
		"version": 2,
		"reason":  "互斥组下线",
	})
	deleteReq.SetPathValue("id", groupID)
	deleted := call(t, f.handler.DeleteSodGroup, deleteReq, true, principal)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	stored, err := f.sodGroups.FindByID(context.Background(), groupID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored.DeletedAt == nil {
		t.Fatalf("sod group not soft deleted: %+v", stored)
	}
}

// TestSodGroupHTTPGuards 覆盖未配置服务、未认证与 B 端缺失商户的分支。
func TestSodGroupHTTPGuards(t *testing.T) {
	f := newRestFixture(t, domain.ChannelAdmin, false)
	handler, err := rest.NewHandler(rest.HandlerConfig{
		Roles:    f.roleSvc,
		Bindings: f.bindingSvc,
		Query:    f.querySvc,
		Changes:  f.changeSvc,
		Channel:  domain.ChannelAdmin,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	if w := call(t, handler.ListSodGroups, httptest.NewRequest(http.MethodGet, "/iam/sod-groups", http.NoBody), true,
		principalOf(domain.ChannelAdmin, "1001", "")); w.Code != http.StatusInternalServerError {
		t.Fatalf("missing service status = %d, want 500", w.Code)
	}
	if w := call(t, handler.GetRoleConstraints, httptest.NewRequest(http.MethodGet, "/iam/roles/x/constraints", http.NoBody), true,
		principalOf(domain.ChannelAdmin, "1001", "")); w.Code != http.StatusInternalServerError {
		t.Fatalf("missing constraint service status = %d, want 500", w.Code)
	}
	if w := call(t, handler.Menus, httptest.NewRequest(http.MethodGet, "/iam/me/menus", http.NoBody), true,
		principalOf(domain.ChannelAdmin, "1001", "")); w.Code != http.StatusInternalServerError {
		t.Fatalf("missing menu service status = %d, want 500", w.Code)
	}
	if w := call(t, handler.ListSodGroups, httptest.NewRequest(http.MethodGet, "/iam/sod-groups", http.NoBody), false,
		auth.Principal{}); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", w.Code)
	}

	biz := newRestFixture(t, domain.ChannelBiz, false)
	if w := call(t, biz.handler.ListSodGroups, httptest.NewRequest(http.MethodGet, "/iam/sod-groups", http.NoBody), true,
		principalOf(domain.ChannelBiz, "2001", "")); w.Code != http.StatusForbidden {
		t.Fatalf("biz without merchant status = %d, want 403", w.Code)
	}
}

// TestSodGroupHTTPRequiresApproval 覆盖直写端点在审批模式下的拒绝分支。
func TestSodGroupHTTPRequiresApproval(t *testing.T) {
	f := newRestFixture(t, domain.ChannelAdmin, true)
	w := call(t, f.handler.CreateSodGroup, newJSONRequest(t, http.MethodPost, "/iam/sod-groups", map[string]any{
		"code":             "finance_dual",
		"name":             "财务双人复核",
		"max_active_roles": 1,
	}), true, principalOf(domain.ChannelAdmin, "1001", ""))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", w.Code, w.Body.String())
	}
}

// TestRoleConstraintHTTPFlow 覆盖角色约束的读取与更新。
func TestRoleConstraintHTTPFlow(t *testing.T) {
	f := newRestFixture(t, domain.ChannelAdmin, false)
	role := f.createRole(t, domain.ChannelAdmin, "ops_lead")
	prerequisite := f.createRole(t, domain.ChannelAdmin, "ops_trainee")
	principal := principalOf(domain.ChannelAdmin, "1001", "")

	getReq := httptest.NewRequest(http.MethodGet, "/iam/roles/"+role.ID+"/constraints", http.NoBody)
	getReq.SetPathValue("id", role.ID)
	if w := call(t, f.handler.GetRoleConstraints, getReq, true, principal); w.Code != http.StatusOK {
		t.Fatalf("get constraints status = %d, body = %s", w.Code, w.Body.String())
	}

	updateReq := newJSONRequest(t, http.MethodPut, "/iam/roles/"+role.ID+"/constraints", map[string]any{
		"required_role_ids": []string{prerequisite.ID},
		"max_bindings":      5,
		"version":           role.Version,
		"reason":            "收紧角色前置条件",
	})
	updateReq.SetPathValue("id", role.ID)
	if w := call(t, f.handler.UpdateRoleConstraints, updateReq, true, principal); w.Code != http.StatusOK {
		t.Fatalf("update constraints status = %d, body = %s", w.Code, w.Body.String())
	}
}

// TestMenusHTTP 覆盖菜单树输出与未认证分支。
func TestMenusHTTP(t *testing.T) {
	f := newRestFixture(t, domain.ChannelAdmin, false)
	principal := principalOf(domain.ChannelAdmin, "1001", "")
	if w := call(t, f.handler.Menus, httptest.NewRequest(http.MethodGet, "/iam/me/menus", http.NoBody), true, principal); w.Code != http.StatusOK {
		t.Fatalf("menus status = %d, body = %s", w.Code, w.Body.String())
	}
	if w := call(t, f.handler.Menus, httptest.NewRequest(http.MethodGet, "/iam/me/menus", http.NoBody), false, auth.Principal{}); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous menus status = %d, want 401", w.Code)
	}
}

// TestChangeRequestRejectAndCancel 覆盖审批单拒绝与撤销的 HTTP 映射。
func TestChangeRequestRejectAndCancel(t *testing.T) {
	f := newRestFixture(t, domain.ChannelAdmin, false)
	role := f.createRole(t, domain.ChannelAdmin, "ops_lead")

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
	rejectReq := newJSONRequest(t, http.MethodPost, "/iam/change-requests/"+request.ID+"/reject", map[string]any{
		"version": request.Version,
		"reason":  "暂不下线",
	})
	rejectReq.SetPathValue("id", request.ID)
	if w := call(t, f.handler.RejectChangeRequest, rejectReq, true,
		principalOf(domain.ChannelAdmin, "1002", "")); w.Code != http.StatusOK {
		t.Fatalf("reject status = %d, body = %s", w.Code, w.Body.String())
	}

	cancelable, err := f.changeSvc.Create(context.Background(), app.CreateChangeRequestCommand{
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRoleDelete,
		TargetID: role.ID,
		Reason:   "角色下线",
		Actor:    "1001",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cancelReq := newJSONRequest(t, http.MethodPost, "/iam/change-requests/"+cancelable.ID+"/cancel", map[string]any{
		"version": cancelable.Version,
	})
	cancelReq.SetPathValue("id", cancelable.ID)
	if w := call(t, f.handler.CancelChangeRequest, cancelReq, true,
		principalOf(domain.ChannelAdmin, "1001", "")); w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", w.Code, w.Body.String())
	}
}

// TestRoleTemplateHTTPFlow 覆盖角色模板的列表 / 创建 / 详情 / 更新 / 替换权限 / 漂移 / 同步 / 删除。
func TestRoleTemplateHTTPFlow(t *testing.T) {
	f := newRestFixture(t, domain.ChannelAdmin, false)
	principal := principalOf(domain.ChannelAdmin, "1001", "")

	if w := call(t, f.handler.ListRoleTemplates, httptest.NewRequest(http.MethodGet, "/iam/role-templates?limit=10", http.NoBody), false,
		auth.Principal{}); w.Code != http.StatusOK {
		t.Fatalf("list templates status = %d, body = %s", w.Code, w.Body.String())
	}

	created := call(t, f.handler.CreateRoleTemplate, newJSONRequest(t, http.MethodPost, "/iam/role-templates", map[string]any{
		"code":        "tpl_ops",
		"name":        "运维模板",
		"permissions": []string{string(iam.PermRoleRead)},
	}), true, principal)
	if created.Code != http.StatusOK {
		t.Fatalf("create template status = %d, body = %s", created.Code, created.Body.String())
	}
	templateID := dataID(t, decodeEnvelope(t, created))

	getReq := httptest.NewRequest(http.MethodGet, "/iam/role-templates/"+templateID, http.NoBody)
	getReq.SetPathValue("id", templateID)
	if w := call(t, f.handler.GetRoleTemplate, getReq, false, auth.Principal{}); w.Code != http.StatusOK {
		t.Fatalf("get template status = %d, body = %s", w.Code, w.Body.String())
	}

	updateReq := newJSONRequest(t, http.MethodPatch, "/iam/role-templates/"+templateID, map[string]any{
		"name":    "运维模板 v2",
		"version": 1,
	})
	updateReq.SetPathValue("id", templateID)
	if w := call(t, f.handler.UpdateRoleTemplate, updateReq, true, principal); w.Code != http.StatusOK {
		t.Fatalf("update template status = %d, body = %s", w.Code, w.Body.String())
	}

	replaceReq := newJSONRequest(t, http.MethodPut, "/iam/role-templates/"+templateID+"/permissions", map[string]any{
		"permissions": []string{string(iam.PermRoleRead)},
		"version":     2,
	})
	replaceReq.SetPathValue("id", templateID)
	if w := call(t, f.handler.ReplaceRoleTemplatePermissions, replaceReq, true, principal); w.Code != http.StatusOK {
		t.Fatalf("replace template permissions status = %d, body = %s", w.Code, w.Body.String())
	}

	driftReq := httptest.NewRequest(http.MethodGet, "/iam/role-templates/"+templateID+"/drift", http.NoBody)
	driftReq.SetPathValue("id", templateID)
	if w := call(t, f.handler.GetTemplateDrift, driftReq, false, auth.Principal{}); w.Code != http.StatusOK {
		t.Fatalf("drift status = %d, body = %s", w.Code, w.Body.String())
	}

	syncReq := newJSONRequest(t, http.MethodPost, "/iam/role-templates/"+templateID+"/sync", map[string]any{
		"dry_run": true,
		"reason":  "演练同步",
	})
	syncReq.SetPathValue("id", templateID)
	if w := call(t, f.handler.SyncRoleTemplate, syncReq, true, principal); w.Code != http.StatusOK {
		t.Fatalf("sync status = %d, body = %s", w.Code, w.Body.String())
	}

	deleteReq := newJSONRequest(t, http.MethodDelete, "/iam/role-templates/"+templateID, map[string]any{
		"version": 3,
		"reason":  "模板下线",
	})
	deleteReq.SetPathValue("id", templateID)
	if w := call(t, f.handler.DeleteRoleTemplate, deleteReq, true, principal); w.Code != http.StatusOK {
		t.Fatalf("delete template status = %d, body = %s", w.Code, w.Body.String())
	}
}

// dataID 从成功信封的 data 中提取 id。
func dataID(t *testing.T, envelope httpx.Envelope) string {
	t.Helper()
	raw, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ID == "" {
		t.Fatalf("data has no id: %s", raw)
	}
	return payload.ID
}

// menuCandidate 构造测试菜单。
func menuCandidate(code, routeName, externalURL string, nodeType domain.MenuNodeType) []*domain.Menu {
	return []*domain.Menu{{
		ID:          code,
		Channel:     domain.ChannelAdmin,
		Code:        code,
		NodeType:    nodeType,
		RouteName:   routeName,
		RoutePath:   "monitor",
		Component:   "/system/monitor/index.vue",
		ExternalURL: externalURL,
		Title:       "监控",
		Status:      domain.MenuStatusActive,
		Sort:        1,
	}}
}
