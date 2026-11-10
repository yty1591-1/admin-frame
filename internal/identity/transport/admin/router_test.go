package admin_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/transport/admin"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
)

// passthrough 是测试用鉴权中间件。
func passthrough(next http.Handler) http.Handler { return next }

// assertNoStore 断言敏感响应带禁止缓存头（IDN-P2-03）。
func assertNoStore(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if got := recorder.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, private" {
		t.Fatalf("cache-control=%q", got)
	}
	if got := recorder.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("pragma=%q", got)
	}
	if got := recorder.Header().Get("Expires"); got != "0" {
		t.Fatalf("expires=%q", got)
	}
	if got := recorder.Header().Get("Vary"); got != "Authorization" {
		t.Fatalf("vary=%q", got)
	}
}

// TestAuthRoutesNoStore 覆盖登录、MFA setup 与 /me 的禁止缓存头。
func TestAuthRoutesNoStore(t *testing.T) {
	handler, err := rest.NewHandler(rest.HandlerConfig{Auth: &app.AuthService{}, Channel: domain.ChannelAdmin})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}
	mux := http.NewServeMux()
	admin.Register(mux, handler, passthrough, nil)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "login", method: http.MethodPost, path: "/admin/v1/auth/login", body: `{"subject":`},
		{name: "mfa_setup", method: http.MethodPost, path: "/admin/v1/auth/mfa/setup", body: `{"setup_ticket":`},
		{name: "me", method: http.MethodGet, path: "/admin/v1/me"},
		{name: "update_me", method: http.MethodPatch, path: "/admin/v1/me", body: `{"display_name":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			mux.ServeHTTP(recorder, request)
			assertNoStore(t, recorder)
		})
	}
}

// recordingPermission 记录每条账号路由实际要求的权限码，并直接放行。
func recordingPermission(seen *[]string) func(string) func(http.Handler) http.Handler {
	return func(code string) func(http.Handler) http.Handler {
		return func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				*seen = append(*seen, code)
				w.WriteHeader(http.StatusNoContent)
			})
		}
	}
}

// TestAccountRoutesRequireSplitPermissions 验证 P1-14 权限矩阵：每类动作只要求自己的权限码。
func TestAccountRoutesRequireSplitPermissions(t *testing.T) {
	handler, err := rest.NewHandler(rest.HandlerConfig{Auth: &app.AuthService{}, Channel: domain.ChannelAdmin})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}
	var seen []string
	perm := recordingPermission(&seen)
	mux := http.NewServeMux()
	admin.Register(mux, handler, passthrough, perm)

	cases := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"list", http.MethodGet, "/admin/v1/auth/accounts", "identity:account:read"},
		{"get", http.MethodGet, "/admin/v1/auth/accounts/1", "identity:account:read"},
		{"sessions_read", http.MethodGet, "/admin/v1/auth/accounts/1/sessions", "identity:account:read"},
		{"disable", http.MethodPost, "/admin/v1/auth/accounts/1/disable", "identity:account:write"},
		{"enable", http.MethodPost, "/admin/v1/auth/accounts/1/enable", "identity:account:write"},
		{"lock", http.MethodPost, "/admin/v1/auth/accounts/1/lock", "identity:account:write"},
		{"unlock", http.MethodPost, "/admin/v1/auth/accounts/1/unlock", "identity:account:write"},
		{"restore", http.MethodPost, "/admin/v1/auth/accounts/1/restore", "identity:account:write"},
		{"password_reset", http.MethodPost, "/admin/v1/auth/accounts/1/password/reset", "identity:account:credential"},
		{"mfa_reset", http.MethodPost, "/admin/v1/auth/accounts/1/mfa/reset", "identity:account:credential"},
		{"delete", http.MethodDelete, "/admin/v1/auth/accounts/1", "identity:account:delete"},
		{"revoke_session", http.MethodDelete, "/admin/v1/auth/accounts/1/sessions/2", "identity:account:session"},
		{"revoke_all", http.MethodPost, "/admin/v1/auth/accounts/1/sessions/revoke-all", "identity:account:session"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen = nil
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, http.NoBody))
			if len(seen) != 1 || seen[0] != tc.want {
				t.Fatalf("permission codes = %v, want [%s]", seen, tc.want)
			}
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", recorder.Code)
			}
		})
	}
}

// TestAccountMutationsRequireIdempotencyKey 验证账号处置路由缺少幂等键时返回 400。
func TestAccountMutationsRequireIdempotencyKey(t *testing.T) {
	handler, err := rest.NewHandler(rest.HandlerConfig{Auth: &app.AuthService{}, Channel: domain.ChannelAdmin})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}
	mux := http.NewServeMux()
	admin.Register(mux, handler, passthrough, passthroughPermission())

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"disable", http.MethodPost, "/admin/v1/auth/accounts/1/disable"},
		{"enable", http.MethodPost, "/admin/v1/auth/accounts/1/enable"},
		{"lock", http.MethodPost, "/admin/v1/auth/accounts/1/lock"},
		{"unlock", http.MethodPost, "/admin/v1/auth/accounts/1/unlock"},
		{"restore", http.MethodPost, "/admin/v1/auth/accounts/1/restore"},
		{"mfa_reset", http.MethodPost, "/admin/v1/auth/accounts/1/mfa/reset"},
		{"password_reset", http.MethodPost, "/admin/v1/auth/accounts/1/password/reset"},
		{"delete", http.MethodDelete, "/admin/v1/auth/accounts/1"},
		{"revoke_session", http.MethodDelete, "/admin/v1/auth/accounts/1/sessions/2"},
		{"revoke_all", http.MethodPost, "/admin/v1/auth/accounts/1/sessions/revoke-all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, http.NoBody))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 when Idempotency-Key missing", recorder.Code)
			}
		})
	}
}

// passthroughPermission 返回直接放行的权限中间件，使请求继续进入幂等检查。
func passthroughPermission() func(string) func(http.Handler) http.Handler {
	return func(string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler { return next }
	}
}

// TestAccountRoutesFailClosedWithoutPermissionMiddleware 验证未注入权限中间件时失败关闭。
func TestAccountRoutesFailClosedWithoutPermissionMiddleware(t *testing.T) {
	handler, err := rest.NewHandler(rest.HandlerConfig{Auth: &app.AuthService{}, Channel: domain.ChannelAdmin})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}
	mux := http.NewServeMux()
	admin.Register(mux, handler, passthrough, nil)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/v1/auth/accounts", http.NoBody))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when permission middleware missing", recorder.Code)
	}
}

// TestM2MRoutesAbsentWhenDisabled 验证 identity.m2m.enabled=false 时
// 签发与管理端点均未注册，请求落到 mux 的 404（§21.4 路由注册门禁）。
func TestM2MRoutesAbsentWhenDisabled(t *testing.T) {
	handler, err := rest.NewHandler(rest.HandlerConfig{Auth: &app.AuthService{}, Channel: domain.ChannelAdmin})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}
	if handler.M2MEnabled() {
		t.Fatalf("m2m must be disabled when no service is injected")
	}
	mux := http.NewServeMux()
	admin.Register(mux, handler, passthrough, nil)

	cases := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/admin/v1/auth/token"},
		{method: http.MethodGet, path: "/admin/v1/auth/m2m/clients"},
		{method: http.MethodPost, path: "/admin/v1/auth/m2m/clients"},
		{method: http.MethodPost, path: "/admin/v1/auth/m2m/clients/1/secrets"},
		{method: http.MethodPost, path: "/admin/v1/auth/m2m/clients/1/disable"},
		{method: http.MethodPost, path: "/admin/v1/auth/m2m/clients/1/enable"},
		{method: http.MethodDelete, path: "/admin/v1/auth/m2m/clients/1"},
	}
	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, nil)
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s: status = %d, want 404", tc.method, tc.path, recorder.Code)
		}
	}
}

// TestScimRoutesAbsentWhenDisabled 验证 identity.scim.enabled=false 时
// SCIM 管理端点与协议端点都未注册，请求落到 mux 的 404（§21.1）。
func TestScimRoutesAbsentWhenDisabled(t *testing.T) {
	handler, err := rest.NewHandler(rest.HandlerConfig{Auth: &app.AuthService{}, Channel: domain.ChannelAdmin})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}
	if handler.SCIMEnabled() {
		t.Fatalf("scim must be disabled when no service is injected")
	}
	mux := http.NewServeMux()
	admin.Register(mux, handler, passthrough, nil)

	cases := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/admin/v1/auth/scim/tokens"},
		{method: http.MethodPost, path: "/admin/v1/auth/scim/tokens"},
		{method: http.MethodDelete, path: "/admin/v1/auth/scim/tokens/1"},
		{method: http.MethodGet, path: "/admin/v1/auth/scim/groups"},
		// 协议端点由 transport/scim 单独注册；关闭时必须完全缺席。
		{method: http.MethodGet, path: "/admin/scim/v2/ServiceProviderConfig"},
		{method: http.MethodGet, path: "/admin/scim/v2/Users"},
	}
	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, nil)
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s: status = %d, want 404", tc.method, tc.path, recorder.Code)
		}
	}
}
