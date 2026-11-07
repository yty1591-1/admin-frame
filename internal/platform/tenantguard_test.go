package platform_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// stubGuard 记录调用并按预设返回。
type stubGuard struct {
	allow  bool
	err    error
	calls  int
	lastID string
}

func (g *stubGuard) Verify(_ context.Context, merchantID string) (bool, error) {
	g.calls++
	g.lastID = merchantID
	return g.allow, g.err
}

// okHandler 是被守卫包裹的业务处理器。
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// guardRequest 构造带 Principal 的请求。
func guardRequest(merchantID string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/biz/v1/anything", http.NoBody)
	principal := auth.Principal{Channel: "biz", SubjectID: "u1", MerchantID: merchantID}
	return request.WithContext(auth.WithPrincipal(request.Context(), principal))
}

// decodeCode 解析统一信封的业务码。
func decodeCode(t *testing.T, recorder *httptest.ResponseRecorder) int {
	t.Helper()
	var envelope struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("响应不是合法 JSON: %v（body=%s）", err, recorder.Body.String())
	}
	return envelope.Code
}

// TestWithTenantGuardAllowsServableTenant 验证放行路径不改变响应。
func TestWithTenantGuardAllowsServableTenant(t *testing.T) {
	guard := &stubGuard{allow: true}
	handler := httpserver.WithTenantGuard(identityMW, guard.Verify, false)(okHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest("m1"))

	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if guard.calls != 1 || guard.lastID != "m1" {
		t.Fatalf("guard calls=%d last=%q", guard.calls, guard.lastID)
	}
}

// TestWithTenantGuardDeniesSuspendedTenant 验证拒绝返回 403 与统一错误码。
func TestWithTenantGuardDeniesSuspendedTenant(t *testing.T) {
	handler := httpserver.WithTenantGuard(identityMW, (&stubGuard{allow: false}).Verify, false)(okHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest("m1"))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if code := decodeCode(t, recorder); code != 40300 {
		t.Fatalf("业务码 = %d, want 40300", code)
	}
}

// TestWithTenantGuardFailsClosedOnError 验证判定失败返回 503 而不是放行 / 403。
func TestWithTenantGuardFailsClosedOnError(t *testing.T) {
	handler := httpserver.WithTenantGuard(identityMW, (&stubGuard{err: errors.New("db down")}).Verify, false)(okHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest("m1"))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	if code := decodeCode(t, recorder); code != 50300 {
		t.Fatalf("业务码 = %d, want 50300", code)
	}
}

// TestWithTenantGuardSkipsNonTenantRequests 验证平台级请求（无 merchant_id）与
// 未认证请求都不触发守卫。
func TestWithTenantGuardSkipsNonTenantRequests(t *testing.T) {
	guard := &stubGuard{allow: false}
	handler := httpserver.WithTenantGuard(identityMW, guard.Verify, false)(okHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest(""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("平台级请求 status = %d, want 200", recorder.Code)
	}

	anonymous := httptest.NewRequest(http.MethodGet, "/biz/v1/anything", http.NoBody)
	anonRecorder := httptest.NewRecorder()
	handler.ServeHTTP(anonRecorder, anonymous)
	if anonRecorder.Code != http.StatusOK {
		t.Fatalf("未认证请求应由下游处理，status = %d", anonRecorder.Code)
	}
	if guard.calls != 0 {
		t.Fatalf("不应调用守卫，calls = %d", guard.calls)
	}
}

// TestWithTenantGuardNilIsNoop 验证未注入实现时中间件保持原行为。
func TestWithTenantGuardNilIsNoop(t *testing.T) {
	handler := httpserver.WithTenantGuard(identityMW, nil, false)(okHandler())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest("m1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

// identityMW 是空鉴权中间件：本文件只验证守卫行为，鉴权由 security 包单测覆盖。
func identityMW(next http.Handler) http.Handler { return next }

// TestWithTenantGuardRejectsMissingMerchantOnBiz 验证 B 端缺商户令牌 fail-closed（T-09）。
//
// 旧实现 merchant_id 为空直接放行：签发缺陷会让令牌静默绕过全部租户校验。
func TestWithTenantGuardRejectsMissingMerchantOnBiz(t *testing.T) {
	guard := &stubGuard{allow: true}
	handler := httpserver.WithTenantGuard(identityMW, guard.Verify, true)(okHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest(""))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("缺商户 status = %d, want 403（body=%s）", recorder.Code, recorder.Body.String())
	}
	if code := decodeCode(t, recorder); code != 40300 {
		t.Fatalf("code = %d, want 40300", code)
	}
	// 守卫根本不该被调用：没有商户就没有判定对象。
	if guard.calls != 0 {
		t.Fatalf("守卫被调用 %d 次, want 0", guard.calls)
	}

	// 同一配置下带商户的请求仍正常走守卫判定。
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest("m-1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("带商户 status = %d, want 200", recorder.Code)
	}
	if guard.lastID != "m-1" {
		t.Fatalf("守卫收到 %q, want m-1", guard.lastID)
	}
}

// TestWithTenantGuardAllowsMissingMerchantOffBiz 验证 A/C 端空商户仍放行。
func TestWithTenantGuardAllowsMissingMerchantOffBiz(t *testing.T) {
	guard := &stubGuard{allow: true}
	handler := httpserver.WithTenantGuard(identityMW, guard.Verify, false)(okHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, guardRequest(""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("A/C 端空商户 status = %d, want 200", recorder.Code)
	}
	if guard.calls != 0 {
		t.Fatalf("守卫不应被调用，实际 %d 次", guard.calls)
	}
}
