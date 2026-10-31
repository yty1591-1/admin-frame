package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/transport/admin"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
)

// TestErrorEnvelopeContract 锁定身份端点的错误信封字段与稳定业务码（BIZ-05）。
//
// 服务端只承诺数字 code + 可展示 message；前端按 code 做本地化，
// 因此 code 不得随文案调整而变化，这里对代表端点做回归保护。
func TestErrorEnvelopeContract(t *testing.T) {
	handler, err := rest.NewHandler(rest.HandlerConfig{Auth: &app.AuthService{}, Channel: domain.ChannelAdmin})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}
	mux := http.NewServeMux()
	admin.Register(mux, handler, passthrough, nil)

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   int
	}{
		{
			name: "login malformed json", method: http.MethodPost, path: "/admin/v1/auth/login",
			body: `{"subject":`, wantStatus: http.StatusBadRequest, wantCode: apperr.CodeBadRequest,
		},
		{
			name: "login unsupported content type", method: http.MethodPost, path: "/admin/v1/auth/login",
			body: `{}`, wantStatus: http.StatusBadRequest, wantCode: apperr.CodeBadRequest,
		},
		{
			name: "me unauthenticated", method: http.MethodGet, path: "/admin/v1/me",
			wantStatus: http.StatusUnauthorized, wantCode: apperr.CodeUnauthorized,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.name == "login unsupported content type" {
				request.Header.Set("Content-Type", "text/plain")
			}
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d want %d", recorder.Code, tc.wantStatus)
			}
			var envelope pkghttpx.Envelope
			if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if envelope.Code != tc.wantCode {
				t.Fatalf("code=%d want %d", envelope.Code, tc.wantCode)
			}
			if envelope.Message == "" {
				t.Fatal("error envelope must carry a client-facing message")
			}
			// 错误响应不得回传业务数据，避免把内部细节混进契约。
			if envelope.Data != nil {
				t.Fatalf("error envelope must not carry data: %v", envelope.Data)
			}
		})
	}
}
