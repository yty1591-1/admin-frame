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
	roles    *persistence.MemoryRoleRepository
	bindings *persistence.MemoryBindingRepository
	handler  *rest.Handler
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
	handler, err := rest.NewHandler(rest.HandlerConfig{
		Roles: roleSvc, Bindings: bindingSvc, Query: querySvc, Channel: domain.ChannelBiz,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{roles: roles, bindings: bindings, handler: handler}
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
