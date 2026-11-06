package c

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
)

// newSAMLTestHandler 构造带（可为空的）SAML 服务的 Handler。
func newSAMLTestHandler(t *testing.T, samlSvc *app.SAMLService, redirect string) *Handler {
	t.Helper()
	restHandler, err := rest.NewHandler(rest.HandlerConfig{Auth: &app.AuthService{}, Channel: domain.ChannelC})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}
	handler, err := NewHandler(HandlerConfig{
		Rest: restHandler, SAML: samlSvc, SAMLRedirectURL: redirect,
	})
	if err != nil {
		t.Fatalf("new c handler: %v", err)
	}
	return handler
}

// TestSAMLEndpointsUnavailableWhenDisabled 校验未启用时 handler 统一 50300。
func TestSAMLEndpointsUnavailableWhenDisabled(t *testing.T) {
	handler := newSAMLTestHandler(t, nil, "")
	cases := []struct {
		name   string
		call   func(http.ResponseWriter, *http.Request)
		method string
		path   string
		body   string
		ctype  string
	}{
		{"authorize", handler.SAMLAuthorize, http.MethodPost, "/c/v1/auth/saml/test/authorize", "", "application/json"},
		{"acs", handler.SAMLACS, http.MethodPost, "/c/v1/auth/saml/acs", "SAMLResponse=x", "application/x-www-form-urlencoded"},
		{"exchange", handler.SAMLExchange, http.MethodPost, "/c/v1/auth/saml/exchange", `{"ticket":"t"}`, "application/json"},
		{"metadata", handler.SAMLMetadata, http.MethodGet, "/c/v1/auth/saml/metadata", "", ""},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.ctype != "" {
			req.Header.Set("Content-Type", tc.ctype)
		}
		recorder := httptest.NewRecorder()
		tc.call(recorder, req)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s = %d, want 503", tc.name, recorder.Code)
		}
		if code := errorCode(t, recorder); code != 50300 {
			t.Fatalf("%s code = %d, want 50300", tc.name, code)
		}
	}
}

// TestSAMLACSRejectsUnsupportedContentType 校验非表单请求返回 415。
func TestSAMLACSRejectsUnsupportedContentType(t *testing.T) {
	handler := newSAMLTestHandler(t, &app.SAMLService{}, "https://c.example.com/login")
	req := httptest.NewRequest(http.MethodPost, "/c/v1/auth/saml/acs",
		strings.NewReader(`{"SAMLResponse":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.SAMLACS(recorder, req)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", recorder.Code)
	}
}

// TestSAMLACSRejectsOversizedBody 校验超过 512KB 的请求体返回 413。
func TestSAMLACSRejectsOversizedBody(t *testing.T) {
	handler := newSAMLTestHandler(t, &app.SAMLService{}, "https://c.example.com/login")
	body := "SAMLResponse=" + strings.Repeat("A", samlACSMaxBodyBytes+1024)
	req := httptest.NewRequest(http.MethodPost, "/c/v1/auth/saml/acs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler.SAMLACS(recorder, req)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", recorder.Code)
	}
}

// TestSAMLACSRejectsMalformedForm 校验非法表单体返回 400。
func TestSAMLACSRejectsMalformedForm(t *testing.T) {
	handler := newSAMLTestHandler(t, &app.SAMLService{}, "https://c.example.com/login")
	req := httptest.NewRequest(http.MethodPost, "/c/v1/auth/saml/acs", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler.SAMLACS(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

// TestSAMLMetadataUnavailableWithoutConfig 校验未生成元数据时返回 40400。
func TestSAMLMetadataUnavailableWithoutConfig(t *testing.T) {
	handler := newSAMLTestHandler(t, &app.SAMLService{}, "")
	recorder := httptest.NewRecorder()
	handler.SAMLMetadata(recorder, httptest.NewRequest(http.MethodGet, "/c/v1/auth/saml/metadata", http.NoBody))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

// TestSAMLRoutesRegisteredOnlyWhenEnabled 校验路由裁剪。
func TestSAMLRoutesRegisteredOnlyWhenEnabled(t *testing.T) {
	disabled := http.NewServeMux()
	Register(disabled, newSAMLTestHandler(t, nil, ""), passthrough)
	req := httptest.NewRequest(http.MethodGet, "/c/v1/auth/saml/metadata", http.NoBody)
	recorder := httptest.NewRecorder()
	disabled.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled status = %d, want 404", recorder.Code)
	}

	enabled := http.NewServeMux()
	Register(enabled, newSAMLTestHandler(t, &app.SAMLService{}, "https://c.example.com/login"), passthrough)
	recorder = httptest.NewRecorder()
	enabled.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound || errorCode(t, recorder) != 40400 {
		t.Fatalf("enabled status = %d body=%s, want 40400", recorder.Code, recorder.Body.String())
	}
}
