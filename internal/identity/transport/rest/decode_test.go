package rest

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

type decodePayload struct {
	Subject string `json:"subject"`
}

// requireBadRequest 断言错误为 40000。
func requireBadRequest(t *testing.T, err error) {
	t.Helper()
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeBadRequest {
		t.Fatalf("err = %v, want 40000", err)
	}
}

// TestDecodeJSONStrict 验证尾随内容、错误 Content-Type 与未知字段均被拒绝（SEC-10）。
func TestDecodeJSONStrict(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		contentType string
		wantErr     bool
	}{
		{name: "valid", body: `{"subject":"alice"}`},
		{name: "valid json content type", body: `{"subject":"alice"}`, contentType: "application/json; charset=utf-8"},
		{name: "valid vendor json", body: `{"subject":"alice"}`, contentType: "application/vnd.api+json"},
		{name: "trailing object", body: `{"subject":"alice"}{"subject":"bob"}`, wantErr: true},
		{name: "trailing garbage", body: `{"subject":"alice"}garbage`, wantErr: true},
		{name: "wrong content type", body: `{"subject":"alice"}`, contentType: "text/plain", wantErr: true},
		{name: "unknown field", body: `{"subject":"alice","extra":1}`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/admin/v1/auth/login", strings.NewReader(tc.body))
			if tc.contentType != "" {
				request.Header.Set("Content-Type", tc.contentType)
			}
			var payload decodePayload
			err := DecodeJSON(request, &payload)
			if tc.wantErr {
				requireBadRequest(t, err)
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
		})
	}
}

// TestDecodeOptionalJSONEmptyBody 验证可选请求体为空时视为未提供。
func TestDecodeOptionalJSONEmptyBody(t *testing.T) {
	request := httptest.NewRequest("POST", "/admin/v1/auth/logout", strings.NewReader(""))
	var payload decodePayload
	if err := DecodeOptionalJSON(request, &payload); err != nil {
		t.Fatalf("empty optional body must be accepted: %v", err)
	}
	payload.Subject = ""
	if err := DecodeOptionalJSON(httptest.NewRequest("POST", "/x", nil), &payload); err != nil {
		t.Fatalf("nil body must be accepted: %v", err)
	}
}
