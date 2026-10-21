package admin_test

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
	"github.com/admin-frame/admin-frame/internal/iam/transport/admin"
	"github.com/admin-frame/admin-frame/internal/iam/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
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

type router struct {
	handler http.Handler
}

// newRouter 装配真实用例 + 测试版鉴权中间件（仅注入 Principal，不做权限判定）。
func newRouter(t *testing.T) *router {
	t.Helper()
	roles := persistence.NewMemoryRoleRepository()
	bindings := persistence.NewMemoryBindingRepository()
	ids := &seqIDs{}
	roleSvc, err := app.NewRoleService(app.RoleServiceConfig{
		Roles: roles, Bindings: bindings, Recorder: recorder{}, IDs: ids,
		Audit:  app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits: app.RoleLimits{MaxPermissionsPerRole: 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingSvc, err := app.NewBindingService(app.BindingServiceConfig{
		Roles: roles, Bindings: bindings, Recorder: recorder{}, IDs: ids,
		Audit:  app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits: app.BindingLimits{MaxBindingsPerSubject: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	querySvc, err := app.NewQueryService(roles, bindings, persistence.NewMemorySnapshotRepository(roles, bindings))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := rest.NewHandler(rest.HandlerConfig{
		Roles: roleSvc, Bindings: bindingSvc, Query: querySvc, Channel: domain.ChannelAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "1001"}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}
	perm := func(string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler { return next }
	}
	return &router{handler: admin.NewRouter(handler, authMW, perm)}
}

// envelope 是响应信封的解码目标。
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// do 执行请求并解析信封。
func (rt *router) do(t *testing.T, method, path string, body any, headers map[string]string) (*httptest.ResponseRecorder, envelope) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	rt.handler.ServeHTTP(rec, req)
	var env envelope
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
		}
	}
	return rec, env
}

type rolePayload struct {
	ID          string   `json:"id"`
	Version     int64    `json:"version"`
	Status      string   `json:"status"`
	Permissions []string `json:"permissions"`
}

func TestCreateGetAndUpdateRole(t *testing.T) {
	rt := newRouter(t)
	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/roles", map[string]any{
		"channel":     "admin",
		"scope_type":  "platform",
		"code":        "ops_lead",
		"name":        "运营主管",
		"permissions": []string{"iam:role:read"},
	}, nil)
	if rec.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("create status=%d env=%+v body=%s", rec.Code, env, rec.Body.String())
	}
	var created rolePayload
	if err := json.Unmarshal(env.Data, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Version != 1 || created.Status != "active" {
		t.Fatalf("created = %+v", created)
	}

	rec, env = rt.do(t, http.MethodGet, "/admin/v1/iam/roles/"+created.ID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec, env = rt.do(t, http.MethodPatch, "/admin/v1/iam/roles/"+created.ID, map[string]any{
		"name": "运营组",
	}, map[string]string{"If-Match": `"1"`})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status=%d env=%+v", rec.Code, env)
	}
	var updated rolePayload
	if err := json.Unmarshal(env.Data, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}

	// 缺少版本：必须拒绝，避免无锁覆盖。
	rec, env = rt.do(t, http.MethodPatch, "/admin/v1/iam/roles/"+created.ID, map[string]any{
		"name": "无版本",
	}, nil)
	if rec.Code != http.StatusBadRequest || env.Code != apperr.CodeBadRequest {
		t.Fatalf("missing version status=%d code=%d", rec.Code, env.Code)
	}

	// 版本冲突：40900。
	rec, env = rt.do(t, http.MethodPatch, "/admin/v1/iam/roles/"+created.ID, map[string]any{
		"name": "旧版本",
	}, map[string]string{"If-Match": `"1"`})
	if rec.Code != http.StatusConflict || env.Code != apperr.CodeConflict {
		t.Fatalf("conflict status=%d code=%d", rec.Code, env.Code)
	}
}

func TestCreateRoleRejectsMismatchedChannelAndUnknownFields(t *testing.T) {
	rt := newRouter(t)
	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/roles", map[string]any{
		"channel": "biz", "scope_type": "merchant", "code": "biz_role", "name": "biz",
	}, nil)
	if rec.Code != http.StatusBadRequest || env.Code != apperr.CodeBadRequest {
		t.Fatalf("channel mismatch status=%d code=%d", rec.Code, env.Code)
	}
	rec, env = rt.do(t, http.MethodPost, "/admin/v1/iam/roles", map[string]any{
		"channel": "admin", "scope_type": "platform", "code": "ops", "name": "ops", "typo": true,
	}, nil)
	if rec.Code != http.StatusBadRequest || env.Code != apperr.CodeBadRequest {
		t.Fatalf("unknown field status=%d code=%d", rec.Code, env.Code)
	}
}

