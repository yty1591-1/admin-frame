package rest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAuthBodyLimitRejectsOversizedBeforeHandler 验证认证路由独立请求体上限（SEC-03）。
func TestAuthBodyLimitRejectsOversizedBeforeHandler(t *testing.T) {
	called := false
	handler := AuthBodyLimit(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	oversized := httptest.NewRequest("POST", "/admin/v1/auth/login",
		strings.NewReader(strings.Repeat("a", int(AuthBodyLimitBytes)+1)))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, oversized)
	if called {
		t.Fatal("oversized request must be rejected before handler")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}

	normal := httptest.NewRequest("POST", "/admin/v1/auth/login",
		strings.NewReader(`{"subject":"alice"}`))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, normal)
	if !called {
		t.Fatal("normal request must reach handler")
	}
}
