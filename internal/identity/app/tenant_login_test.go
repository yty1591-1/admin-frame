package app_test

import (
	"context"
	"errors"
	"testing"

	app "github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// tenantCheckStub 是租户状态检查桩，记录调用并按预设返回。
type tenantCheckStub struct {
	allow  bool
	err    error
	calls  int
	lastID string
}

func (s *tenantCheckStub) Verify(_ context.Context, merchantID string) (bool, error) {
	s.calls++
	s.lastID = merchantID
	return s.allow, s.err
}

// withTenantCheck 向认证服务注入租户检查器。
func withTenantCheck(checker app.TenantStatusChecker) withAuthConfig {
	return func(cfg *app.AuthServiceConfig) { cfg.TenantCheck = checker }
}

// TestLoginChecksTenantStatus 验证 B 端登录的租户校验（T-08）：
// 停用 403、仓储故障 503 失败关闭、放行时照常走凭据校验。
func TestLoginChecksTenantStatus(t *testing.T) {
	newFixtureWithCheck := func(t *testing.T, checker app.TenantStatusChecker) *authFixture {
		return newAuthFixture(t, domain.ChannelBiz, withTenantCheck(checker))
	}

	t.Run("suspended_tenant_rejected_before_credentials", func(t *testing.T) {
		checker := &tenantCheckStub{allow: false}
		fixture := newFixtureWithCheck(t, checker)

		_, err := fixture.svc.Login(context.Background(), app.LoginInput{
			Subject: "user@biz.example.com", MerchantID: "m-1",
			Password: "whatever-would-be-right", TraceID: "trace-t",
		})
		requireAppCode(t, err, apperr.CodeForbidden)
		if checker.lastID != "m-1" {
			t.Fatalf("检查的商户 = %q, want m-1", checker.lastID)
		}
	})

	t.Run("checker_failure_fails_closed", func(t *testing.T) {
		checker := &tenantCheckStub{err: errors.New("tenant db down")}
		fixture := newFixtureWithCheck(t, checker)

		_, err := fixture.svc.Login(context.Background(), app.LoginInput{
			Subject: "user@biz.example.com", MerchantID: "m-1",
			Password: "pw", TraceID: "trace-t",
		})
		requireAppCode(t, err, apperr.CodeServiceUnavailable)
	})

	t.Run("allowed_tenant_proceeds_to_credentials", func(t *testing.T) {
		checker := &tenantCheckStub{allow: true}
		fixture := newFixtureWithCheck(t, checker)

		_, err := fixture.svc.Login(context.Background(), app.LoginInput{
			Subject: "nobody@biz.example.com", MerchantID: "m-1",
			Password: "pw", TraceID: "trace-t",
		})
		// 账号不存在 → 401（凭据错误），说明租户检查已放行并进入凭据阶段。
		requireAppCode(t, err, apperr.CodeUnauthorized)
		if checker.calls != 1 {
			t.Fatalf("租户检查调用 %d 次, want 1", checker.calls)
		}
	})

	t.Run("admin_login_skips_tenant_check", func(t *testing.T) {
		checker := &tenantCheckStub{allow: false}
		fixture := newAuthFixture(t, domain.ChannelAdmin, withTenantCheck(checker))

		_, err := fixture.svc.Login(context.Background(), app.LoginInput{
			Subject: "admin@example.com", Password: "pw", TraceID: "trace-t",
		})
		// A 端无商户：即使检查器会拒绝也不应被调用。
		requireAppCode(t, err, apperr.CodeUnauthorized)
		if checker.calls != 0 {
			t.Fatalf("A 端不应触发租户检查，实际 %d 次", checker.calls)
		}
	})
}
