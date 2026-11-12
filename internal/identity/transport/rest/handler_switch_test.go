package rest

// 本文件覆盖租户切换端点（handler_switch.go）的成功与失败路径。

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// switchMembershipChecker 是固定返回值的成员关系检查器。
type switchMembershipChecker struct {
	member bool
}

// IsMember 按测试配置返回成员资格。
func (c switchMembershipChecker) IsMember(context.Context, string, string) (bool, error) {
	return c.member, nil
}

// withE2EMembership 注入租户成员关系检查器。
func withE2EMembership(checker app.MembershipChecker) e2eOption {
	return func(cfg *app.AuthServiceConfig, _ *HandlerConfig) {
		cfg.Membership = checker
	}
}

// newSwitchTenantFixture 构造带 active 账号与 active 会话的 B 端夹具。
func newSwitchTenantFixture(t *testing.T, member bool) (f *e2eFixture, accountID, sessionID string) {
	t.Helper()
	f = newE2EFixture(t, domain.ChannelBiz, withE2EMembership(switchMembershipChecker{member: member}))
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: "acct-biz", Channel: domain.ChannelBiz, Subject: "biz@example.com",
		MerchantID: "merchant-1", Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := account.Activate(f.clock.Now()); err != nil {
		t.Fatalf("activate account: %v", err)
	}
	if err := f.accounts.Save(context.Background(), account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	session, err := domain.NewSession(domain.NewSessionParams{
		ID: "sess-biz", AccountID: account.ID, FamilyID: "fam-biz",
		RefreshHash: "refresh-hash", TokenVersion: account.TokenVersion,
		ExpiresAt: f.clock.Now().Add(time.Hour), Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := f.sessions.Save(context.Background(), session, 0); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return f, account.ID, session.ID
}

// TestSwitchTenant 覆盖同会话重签成功与鉴权 / 租户校验失败。
func TestSwitchTenant(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f, accountID, sessionID := newSwitchTenantFixture(t, true)
		recorder := f.invoke(f.handler.SwitchTenant, e2eCall{
			method: http.MethodPost, target: "/biz/v1/auth/tenant-switch",
			body:      `{"tenant_id":"tenant-2"}`,
			principal: f.principal(accountID, sessionID),
		})
		requireStatus(t, recorder, http.StatusOK)
		var payload switchTenantResponse
		decodeData(t, recorder, &payload)
		if payload.AccessToken == "" || payload.TokenType != "Bearer" || payload.ExpiresIn <= 0 {
			t.Fatalf("switch response = %+v", payload)
		}
		if payload.CurrentTenant != "tenant-2" {
			t.Fatalf("current_tenant = %q, want tenant-2", payload.CurrentTenant)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		f, _, _ := newSwitchTenantFixture(t, true)
		recorder := f.invoke(f.handler.SwitchTenant, e2eCall{
			method: http.MethodPost, target: "/biz/v1/auth/tenant-switch",
			body: `{"tenant_id":"tenant-2"}`,
		})
		requireStatus(t, recorder, http.StatusUnauthorized)
		requireErrorCode(t, recorder, apperr.CodeUnauthorized)
	})

	t.Run("missing_tenant", func(t *testing.T) {
		f, accountID, sessionID := newSwitchTenantFixture(t, true)
		recorder := f.invoke(f.handler.SwitchTenant, e2eCall{
			method: http.MethodPost, target: "/biz/v1/auth/tenant-switch",
			body:      `{}`,
			principal: f.principal(accountID, sessionID),
		})
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, apperr.CodeBadRequest)
	})

	t.Run("not_a_member", func(t *testing.T) {
		f, accountID, sessionID := newSwitchTenantFixture(t, false)
		recorder := f.invoke(f.handler.SwitchTenant, e2eCall{
			method: http.MethodPost, target: "/biz/v1/auth/tenant-switch",
			body:      `{"tenant_id":"tenant-2"}`,
			principal: f.principal(accountID, sessionID),
		})
		requireStatus(t, recorder, http.StatusForbidden)
		requireErrorCode(t, recorder, apperr.CodeForbidden)
	})
}