func TestReplacePermissionsReturnsDiffAndRejectsMismatchedVersion(t *testing.T) {
	rt := newRouter(t)
	_, env := rt.do(t, http.MethodPost, "/admin/v1/iam/roles", map[string]any{
		"channel": "admin", "scope_type": "platform", "code": "ops", "name": "ops",
		"permissions": []string{"iam:role:read"},
	}, nil)
	var created rolePayload
	if err := json.Unmarshal(env.Data, &created); err != nil {
		t.Fatal(err)
	}

	rec, env := rt.do(t, http.MethodPut, "/admin/v1/iam/roles/"+created.ID+"/permissions", map[string]any{
		"permissions": []string{"iam:role:read", "iam:binding:read"},
		"version":     created.Version,
		"reason":      "职责扩展",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result struct {
		Added   []string `json:"added"`
		Removed []string `json:"removed"`
		Role    struct {
			Version int64 `json:"version"`
		} `json:"role"`
	}
	if err := json.Unmarshal(env.Data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 1 || result.Added[0] != "iam:binding:read" {
		t.Fatalf("added = %v", result.Added)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("removed = %v", result.Removed)
	}
	if result.Role.Version != 2 {
		t.Fatalf("version = %d, want 2", result.Role.Version)
	}

	// If-Match 与 body.version 不一致 → 40000。
	rec, env = rt.do(t, http.MethodPut, "/admin/v1/iam/roles/"+created.ID+"/permissions", map[string]any{
		"permissions": []string{"iam:role:read"}, "version": 2, "reason": "x",
	}, map[string]string{"If-Match": `"3"`})
	if rec.Code != http.StatusBadRequest || env.Code != apperr.CodeBadRequest {
		t.Fatalf("version mismatch status=%d code=%d", rec.Code, env.Code)
	}

	// 非法 If-Match。
	rec, env = rt.do(t, http.MethodPut, "/admin/v1/iam/roles/"+created.ID+"/permissions", map[string]any{
		"permissions": []string{"iam:role:read"},
	}, map[string]string{"If-Match": "abc"})
	if rec.Code != http.StatusBadRequest || env.Code != apperr.CodeBadRequest {
		t.Fatalf("invalid If-Match status=%d code=%d", rec.Code, env.Code)
	}
}

func TestBindingSubjectPermissionsAndAuthorize(t *testing.T) {
	rt := newRouter(t)
	_, env := rt.do(t, http.MethodPost, "/admin/v1/iam/roles", map[string]any{
		"channel": "admin", "scope_type": "platform", "code": "ops", "name": "ops",
		"permissions": []string{"iam:role:read"},
	}, nil)
	var created rolePayload
	if err := json.Unmarshal(env.Data, &created); err != nil {
		t.Fatal(err)
	}

	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/bindings", map[string]any{
		"subject_type": "staff", "subject_id": "2001", "role_id": created.ID, "reason": "onboarding",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("bind status=%d body=%s", rec.Code, rec.Body.String())
	}
	var binding struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(env.Data, &binding); err != nil {
		t.Fatal(err)
	}

	rec, env = rt.do(t, http.MethodGet, "/admin/v1/iam/bindings?subject_type=staff&subject_id=2001", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list bindings status=%d", rec.Code)
	}
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("bindings = %+v", list)
	}

	rec, env = rt.do(t, http.MethodGet, "/admin/v1/iam/subjects/staff/2001/permissions", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("subject permissions status=%d body=%s", rec.Code, rec.Body.String())
	}
	var permissions struct {
		Roles       []string `json:"roles"`
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(env.Data, &permissions); err != nil {
		t.Fatal(err)
	}
	if len(permissions.Roles) != 1 || len(permissions.Permissions) != 1 {
		t.Fatalf("permissions = %+v", permissions)
	}

	rec, env = rt.do(t, http.MethodPost, "/admin/v1/iam/authorize", map[string]any{
		"subject_type": "staff", "subject_id": "2001", "permission": "iam:role:read",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorize status=%d", rec.Code)
	}
	var simulated struct {
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(env.Data, &simulated); err != nil {
		t.Fatal(err)
	}
	if !simulated.Allowed || simulated.Reason != "permission_granted" {
		t.Fatalf("simulated = %+v", simulated)
	}

	rec, env = rt.do(t, http.MethodGet, "/admin/v1/iam/me/permissions", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status=%d", rec.Code)
	}
	var me struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(env.Data, &me); err != nil {
		t.Fatal(err)
	}
	if len(me.Permissions) != 0 {
		t.Fatalf("principal 1001 has no binding; permissions = %v", me.Permissions)
	}

	// 解绑后主体权限清空。
	rec, env = rt.do(t, http.MethodDelete, "/admin/v1/iam/bindings/"+binding.ID, map[string]any{
		"reason": "offboarding",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("unbind status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec, env = rt.do(t, http.MethodGet, "/admin/v1/iam/subjects/staff/2001/permissions", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("subject permissions after unbind status=%d", rec.Code)
	}
	if err := json.Unmarshal(env.Data, &permissions); err != nil {
		t.Fatal(err)
	}
	if len(permissions.Permissions) != 0 {
		t.Fatalf("permissions after unbind = %v", permissions.Permissions)
	}
}

func TestPermissionsAndRoleList(t *testing.T) {
	rt := newRouter(t)
	rec, env := rt.do(t, http.MethodGet, "/admin/v1/iam/permissions?page=1&page_size=5", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("permissions status=%d", rec.Code)
	}
	var list struct {
		Items []struct {
			Code string `json:"code"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 5 || list.Total < 5 {
		t.Fatalf("permissions = %+v", list)
	}
	rec, _ = rt.do(t, http.MethodGet, "/admin/v1/iam/permissions?page_size=101", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized page status=%d", rec.Code)
	}
	rec, _ = rt.do(t, http.MethodGet, "/admin/v1/iam/permissions/tree", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tree status=%d", rec.Code)
	}
	rec, _ = rt.do(t, http.MethodGet, "/admin/v1/iam/roles?page_size=10", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("roles status=%d", rec.Code)
	}
}

func TestDeleteRoleRequiresVersionAndReportsNotFound(t *testing.T) {
	rt := newRouter(t)
	rec, env := rt.do(t, http.MethodDelete, "/admin/v1/iam/roles/not-exist", nil, nil)
	if rec.Code != http.StatusBadRequest || env.Code != apperr.CodeBadRequest {
		t.Fatalf("missing version status=%d code=%d", rec.Code, env.Code)
	}
	rec, env = rt.do(t, http.MethodDelete, "/admin/v1/iam/roles/not-exist", nil, map[string]string{"If-Match": `"1"`})
	if rec.Code != http.StatusNotFound || env.Code != apperr.CodeNotFound {
		t.Fatalf("not found status=%d code=%d", rec.Code, env.Code)
	}
}

func TestMissingPrincipalIsUnauthorized(t *testing.T) {
	roles := persistence.NewMemoryRoleRepository()
	bindings := persistence.NewMemoryBindingRepository()
	ids := &seqIDs{}
	roleSvc, err := app.NewRoleService(app.RoleServiceConfig{Roles: roles, Bindings: bindings, Recorder: recorder{}, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	bindingSvc, err := app.NewBindingService(app.BindingServiceConfig{Roles: roles, Bindings: bindings, Recorder: recorder{}, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	querySvc, err := app.NewQueryService(roles, bindings, persistence.NewMemorySnapshotRepository(roles, bindings))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := rest.NewHandler(rest.HandlerConfig{
		Roles: roleSvc, Bindings: bindingSvc, Query: querySvc, Channel: domain.ChannelAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	perm := func(string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler { return next }
	}
	mux := http.NewServeMux()
	admin.Register(mux, handler, func(next http.Handler) http.Handler { return next }, perm)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/iam/roles", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestNewHandlerRequiresDependencies(t *testing.T) {
	if _, err := rest.NewHandler(rest.HandlerConfig{}); err == nil {
		t.Fatal("expected error for missing services")
	}
}
