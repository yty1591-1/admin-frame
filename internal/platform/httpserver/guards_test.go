package httpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// TestAfterAuthRunsGuardsInDeclaredOrder 验证守卫按声明顺序装配：
// 租户状态守卫先执行，被拒请求不再进入配额守卫（阶段 3 显式装配契约）。
func TestAfterAuthRunsGuardsInDeclaredOrder(t *testing.T) {
	var tenantCalls, quotaCalls int

	tenantMW := httpserver.TenantGuardMiddleware(
		func(context.Context, string) (bool, error) {
			tenantCalls++
			return false, nil
		},
		true,
	)
	quotaMW := httpserver.QuotaGuardMiddleware(
		func(context.Context, string) (int, error) {
			quotaCalls++
			return 0, nil
		},
	)
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := auth.Principal{Channel: "biz", SubjectID: "u1", MerchantID: "m1"}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}

	handler := httpserver.AfterAuth(authMW, tenantMW, quotaMW)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
	))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/biz/v1/anything", http.NoBody))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if tenantCalls != 1 {
		t.Fatalf("tenant guard calls = %d, want 1", tenantCalls)
	}
	if quotaCalls != 0 {
		t.Fatalf("quota guard calls = %d, want 0（租户守卫拒绝后不应继续）", quotaCalls)
	}
}
