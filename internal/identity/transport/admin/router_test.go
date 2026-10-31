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
