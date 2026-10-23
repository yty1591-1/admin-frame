package c

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
)

// newTestHandler 构造最小 C 端 Handler：短信与 OAuth 均未启用。
func newTestHandler(t *testing.T, providers ...string) *Handler {
	t.Helper()
	restHandler, err := rest.NewHandler(rest.HandlerConfig{Auth: &app.AuthService{}, Channel: domain.ChannelC})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}
	handler, err := NewHandler(HandlerConfig{Rest: restHandler, Providers: providers})
	if err != nil {
		t.Fatalf("new c handler: %v", err)
	}
	return handler
}

// passthrough 是测试用鉴权中间件。
func passthrough(next http.Handler) http.Handler { return next }

// errorCode 解析统一错误信封中的 code。
func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) int {
	t.Helper()
	var envelope struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope from %q: %v", recorder.Body.String(), err)
	}
	return envelope.Code
}

func TestNewHandlerRequiresRest(t *testing.T) {
	if _, err := NewHandler(HandlerConfig{}); err == nil {
		t.Fatal("rest handler must be required")
	}
}

func TestRegisterRoutes(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, newTestHandler(t, "test"), passthrough)

	cases := []struct {
		name     string
		method   string
		path     string
		body     string
		wantHTTP int
		wantCode int
	}{
		{
			name: "sms_disabled_route_absent", method: http.MethodPost, path: "/c/v1/auth/sms/send",
			body: `{"phone":"+8613800138000"}`, wantHTTP: http.StatusNotFound,
		},
		{
			name: "login_disabled_returns_50300", method: http.MethodPost, path: "/c/v1/auth/login",
			body:     `{"phone":"+8613800138000","code":"123456"}`,
			wantHTTP: http.StatusServiceUnavailable, wantCode: 50300,
		},
		{
			name: "login_invalid_body_returns_40000", method: http.MethodPost, path: "/c/v1/auth/login",
			body: `{"phone":`, wantHTTP: http.StatusBadRequest, wantCode: 40000,
		},
		{
			name: "login_unknown_field_rejected", method: http.MethodPost, path: "/c/v1/auth/login",
			body:     `{"phone":"+8613800138000","code":"123456","password":"x"}`,
			wantHTTP: http.StatusBadRequest, wantCode: 40000,
		},
		{
			name: "configured_provider_authorize_returns_50300", method: http.MethodPost,
			path: "/c/v1/auth/oauth/test/authorize", wantHTTP: http.StatusServiceUnavailable, wantCode: 50300,
		},
		{
			name: "unconfigured_provider_route_absent", method: http.MethodPost,
			path: "/c/v1/auth/oauth/other/authorize", wantHTTP: http.StatusNotFound,
		},
		{
			name: "login_wrong_method", method: http.MethodGet, path: "/c/v1/auth/login",
			wantHTTP: http.StatusMethodNotAllowed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body *strings.Reader
			if tc.body == "" {
				body = strings.NewReader("")
			} else {
				body = strings.NewReader(tc.body)
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, body)
			mux.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantHTTP {
				t.Fatalf("status = %d, want %d (%s)", recorder.Code, tc.wantHTTP, recorder.Body.String())
			}
			if tc.wantCode != 0 {
				if got := errorCode(t, recorder); got != tc.wantCode {
					t.Fatalf("code = %d, want %d", got, tc.wantCode)
				}
			}
		})
	}
}

func TestRegisterNilHandler(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil, passthrough)
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, "/c/v1/auth/login", nil))
	if pattern != "" {
		t.Fatalf("nil handler must not register routes, got pattern %q", pattern)
	}
}
