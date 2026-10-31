package biz_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/transport/biz"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
)

// passthrough 是测试用鉴权中间件。
func passthrough(next http.Handler) http.Handler { return next }

// TestAuthRoutesNoStore 覆盖 B 端登录、登出与 /me 的禁止缓存头。
func TestAuthRoutesNoStore(t *testing.T) {
	handler, err := rest.NewHandler(rest.HandlerConfig{Auth: &app.AuthService{}, Channel: domain.ChannelBiz})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}
	mux := http.NewServeMux()
	biz.Register(mux, handler, passthrough)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "login", method: http.MethodPost, path: "/biz/v1/auth/login", body: `{"subject":`},
		{name: "me", method: http.MethodGet, path: "/biz/v1/me"},
		{name: "update_me", method: http.MethodPatch, path: "/biz/v1/me", body: `{"display_name":`},
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
