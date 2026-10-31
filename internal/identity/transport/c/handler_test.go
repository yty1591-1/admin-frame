package c

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// TestTokenResponseFromIncludesAbsoluteExpiresAt 验证 C 端令牌响应与 A/B 端对齐，
// 返回设备绝对到期时间（IDN-GAP-MNT-01）。
func TestTokenResponseFromIncludesAbsoluteExpiresAt(t *testing.T) {
	absolute := time.Date(2026, 11, 30, 12, 0, 0, 0, time.UTC)
	got := tokenResponseFrom(app.TokenPair{
		AccessToken:       "access",
		RefreshToken:      "refresh",
		ExpiresIn:         60,
		AbsoluteExpiresAt: absolute,
	})
	if got.AbsoluteExpiresAt == nil || !got.AbsoluteExpiresAt.Equal(absolute) {
		t.Fatalf("absolute_expires_at = %v, want %v", got.AbsoluteExpiresAt, absolute)
	}
	if zero := tokenResponseFrom(app.TokenPair{ExpiresIn: 60}); zero.AbsoluteExpiresAt != nil {
		t.Fatalf("zero absolute expiry must be omitted, got %v", zero.AbsoluteExpiresAt)
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

// TestAuthRoutesNoStore 断言 C 端认证响应统一带禁止缓存头（IDN-P2-03）。
func TestAuthRoutesNoStore(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, newTestHandler(t, "test"), passthrough)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/c/v1/auth/login",
		strings.NewReader(`{"phone":"+8613800138000","code":"123456"}`))
	mux.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("cache-control=%q, want no-store", got)
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

func TestRegisterNilHandler(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil, passthrough)
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, "/c/v1/auth/login", http.NoBody))
	if pattern != "" {
		t.Fatalf("nil handler must not register routes, got pattern %q", pattern)
	}
}
