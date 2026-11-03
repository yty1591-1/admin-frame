package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// membershipStub 是成员关系检查桩（T-22），记录调用并按预设返回。
type membershipStub struct {
	members  map[string]bool // "tenant:subject" → 是否在册
	err      error
	lastCall string
}

func (s *membershipStub) IsMember(_ context.Context, tenantID, subjectID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	s.lastCall = tenantID + ":" + subjectID
	return s.members[s.lastCall], nil
}

// withMembership 向认证服务注入成员关系检查器。
func withMembership(m app.MembershipChecker) withAuthConfig {
	return func(cfg *app.AuthServiceConfig) { cfg.Membership = m }
}

// switchSession 为账号种一个活跃会话，返回 sessionID。
func switchSession(t *testing.T, f *authFixture, accountID string) string {
	t.Helper()
	ctx := context.Background()
	now := f.clock.Now().UTC()
	family, err := domain.NewSessionFamily(domain.NewSessionFamilyParams{
		ID:        "fam-switch-" + accountID,
		AccountID: accountID,
		ExpiresAt: now.Add(time.Hour),
		Now:       now,
	})
	if err != nil {
		t.Fatalf("new family: %v", err)
	}
	if err := f.sessions.SaveFamily(ctx, family, 0); err != nil {
		t.Fatalf("save family: %v", err)
	}
	session, err := domain.NewSession(domain.NewSessionParams{
		ID:           "fam-switch-" + accountID,
		AccountID:    accountID,
		FamilyID:     "fam-switch-" + accountID,
		RefreshHash:  "hash-switch-" + accountID,
		TokenVersion: 1,
		ExpiresAt:    now.Add(time.Hour),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := f.sessions.Save(ctx, session, 0); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return session.ID
}

// TestSwitchTenantIssuesTokenForMember 验证成员切换成功：同会话重签 + current_tenant 声明。
func TestSwitchTenantIssuesTokenForMember(t *testing.T) {
	membership := &membershipStub{members: map[string]bool{"t-2:acct-biz": true}}
	fixture := newAuthFixture(t, domain.ChannelBiz, withTenantCheck(&tenantCheckStub{allow: true}),
		withMembership(membership))
	account := fixture.seedAccount(t, "acct-biz", "switch@biz.example.com", "pw-123456")
	sessionID := switchSession(t, fixture, account.ID)

	result, err := fixture.svc.SwitchTenant(context.Background(), app.SwitchTenantInput{
		SessionActor: app.SessionActor{AccountID: account.ID, SessionID: sessionID, MerchantID: "m-1"},
		TenantID:     "t-2",
	})
	if err != nil {
		t.Fatalf("SwitchTenant() error = %v", err)
	}
	if result.CurrentTenant != "t-2" || result.AccessToken == "" || result.ExpiresIn <= 0 {
		t.Fatalf("result = %+v", result)
	}
	// 新令牌必须携带 current_tenant 声明且可验签。
	claims, err := fixture.signer.Parse(result.AccessToken)
	if err != nil {
		t.Fatalf("verify access token: %v", err)
	}
	if got := claims.GetString("current_tenant"); got != "t-2" {
		t.Fatalf("current_tenant = %q, want t-2", got)
	}
	// 同会话：sid 不变，merchant_id 跟随 current_tenant（兼容回落口径）。
	if got := claims.GetString("sid"); got != sessionID {
		t.Fatalf("sid = %q, want %q", got, sessionID)
	}
	if got := claims.GetString("merchant_id"); got != "t-2" {
		t.Fatalf("merchant_id = %q, want t-2（与 current_tenant 同值签发）", got)
	}
	// 成员关系确实被查询。
	if membership.lastCall != "t-2:acct-biz" {
		t.Fatalf("membership 查询 = %q", membership.lastCall)
	}
}

// TestSwitchTenantFailClosed 验证切换的失败关闭语义。
func TestSwitchTenantFailClosed(t *testing.T) {
	t.Run("not_a_member", func(t *testing.T) {
		membership := &membershipStub{members: map[string]bool{}}
		fixture := newAuthFixture(t, domain.ChannelBiz, withTenantCheck(&tenantCheckStub{allow: true}),
			withMembership(membership))
		account := fixture.seedAccount(t, "acct-x", "x@biz.example.com", "pw-123456")
		sessionID := switchSession(t, fixture, account.ID)

		_, err := fixture.svc.SwitchTenant(context.Background(), app.SwitchTenantInput{
			SessionActor: app.SessionActor{AccountID: account.ID, SessionID: sessionID},
			TenantID:     "t-other",
		})
		requireAppCode(t, err, apperr.CodeForbidden)
	})

	t.Run("membership_checker_nil_rejects", func(t *testing.T) {
		fixture := newAuthFixture(t, domain.ChannelBiz, withTenantCheck(&tenantCheckStub{allow: true}))
		account := fixture.seedAccount(t, "acct-y", "y@biz.example.com", "pw-123456")
		sessionID := switchSession(t, fixture, account.ID)

		// 未注入 membership：宁可拒绝也不放行（装配缺失不能变成免检通道）。
		_, err := fixture.svc.SwitchTenant(context.Background(), app.SwitchTenantInput{
			SessionActor: app.SessionActor{AccountID: account.ID, SessionID: sessionID},
			TenantID:     "t-1",
		})
		requireAppCode(t, err, apperr.CodeForbidden)
	})

	t.Run("membership_error_fails_closed", func(t *testing.T) {
		membership := &membershipStub{err: errors.New("member db down")}
		fixture := newAuthFixture(t, domain.ChannelBiz, withTenantCheck(&tenantCheckStub{allow: true}),
			withMembership(membership))
		account := fixture.seedAccount(t, "acct-z", "z@biz.example.com", "pw-123456")
		sessionID := switchSession(t, fixture, account.ID)

		_, err := fixture.svc.SwitchTenant(context.Background(), app.SwitchTenantInput{
			SessionActor: app.SessionActor{AccountID: account.ID, SessionID: sessionID},
			TenantID:     "t-1",
		})
		requireAppCode(t, err, apperr.CodeServiceUnavailable)
	})

	t.Run("suspended_tenant_rejected", func(t *testing.T) {
		membership := &membershipStub{members: map[string]bool{"t-down:acct-w": true}}
		fixture := newAuthFixture(t, domain.ChannelBiz, withTenantCheck(&tenantCheckStub{allow: false}),
			withMembership(membership))
		account := fixture.seedAccount(t, "acct-w", "w@biz.example.com", "pw-123456")
		sessionID := switchSession(t, fixture, account.ID)

		_, err := fixture.svc.SwitchTenant(context.Background(), app.SwitchTenantInput{
			SessionActor: app.SessionActor{AccountID: account.ID, SessionID: sessionID},
			TenantID:     "t-down",
		})
		requireAppCode(t, err, apperr.CodeForbidden)
	})

	t.Run("unknown_session_rejected", func(t *testing.T) {
		membership := &membershipStub{members: map[string]bool{"t-1:acct-v": true}}
		fixture := newAuthFixture(t, domain.ChannelBiz, withTenantCheck(&tenantCheckStub{allow: true}),
			withMembership(membership))
		account := fixture.seedAccount(t, "acct-v", "v@biz.example.com", "pw-123456")

		_, err := fixture.svc.SwitchTenant(context.Background(), app.SwitchTenantInput{
			SessionActor: app.SessionActor{AccountID: account.ID, SessionID: "no-such-session"},
			TenantID:     "t-1",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
	})

	t.Run("admin_channel_rejected", func(t *testing.T) {
		fixture := newAuthFixture(t, domain.ChannelAdmin)
		_, err := fixture.svc.SwitchTenant(context.Background(), app.SwitchTenantInput{
			SessionActor: app.SessionActor{AccountID: "a", SessionID: "s"},
			TenantID:     "t-1",
		})
		requireAppCode(t, err, apperr.CodeBadRequest)
	})
}

// TestIssueCarriesCurrentTenantClaim 验证登录签发的令牌自带 current_tenant（= merchant_id）。
func TestIssueCarriesCurrentTenantClaim(t *testing.T) {
	fixture := newAuthFixture(t, domain.ChannelBiz)
	account := fixture.seedAccount(t, "acct-claim", "claim@biz.example.com", "pw-123456")

	// 直接走 TokenService.Issue（登录用例内部同一路径）。
	pair, err := fixture.tokens.Issue(account, "sess-claim")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	claims, err := fixture.signer.Parse(pair.AccessToken)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// B 端签发：current_tenant 与 merchant_id 同值，旧客户端与新切换链路共用回落语义。
	if got := claims.GetString("current_tenant"); got != "m-1" {
		t.Fatalf("current_tenant = %q, want m-1", got)
	}
	if !strings.HasPrefix(pair.RefreshToken, app.RefreshPrefixBiz) {
		t.Fatalf("refresh 前缀 = %q", pair.RefreshToken[:4])
	}
}
