package biz_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	iambiz "github.com/admin-frame/admin-frame/internal/iam/transport/biz"
	"github.com/admin-frame/admin-frame/internal/iam/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

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

type recorder struct{}

func (recorder) Record(context.Context, audit.Event) error { return nil }

// fixture 复用真实用例与内存仓储，只替换 Principal 与权限中间件。
type fixture struct {
	roles      *persistence.MemoryRoleRepository
	bindings   *persistence.MemoryBindingRepository
	changeRepo *persistence.MemoryChangeRequestRepository
	handler    *rest.Handler
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	roles := persistence.NewMemoryRoleRepository()
	bindings := persistence.NewMemoryBindingRepository()
	ids := &seqIDs{}
	roleSvc, err := app.NewRoleService(app.RoleServiceConfig{
		Roles: roles, Bindings: bindings, Recorder: recorder{}, IDs: ids,
		Audit:  app.AuditMeta{Service: "test", ActorType: "merchant_staff"},
		Limits: app.RoleLimits{MaxPermissionsPerRole: 200, MaxCustomRolesPerMerchant: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingSvc, err := app.NewBindingService(app.BindingServiceConfig{
		Roles: roles, Bindings: bindings, Recorder: recorder{}, IDs: ids,
		Audit:  app.AuditMeta{Service: "test", ActorType: "merchant_staff"},
		Limits: app.BindingLimits{MaxBindingsPerSubject: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	querySvc, err := app.NewQueryService(roles, bindings, persistence.NewMemorySnapshotRepository(roles, bindings))
	if err != nil {
		t.Fatal(err)
	}
	applicator, err := app.NewRoleChangeApplicator(roleSvc, bindingSvc)
	if err != nil {
		t.Fatal(err)
	}
	changeRepo := persistence.NewMemoryChangeRequestRepository()
	changeSvc, err := app.NewChangeRequestService(app.ChangeRequestServiceConfig{
		Requests:   changeRepo,
		Applicator: applicator,
		IDs:        ids,
		Limits:     app.ChangeLimits{AllowBizChannel: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := rest.NewHandler(rest.HandlerConfig{
		Roles: roleSvc, Bindings: bindingSvc, Query: querySvc,
		Changes: changeSvc, Channel: domain.ChannelBiz,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{roles: roles, bindings: bindings, changeRepo: changeRepo, handler: handler}
}

type router struct{ handler http.Handler }

// router 构造以 principal 身份访问的 B 端路由；权限中间件放行以便验证 Handler 语义。
func (f *fixture) router(principal auth.Principal) *router {
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}
	perm := func(string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler { return next }
	}
	return &router{handler: iambiz.NewRouter(f.handler, authMW, perm)}
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (rt *router) do(t *testing.T, method, path string, body any) (*httptest.ResponseRecorder, envelope) {
	t.Helper()
	var payload []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = raw
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	rt.handler.ServeHTTP(rec, req)
	var env envelope
	if rec.Body.Len() > 0 {
		// 未命中的路由由 ServeMux 返回纯文本 404，无信封可解码。
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			return rec, envelope{}
		}
	}
	return rec, env
}

type rolePayload struct {
	ID         string `json:"id"`
	Channel    string `json:"channel"`
	ScopeType  string `json:"scope_type"`
	MerchantID string `json:"merchant_id"`
	Code       string `json:"code"`
	Version    int64  `json:"version"`
}

// TestBizRoleTenantIsolation 验证商户与端别均来自装配期 / Principal，而非请求体。
func TestBizRoleTenantIsolation(t *testing.T) {
	f := newFixture(t)
	m1 := f.router(auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-1", MerchantID: "m-1"})

	rec, env := m1.do(t, http.MethodPost, "/biz/v1/iam/roles", map[string]any{
		"scope_type":  "merchant",
		"code":        "merchant_viewer",
		"name":        "查看者",
		"permissions": []string{"order:read"},
		"data_scopes": []map[string]any{{"resource": "order", "scope_type": "merchant"}},
	})
	if rec.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("create status=%d env=%+v body=%s", rec.Code, env, rec.Body.String())
	}
	var created rolePayload
	if err := json.Unmarshal(env.Data, &created); err != nil {
		t.Fatal(err)
	}
	if created.Channel != "biz" || created.ScopeType != "merchant" || created.MerchantID != "m-1" {
		t.Fatalf("created = %+v", created)
	}

	// 请求体伪造 A 端端别必须被拒绝，而不是被静默改写。
	rec, env = m1.do(t, http.MethodPost, "/biz/v1/iam/roles", map[string]any{
		"channel": "admin", "scope_type": "platform", "code": "ops_lead", "name": "运营主管",
	})
	if rec.Code != http.StatusBadRequest || env.Code != 40000 {
		t.Fatalf("admin channel status=%d env=%+v", rec.Code, env)
	}

	// 同商户可读；跨商户一律 404（不泄露角色是否存在）。
	rec, _ = m1.do(t, http.MethodGet, "/biz/v1/iam/roles/"+created.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("same merchant get status=%d", rec.Code)
	}
	m2 := f.router(auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-2", MerchantID: "m-2"})
	rec, env = m2.do(t, http.MethodGet, "/biz/v1/iam/roles/"+created.ID, nil)
	if rec.Code != http.StatusNotFound || env.Code != 40400 {
		t.Fatalf("cross merchant status=%d env=%+v", rec.Code, env)
	}

	// 缺商户的 Principal 一律拒绝（不能退化为「全部商户」）。
	noMerchant := f.router(auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-3"})
	rec, _ = noMerchant.do(t, http.MethodGet, "/biz/v1/iam/roles", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing merchant status=%d", rec.Code)
	}
}

// TestBizPermissionCatalogFiltersAdminOnly 验证 B 端看不到 A 端专属权限点。
func TestBizPermissionCatalogFiltersAdminOnly(t *testing.T) {
	f := newFixture(t)
	rt := f.router(auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-1", MerchantID: "m-1"})

	// 即使显式传 channel=admin，也必须被强制收敛到 biz。
	rec, env := rt.do(t, http.MethodGet, "/biz/v1/iam/permissions?channel=admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("permissions status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []struct {
			Code    string `json:"code"`
			Channel string `json:"channel"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != len(list.Items) || len(list.Items) == 0 {
		t.Fatalf("total=%d items=%d", list.Total, len(list.Items))
	}
	seen := make(map[string]struct{}, len(list.Items))
	for _, item := range list.Items {
		if item.Channel != "biz" && item.Channel != "both" {
			t.Fatalf("B 端目录泄露 %s (channel=%s)", item.Code, item.Channel)
		}
		seen[item.Code] = struct{}{}
	}
	for _, banned := range []string{"iam:permission:read", "audit:read", "config:write"} {
		if _, ok := seen[banned]; ok {
			t.Fatalf("admin-only permission %s leaked to biz", banned)
		}
	}
	if _, ok := seen["iam:role:read"]; !ok {
		t.Fatal("iam:role:read must be visible to biz")
	}
}

// TestBizRouterHasNoAuthorizeEndpoint 验证模拟鉴权（A 端专属）未挂到 B 端。
func TestBizRouterHasNoAuthorizeEndpoint(t *testing.T) {
	f := newFixture(t)
	rt := f.router(auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-1", MerchantID: "m-1"})
	rec, _ := rt.do(t, http.MethodPost, "/biz/v1/iam/authorize", map[string]any{
		"subject_type": "merchant_staff", "subject_id": "u-1", "permission": "order:read",
	})
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("authorize route must not exist on biz, got %d", rec.Code)
	}
}

// TestBizRouterHasNoBreakglassEndpoints 验证破玻璃只在 A 端暴露。
//
// 破玻璃是平台级应急能力：允许商户自助申请等于把超级权限入口下放到租户，
// 因此在路由层也不注册，而不是「存在但返回 403」——后者会泄露该功能已上线。
func TestBizRouterHasNoBreakglassEndpoints(t *testing.T) {
	f := newFixture(t)
	rt := f.router(auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-1", MerchantID: "m-1"})
	for _, path := range []string{
		"/biz/v1/iam/breakglass",
		"/biz/v1/iam/breakglass/1/approve",
		"/biz/v1/iam/breakglass/1/revoke",
	} {
		rec, _ := rt.do(t, http.MethodPost, path, map[string]any{"version": 1})
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s must not exist on biz, got %d", path, rec.Code)
		}
	}
}

type changePayload struct {
	ID         string `json:"id"`
	Channel    string `json:"channel"`
	MerchantID string `json:"merchant_id"`
	Status     string `json:"status"`
	Version    int64  `json:"version"`
}

func decodeChange(t *testing.T, env envelope) changePayload {
	t.Helper()
	var created changePayload
	if err := json.Unmarshal(env.Data, &created); err != nil {
		t.Fatalf("decode change request: %v (data=%s)", err, env.Data)
	}
	return created
}

// TestBizChangeRequestTenantIsolation 验证审批单的商户只来自 Principal。
//
// 覆盖三条不变量：
//  1. 创建时 MerchantID 取 Principal，请求体伪造 channel=admin 被拒绝；
//  2. 列表只返回本商户单据（跨商户查询为空，不泄露存在性）；
//  3. 跨商户审批返回 404 而不是 403（避免枚举）。
func TestBizChangeRequestTenantIsolation(t *testing.T) {
	f := newFixture(t)
	m1 := f.router(auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-1", MerchantID: "m-1"})

	roleRec, roleEnv := m1.do(t, http.MethodPost, "/biz/v1/iam/roles", map[string]any{
		"scope_type":  "merchant",
		"code":        "merchant_viewer",
		"name":        "查看者",
		"permissions": []string{"order:read"},
		"data_scopes": []map[string]any{{"resource": "order", "scope_type": "merchant"}},
	})
	if roleRec.Code != http.StatusOK {
		t.Fatalf("create role status=%d body=%s", roleRec.Code, roleRec.Body.String())
	}
	var role rolePayload
	if err := json.Unmarshal(roleEnv.Data, &role); err != nil {
		t.Fatal(err)
	}

	rec, env := m1.do(t, http.MethodPost, "/biz/v1/iam/change-requests", map[string]any{
		"action":    "role_permission_replace",
		"target_id": role.ID,
		"payload":   map[string]any{"permissions": []string{"order:read", "order:write"}},
		"reason":    "补授写入权限",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create change request status=%d body=%s", rec.Code, rec.Body.String())
	}
	created := decodeChange(t, env)
	if created.Channel != "biz" || created.Status != "pending" {
		t.Fatalf("created = %+v", created)
	}
	if created.MerchantID != "" {
		t.Fatalf("B 端响应不应回显 merchant_id，got %q", created.MerchantID)
	}
	// 仓储是租户归属的最终事实：必须由 Principal 派生。
	stored, err := f.changeRepo.FindByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored.MerchantID != "m-1" || stored.Channel != domain.ChannelBiz {
		t.Fatalf("stored = %+v", stored)
	}

	// 请求体伪造端别必须拒绝，而不是被静默改写为 biz。
	rec, env = m1.do(t, http.MethodPost, "/biz/v1/iam/change-requests", map[string]any{
		"channel": "admin", "action": "role_update", "target_id": role.ID,
	})
	if rec.Code != http.StatusBadRequest || env.Code != 40000 {
		t.Fatalf("admin channel status=%d env=%+v", rec.Code, env)
	}

	// 载荷伪造租户必须 403，而不是静默忽略。
	rec, env = m1.do(t, http.MethodPost, "/biz/v1/iam/change-requests", map[string]any{
		"action": "role_permission_replace", "target_id": role.ID,
		"payload": map[string]any{"merchant_id": "m-2", "permissions": []string{"order:write"}},
	})
	if rec.Code != http.StatusForbidden || env.Code != 40300 {
		t.Fatalf("payload merchant_id status=%d env=%+v", rec.Code, env)
	}

	// B 端不得申请平台级作用域：越权意图在申请阶段即被拒绝。
	rec, env = m1.do(t, http.MethodPost, "/biz/v1/iam/change-requests", map[string]any{
		"action": "role_create",
		"payload": map[string]any{
			"scope_type": "platform", "code": "escaped", "name": "越权角色",
			"permissions": []string{"order:read"},
		},
	})
	if rec.Code != http.StatusBadRequest || env.Code != 40000 {
		t.Fatalf("platform scope status=%d env=%+v", rec.Code, env)
	}

	// 同商户可见。
	rec, env = m1.do(t, http.MethodGet, "/biz/v1/iam/change-requests", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 {
		t.Fatalf("same merchant total=%d, want 1", list.Total)
	}

	// 跨商户：列表为空、审批 404。
	m2 := f.router(auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-2", MerchantID: "m-2"})
	rec, env = m2.do(t, http.MethodGet, "/biz/v1/iam/change-requests", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("m2 list status=%d", rec.Code)
	}
	list.Total = -1
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 0 {
		t.Fatalf("cross merchant total=%d, want 0", list.Total)
	}
	rec, env = m2.do(t, http.MethodPost, "/biz/v1/iam/change-requests/"+created.ID+"/approve",
		map[string]any{"version": created.Version})
	if rec.Code != http.StatusNotFound || env.Code != 40400 {
		t.Fatalf("cross merchant approve status=%d env=%+v", rec.Code, env)
	}

	// 申请人自审必须 403（SoD），且状态保持 pending。
	rec, env = m1.do(t, http.MethodPost, "/biz/v1/iam/change-requests/"+created.ID+"/approve",
		map[string]any{"version": created.Version})
	if rec.Code != http.StatusForbidden || env.Code != 40300 {
		t.Fatalf("self approval status=%d env=%+v", rec.Code, env)
	}

	// 同商户另一主体审批通过，随后应用：角色权限真实生效。
	approver := f.router(auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-9", MerchantID: "m-1"})
	rec, env = approver.do(t, http.MethodPost, "/biz/v1/iam/change-requests/"+created.ID+"/approve",
		map[string]any{"version": created.Version})
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", rec.Code, rec.Body.String())
	}
	approved := decodeChange(t, env)
	rec, env = approver.do(t, http.MethodPost, "/biz/v1/iam/change-requests/"+created.ID+"/apply",
		map[string]any{"version": approved.Version})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	if applied := decodeChange(t, env); applied.Status != "applied" {
		t.Fatalf("applied = %+v", applied)
	}
	updated, err := f.roles.FindByID(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("reload role: %v", err)
	}
	if updated.MerchantID != "m-1" {
		t.Fatalf("role merchant = %q, want m-1", updated.MerchantID)
	}
}
