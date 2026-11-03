package app

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// stubChecker 是 TenantStatusChecker 的最小桩。
type stubChecker struct {
	allow bool
	err   error
	calls int
}

func (s *stubChecker) Verify(context.Context, string) (bool, error) {
	s.calls++
	return s.allow, s.err
}

// requireCode 断言错误业务码。
func requireCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want code %d, got nil", want)
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != want {
		t.Fatalf("error = %v, want code %d", err, want)
	}
}

// TestCheckTenantForProvision 验证账号开通的租户校验钩子（T-10）。
//
// 未来 B 端邀请流（T-14）建号必须经过这里；当前 C 端自注册商户为空，天然放行。
func TestCheckTenantForProvision(t *testing.T) {
	ctx := context.Background()

	// 空商户（A/C 端）直接放行，不触发检查器。
	checker := &stubChecker{allow: false}
	if err := checkTenantForProvision(ctx, checker, ""); err != nil {
		t.Fatalf("空商户 error = %v", err)
	}
	if checker.calls != 0 {
		t.Fatalf("空商户不应触发检查，实际 %d 次", checker.calls)
	}

	// 未配置检查器（A/C 端进程、内存模式）直接放行。
	if err := checkTenantForProvision(ctx, nil, "m-1"); err != nil {
		t.Fatalf("nil 检查器 error = %v", err)
	}

	// 明确拒绝 → 403。
	checker = &stubChecker{allow: false}
	requireCode(t, checkTenantForProvision(ctx, checker, "m-1"), apperr.CodeForbidden)

	// 无法判定 → 503 失败关闭。
	checker = &stubChecker{err: errors.New("db down")}
	requireCode(t, checkTenantForProvision(ctx, checker, "m-1"), apperr.CodeServiceUnavailable)

	// 在服务期 → 放行。
	checker = &stubChecker{allow: true}
	if err := checkTenantForProvision(ctx, checker, "m-1"); err != nil {
		t.Fatalf("在服务期 error = %v", err)
	}
}
