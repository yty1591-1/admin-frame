package scim_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/identity/transport/scim"
)

// scimIDs 是递增主键生成器。
type scimIDs struct{ n int64 }

func (s *scimIDs) NextString() (string, error) {
	s.n++
	return strings.Repeat("0", 0) + itoa(s.n), nil
}

// itoa 避免引入 strconv 之外的依赖。
func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// newSCIMHandler 构造带真实令牌的 SCIM Handler。
func newSCIMHandler(t *testing.T) (handler *scim.Handler, token string) {
	t.Helper()
	tokens, groups := persistence.NewMemoryScimRepositories()
	svc, err := app.NewScimService(app.ScimServiceConfig{
		Accounts:      persistence.NewMemoryAccountRepository(),
		Identities:    persistence.NewMemoryOAuthIdentityRepository(),
		Groups:        groups,
		Tokens:        tokens,
		Sessions:      persistence.NewMemorySessionRepository(),
		IDs:           &scimIDs{},
		Provider:      "okta",
		AutoProvision: true,
		PageSize:      50,
		TokenGrace:    time.Hour,
	})
	if err != nil {
		t.Fatalf("new scim service: %v", err)
	}
	created, err := svc.CreateToken(context.Background(), "1", "trace")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	handler = scim.NewHandler(scim.HandlerConfig{Service: svc})
	if handler == nil {
		t.Fatalf("handler must not be nil")
	}
	return handler, created.Plaintext
}

// TestSCIMRejectsMissingAndJWTBearer 验证 AC-P16-SCIM-6：
// 账号访问令牌（JWT）不能调用 SCIM，只有 scim_ 令牌可以。
func TestSCIMRejectsMissingAndJWTBearer(t *testing.T) {
	handler, token := newSCIMHandler(t)
	mux := http.NewServeMux()
	scim.Register(mux, handler)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{name: "no token", header: "", want: http.StatusUnauthorized},
		{name: "jwt bearer", header: "Bearer eyJhbGciOiJIUzI1NiJ9.e30.sig", want: http.StatusUnauthorized},
		{name: "scim token", header: "Bearer " + token, want: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/admin/scim/v2/ServiceProviderConfig", http.NoBody)
			if tc.header != "" {
				request.Header.Set("Authorization", tc.header)
			}
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d (body=%s)", recorder.Code, tc.want, recorder.Body.String())
			}
			if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/scim+json") {
				t.Fatalf("content type = %q, want application/scim+json", contentType)
			}
		})
	}
}

// TestSCIMErrorEnvelope 验证协议错误体使用 RFC 7644 §3.12 结构。
func TestSCIMErrorEnvelope(t *testing.T) {
	handler, token := newSCIMHandler(t)
	mux := http.NewServeMux()
	scim.Register(mux, handler)

	request := httptest.NewRequest(http.MethodGet, "/admin/scim/v2/Users/does-not-exist", http.NoBody)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "urn:ietf:params:scim:api:messages:2.0:Error") {
		t.Fatalf("body = %s, want scim error schema", body)
	}
	if strings.Contains(body, `"code"`) {
		t.Fatalf("body = %s, must not use the unified envelope", body)
	}
}

// TestSCIMBulkReturnsNotImplemented 验证 /Bulk 返回 501。
func TestSCIMBulkReturnsNotImplemented(t *testing.T) {
	handler, token := newSCIMHandler(t)
	mux := http.NewServeMux()
	scim.Register(mux, handler)

	request := httptest.NewRequest(http.MethodPost, "/admin/scim/v2/Bulk", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/scim+json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", recorder.Code)
	}
}

// TestSCIMGroupPagination 验证 startIndex / count 与 totalResults。
func TestSCIMGroupPagination(t *testing.T) {
	handler, token := newSCIMHandler(t)
	mux := http.NewServeMux()
	scim.Register(mux, handler)

	request := httptest.NewRequest(http.MethodGet, "/admin/scim/v2/Groups?startIndex=1&count=10", http.NoBody)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{`"totalResults":0`, `"startIndex":1`, `"Resources"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
}

// 编译期断言：SCIM 令牌状态常量与错误结构可被外部包使用。
var (
	_ = domain.ScimTokenActive
	_ = app.ScimError{Status: 400, ScimType: "invalidValue", Detail: "x"}
)

// TestSCIMDiscoveryEndpoints 验证发现端点的列表与单项地址都可解析。
//
// 列表里的 meta.location 指向 /ResourceTypes/{id} 与 /Schemas/{id}，
// 客户端（Okta / Entra ID）会跟随该地址；只实现列表会让能力探测半途失败。
func TestSCIMDiscoveryEndpoints(t *testing.T) {
	handler, token := newSCIMHandler(t)
	mux := http.NewServeMux()
	scim.Register(mux, handler)

	cases := []struct {
		name string
		path string
		want int
		body []string
	}{
		{
			name: "resource types list",
			path: "/admin/scim/v2/ResourceTypes",
			want: http.StatusOK,
			body: []string{`"id":"User"`, `"id":"Group"`, "/ResourceTypes/User"},
		},
		{name: "resource type user", path: "/admin/scim/v2/ResourceTypes/User", want: http.StatusOK,
			body: []string{`"id":"User"`, `"endpoint":"/Users"`}},
		{name: "resource type case insensitive", path: "/admin/scim/v2/ResourceTypes/group", want: http.StatusOK,
			body: []string{`"id":"Group"`}},
		{name: "resource type unknown", path: "/admin/scim/v2/ResourceTypes/Nope", want: http.StatusNotFound,
			body: []string{"resource type not found"}},
		{
			name: "schemas list",
			path: "/admin/scim/v2/Schemas",
			want: http.StatusOK,
			body: []string{"urn:ietf:params:scim:schemas:core:2.0:User", "urn:ietf:params:scim:schemas:core:2.0:Group"},
		},
		{name: "schema user", path: "/admin/scim/v2/Schemas/urn:ietf:params:scim:schemas:core:2.0:User",
			want: http.StatusOK, body: []string{`"name":"User"`, `"name":"active"`}},
		{name: "schema unknown", path: "/admin/scim/v2/Schemas/urn:unknown", want: http.StatusNotFound,
			body: []string{"schema not found"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tc.path, http.NoBody)
			request.Header.Set("Authorization", "Bearer "+token)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d (body=%s)", recorder.Code, tc.want, recorder.Body.String())
			}
			for _, want := range tc.body {
				if !strings.Contains(recorder.Body.String(), want) {
					t.Fatalf("body = %s, want %s", recorder.Body.String(), want)
				}
			}
		})
	}
}
