package c

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
)

// TestOAuthRoutesResolveProviderFromPath 验证 OAuth 登录路由经 Register 注册后能解析 {provider}。
//
// 回归背景：若按字面量 provider 拼接注册，PathValue("provider") 为空，
// 所有真实 OAuth 请求都会被用例层拒绝为 40400。
func TestOAuthRoutesResolveProviderFromPath(t *testing.T) {
	fixture := newCFixture(t)

	cases := []struct {
		name     string
		path     string
		wantCode int
	}{
		{name: "authorize known provider", path: "/c/v1/auth/oauth/test/authorize", wantCode: pkghttpx.CodeOK},
		{name: "authorize unknown provider", path: "/c/v1/auth/oauth/unknown/authorize", wantCode: apperr.CodeNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, tc.path, http.NoBody)
			fixture.mux.ServeHTTP(recorder, request)

			var envelope pkghttpx.Envelope
			if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if envelope.Code != tc.wantCode {
				t.Fatalf("code=%d want %d (body=%s)", envelope.Code, tc.wantCode, recorder.Body.String())
			}
		})
	}
}
