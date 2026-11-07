package platform_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// stubQuotaGuard 记录调用并按预设返回（ENT-14）。
type stubQuotaGuard struct {
	retryAfter int
	err        error
	calls      int
	lastTenant string
}

func (g *stubQuotaGuard) AllowRequest(_ context.Context, tenantID string) (int, error) {
	g.calls++
	g.lastTenant = tenantID
	return g.retryAfter, g.err
}

// TestWithQuotaGuardAllowsWithinQuota 验证配额内放行并透传租户。
func TestWithQuotaGuardAllowsWithinQuota(t *testing.T) {
	guard := &stubQuotaGuard{}
	handler := platform.WithQuotaGuard(identityMW, guard.AllowRequest)(okHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest("m1"))

	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if guard.calls != 1 || guard.lastTenant != "m1" {
		t.Fatalf("guard calls=%d last=%q", guard.calls, guard.lastTenant)
	}
}

// TestWithQuotaGuardRejectsExhausted 验证耗尽返回 42900 + Retry-After。
func TestWithQuotaGuardRejectsExhausted(t *testing.T) {
	guard := &stubQuotaGuard{retryAfter: 7}
	handler := platform.WithQuotaGuard(identityMW, guard.AllowRequest)(okHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest("m1"))

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if code := decodeCode(t, recorder); code != 42900 {
		t.Fatalf("业务码 = %d, want 42900", code)
	}
	if retry := recorder.Header().Get("Retry-After"); retry != "7" {
		t.Fatalf("Retry-After = %q, want 7", retry)
	}
}

// TestWithQuotaGuardFailsOpenOnError 验证执行器故障放行（配额不比业务更脆）。
func TestWithQuotaGuardFailsOpenOnError(t *testing.T) {
	guard := &stubQuotaGuard{err: errors.New("backend down")}
	handler := platform.WithQuotaGuard(identityMW, guard.AllowRequest)(okHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest("m1"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("backend failure must fail-open, status = %d", recorder.Code)
	}
}

// TestWithQuotaGuardSkipsPlatformRequests 验证无租户上下文的平台级请求不扣减。
func TestWithQuotaGuardSkipsPlatformRequests(t *testing.T) {
	guard := &stubQuotaGuard{}
	handler := platform.WithQuotaGuard(identityMW, guard.AllowRequest)(okHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/v1/anything", http.NoBody))

	if recorder.Code != http.StatusOK || guard.calls != 0 {
		t.Fatalf("platform request must skip quota: status=%d calls=%d", recorder.Code, guard.calls)
	}
}
