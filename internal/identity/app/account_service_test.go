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
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/totp"
)

// TestAccountServiceChangePassword 验证自助改密：旧密码校验、会话全撤销与审计。
func TestAccountServiceChangePassword(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
	f.seedAccount(t, "acct-pwd", "alice", "Old#Password12")

	first := f.login(t, "alice", "Old#Password12")
	second := f.login(t, "alice", "Old#Password12")

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: first.AccountID, SessionID: first.SessionID, TraceID: "trace-pwd"}

	requireAppCode(t, svc.ChangePassword(ctx, app.ChangePasswordInput{
		Actor: actor, OldPassword: "wrong-old", NewPassword: "New#Password12",
	}), apperr.CodeUnauthorized)

	requireAppCode(t, svc.ChangePassword(ctx, app.ChangePasswordInput{
		Actor: actor, OldPassword: "Old#Password12", NewPassword: "Old#Password12",
	}), apperr.CodeBadRequest)

	if err := svc.ChangePassword(ctx, app.ChangePasswordInput{
		Actor: actor, OldPassword: "Old#Password12", NewPassword: "New#Password12",
	}); err != nil {
		t.Fatalf("change password: %v", err)
	}

	account, err := f.accounts.FindByID(ctx, first.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.TokenVersion != 2 {
		t.Fatalf("token_version = %d, want 2", account.TokenVersion)
	}
	for _, id := range []string{first.SessionID, second.SessionID} {
		session := f.sessionOf(t, id)
		if session.Status != domain.SessionStatusRevoked || session.RevokedReason != domain.RevokeReasonPasswordChanged {
			t.Fatalf("session %s must be revoked by password change: %+v", id, session)
		}
	}
	_, err = f.svc.Login(ctx, app.LoginInput{Subject: "alice", Password: "Old#Password12"})
	requireAppCode(t, err, apperr.CodeUnauthorized)
	if _, err := f.svc.Login(ctx, app.LoginInput{Subject: "alice", Password: "New#Password12"}); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	if len(capture.byAction(audit.ActionAuthPasswordChanged)) != 1 {
		t.Fatalf("expected auth.password.changed, got %+v", capture.snapshot())
	}
}

// TestAccountServicePrivilegedStepUpMFA 验证管理员特权操作的二次 MFA。
func TestAccountServicePrivilegedStepUpMFA(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.MFAAdminRequired = true
	})
	f.seedAccount(t, "acct-operator", "operator", "Operator#Pass12")
	f.seedAccount(t, "acct-target", "target", "Target#Pass12")
	secret, _ := bindMFA(t, f, "acct-operator")
	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: "acct-operator", TraceID: "trace-stepup"}

	base := app.AccountOperationInput{
		Actor:           actor,
		TargetAccountID: "acct-target",
		Reason:          "incident-response",
	}
	if _, err := svc.ResetPassword(ctx, base); err != nil {
		requireAppCode(t, err, apperr.CodeBadRequest)
	} else {
		t.Fatal("reset password must require step-up mfa")
	}

	withCode := base
	withCode.MFACode = totpCodeAt(t, secret, f.clock.Now())
	if _, err := svc.ResetPassword(ctx, withCode); err != nil {
		t.Fatalf("reset password with step-up mfa: %v", err)
	}
}

// TestAccountServicePasswordHistoryRejectsReuse 验证窗口内历史密码复用被拒绝（SEC-06）。
func TestAccountServicePasswordHistoryRejectsReuse(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.PasswordHistoryCount = 5
		cfg.PasswordHistoryWindow = 365 * 24 * time.Hour
	})
	f.seedAccount(t, "acct-history", "alice", "Old#Password12")
	login := f.login(t, "alice", "Old#Password12")
	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: login.AccountID, SessionID: login.SessionID, TraceID: "trace-history"}

	for _, step := range []struct{ old, next string }{
		{"Old#Password12", "Mid#Password12"},
		{"Mid#Password12", "Next#Password12"},
	} {
		if err := svc.ChangePassword(ctx, app.ChangePasswordInput{
			Actor: actor, OldPassword: step.old, NewPassword: step.next,
		}); err != nil {
			t.Fatalf("change %s -> %s: %v", step.old, step.next, err)
		}
	}
	// 回退到最早的历史密码必须被拒绝。
	requireAppCode(t, svc.ChangePassword(ctx, app.ChangePasswordInput{
		Actor: actor, OldPassword: "Next#Password12", NewPassword: "Old#Password12",
	}), apperr.CodeBadRequest)

	credential, err := f.credentials.FindByAccount(ctx, login.AccountID, domain.CredentialTypePassword)
	if err != nil {
		t.Fatalf("load credential: %v", err)
	}
	if len(credential.PasswordHistory) != 2 {
		t.Fatalf("password history = %d, want 2", len(credential.PasswordHistory))
	}
}

// TestPasswordPolicyInvalidOverrideFallsBack 验证非法租户策略回退全局。
func TestPasswordPolicyInvalidOverrideFallsBack(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.PasswordPolicyFor = func(string) app.PasswordPolicy {
			return app.PasswordPolicy{MinLength: 1, MaxLength: 1, RequireComplexity: true}
		}
	})
	f.seedAccount(t, "acct-fallback", "fallback-user", "Fallback#Passw0rd")
	login := f.login(t, "fallback-user", "Fallback#Passw0rd")
	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	if err := svc.ChangePassword(ctx, app.ChangePasswordInput{
		Actor:       app.SessionActor{AccountID: login.AccountID, SessionID: login.SessionID},
		OldPassword: "Fallback#Passw0rd",
		NewPassword: "Fallback#Passw0rd2",
	}); err != nil {
		t.Fatalf("change password with fallback policy: %v", err)
	}
}

// TestRefreshBackfillsDevice 覆盖刷新时补齐历史设备画像分支。
func TestRefreshBackfillsDevice(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-device", "device-user", "Device#Passw0rd")
	login := f.login(t, "device-user", "Device#Passw0rd")
	if _, err := f.svc.Refresh(ctx, app.RefreshInput{
		RefreshToken: login.RefreshToken,
		Device: domain.DeviceInfo{
			Type: "desktop", OS: "windows", Browser: "chrome", IPMasked: "10.0.x.x",
		},
	}); err != nil {
		t.Fatalf("refresh with device: %v", err)
	}
}

// TestLoginEvictsOldestDevice 覆盖单账号设备上限与最旧设备淘汰（PERF-04 应用层）。
func TestLoginEvictsOldestDevice(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.MaxPerAccount = 2
	})
	f.seedAccount(t, "acct-evict", "evict-user", "Evict#Passw0rd")
	for i := 0; i < 3; i++ {
		f.login(t, "evict-user", "Evict#Passw0rd")
		f.clock.Advance(time.Minute)
	}
	page, err := f.svc.ListSessions(ctx, app.SessionListInput{
		SessionActor: app.SessionActor{AccountID: "acct-evict", TraceID: "trace-evict"},
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("active devices = %d, want 2", len(page.Items))
	}
}

// TestLoginAfterLockExpiry 覆盖锁定期满后自动解锁（SEC-05）。
func TestLoginAfterLockExpiry(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-expire", "expire-user", "Expire#Passw0rd")
	for i := 0; i < app.DefaultLockoutMaxFailures; i++ {
		_, _ = f.svc.Login(ctx, app.LoginInput{Subject: "expire-user", Password: "wrong"})
	}
	if _, err := f.svc.Login(ctx, app.LoginInput{
		Subject: "expire-user", Password: "Expire#Passw0rd",
	}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("locked login error = %v, want unauthorized", err)
	}
	f.clock.Advance(app.DefaultLockoutDuration + time.Minute)
	if _, err := f.svc.Login(ctx, app.LoginInput{
		Subject: "expire-user", Password: "Expire#Passw0rd",
	}); err != nil {
		t.Fatalf("login after lock expiry: %v", err)
	}
}

// TestCompletePasswordChangeFlow 覆盖管理员重置后的强制改密闭环。
func TestCompletePasswordChangeFlow(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-force", "force-user", "Force#Passw0rd")
	login := f.login(t, "force-user", "Force#Passw0rd")
	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: login.AccountID, TraceID: "trace-force"}
	temporary, err := svc.ResetPassword(ctx, app.AccountOperationInput{
		Actor: actor, TargetAccountID: login.AccountID,
	})
	if err != nil {
		t.Fatalf("reset password: %v", err)
	}
	challenge := f.login(t, "force-user", temporary)
	if !challenge.PasswordChangeRequired || challenge.PasswordChangeTicket == "" {
		t.Fatalf("expected password change challenge, got %+v", challenge)
	}
	if _, err := f.svc.CompletePasswordChange(ctx, app.CompletePasswordChangeInput{
		Ticket: challenge.PasswordChangeTicket, NewPassword: "Forced#Passw0rd2", TraceID: "trace-force-done",
	}); err != nil {
		t.Fatalf("complete password change: %v", err)
	}
	// 票据一次性：重复使用必须失败。
	if _, err := f.svc.CompletePasswordChange(ctx, app.CompletePasswordChangeInput{
		Ticket: challenge.PasswordChangeTicket, NewPassword: "Forced#Passw0rd2",
	}); err == nil {
		t.Fatal("password change ticket must be single-use")
	}
	// 空票据与损坏票据统一 40100。
	requireAppCode(t, func() error {
		_, err := f.svc.CompletePasswordChange(ctx, app.CompletePasswordChangeInput{Ticket: "", NewPassword: "Forced#Passw0rd2"})
		return err
	}(), apperr.CodeUnauthorized)
	requireAppCode(t, func() error {
		_, err := f.svc.CompletePasswordChange(ctx, app.CompletePasswordChangeInput{
			Ticket: challenge.PasswordChangeTicket + "x", NewPassword: "Forced#Passw0rd2",
		})
		return err
	}(), apperr.CodeUnauthorized)
}

// TestAccountServiceRejectsInvalidOperations 覆盖管理员账号操作的参数 / 状态错误分支。
func TestAccountServiceRejectsInvalidOperations(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-target", "target", "Target#Passw0rd")
	f.login(t, "target", "Target#Passw0rd")
	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	validActor := app.SessionActor{AccountID: "acct-target", TraceID: "trace-ops"}
	noActor := app.SessionActor{AccountID: "", TraceID: "trace-ops"}
	unknown := app.AccountOperationInput{Actor: validActor, TargetAccountID: "missing"}

	// 目标不存在 / 重复状态 / 非法锁定时长。
	requireAppCode(t, svc.Disable(ctx, unknown), apperr.CodeNotFound)
	// 列表参数非法与分页边界。
	_, _, listErr := svc.List(ctx, app.AccountListInput{Actor: noActor})
	requireAppCode(t, listErr, apperr.CodeUnauthorized)
	_, _, listErr = svc.List(ctx, app.AccountListInput{Actor: validActor, Status: "unknown", Limit: 1})
	requireAppCode(t, listErr, apperr.CodeBadRequest)
	_, _, listErr = svc.List(ctx, app.AccountListInput{
		Actor: validActor, SubjectExact: strings.Repeat("x", 256),
	})
	requireAppCode(t, listErr, apperr.CodeBadRequest)
	// 详情 / 会话参数非法。
	_, detailErr := svc.Get(ctx, validActor, "")
	requireAppCode(t, detailErr, apperr.CodeBadRequest)
	_, pageErr := svc.ListTargetSessions(ctx, validActor, "", "", 0)
	requireAppCode(t, pageErr, apperr.CodeBadRequest)
}

// TestAccountServiceTenantPasswordPolicy 验证 B 端改密按商户策略收紧（BIZ-04）。
func TestAccountServiceTenantPasswordPolicy(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelBiz, func(cfg *app.AuthServiceConfig) {
		cfg.PasswordPolicyFor = func(merchantID string) app.PasswordPolicy {
			if merchantID == "m-1" {
				return app.PasswordPolicy{MinLength: 16, MaxLength: 64, RequireComplexity: true}
			}
			return app.PasswordPolicy{}
		}
	})
	f.seedAccount(t, "acct-tenant", "tenant-user", "Tenant#Passw0rd")
	login := f.login(t, "tenant-user", "Tenant#Passw0rd")
	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: login.AccountID, SessionID: login.SessionID, TraceID: "trace-tenant"}
	// 14 字符满足全局策略，但低于租户 16 位下限。
	requireAppCode(t, svc.ChangePassword(ctx, app.ChangePasswordInput{
		Actor: actor, OldPassword: "Tenant#Passw0rd", NewPassword: "Aa1#shortpassw",
	}), apperr.CodeBadRequest)
	if err := svc.ChangePassword(ctx, app.ChangePasswordInput{
		Actor: actor, OldPassword: "Tenant#Passw0rd", NewPassword: "Aa1#longerpassw0rd",
	}); err != nil {
		t.Fatalf("change password with tenant policy: %v", err)
	}
}

// TestAccountServiceStepUpReusesLoginStep 验证登录后立即 step-up 可复用刚消费的时间步（BIZ-08）。
func TestAccountServiceStepUpReusesLoginStep(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.MFAAdminRequired = true
	})
	f.seedAccount(t, "acct-op", "operator", "Operator#Pass12")
	f.seedAccount(t, "acct-target", "target", "Target#Pass12")
	secret, _ := bindMFA(t, f, "acct-op")
	bindMFA(t, f, "acct-target")

	// 登录会消费当前时间步；立即用同一个动态码执行特权操作必须成功（不强制等 30s）。
	challenge := f.login(t, "operator", "Operator#Pass12")
	if !challenge.MFARequired {
		t.Fatalf("expected MFA challenge, got %+v", challenge)
	}
	code := totpCodeAt(t, secret, f.clock.Now())
	if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
		Ticket: challenge.MFATicket, Code: code, TraceID: "trace-login-step",
	}); err != nil {
		t.Fatalf("verify login step: %v", err)
	}

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	if err := svc.Lock(ctx, app.LockAccountInput{
		AccountOperationInput: app.AccountOperationInput{
			Actor:           app.SessionActor{AccountID: "acct-op", TraceID: "trace-stepup-reuse"},
			TargetAccountID: "acct-target",
			MFACode:         code,
		},
		Duration: time.Minute,
	}); err != nil {
		t.Fatalf("step-up with reused login step: %v", err)
	}
	// 超出默认宽限（2m）后同一时间步必须按重放拒绝。
	f.clock.Advance(2*time.Minute + totp.Period)
	if err := svc.Unlock(ctx, app.AccountOperationInput{
		Actor:           app.SessionActor{AccountID: "acct-op", TraceID: "trace-stepup-expired"},
		TargetAccountID: "acct-target",
		MFACode:         code,
	}); err == nil {
		t.Fatal("expired step-up reuse must be rejected")
	}
}

// TestAccountServiceStepUpCoversLifecycleAndSessions 验证启用、锁定、解锁、恢复与
// 单会话撤销均受 step-up 保护（IDN-GAP-SEC-02）：缺少 mfa_code 返回 40000。
func TestAccountServiceStepUpCoversLifecycleAndSessions(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.MFAAdminRequired = true
	})
	f.seedAccount(t, "acct-operator", "operator", "Operator#Pass12")
	f.seedAccount(t, "acct-target", "target", "Target#Pass12")
	secret, _ := bindMFA(t, f, "acct-operator")
	bindMFA(t, f, "acct-target")
	login := loginWithMFA(t, f, "target", "Target#Pass12")

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: "acct-operator", TraceID: "trace-stepup-lifecycle"}
	// 每次成功 step-up 会消费当前时间步，生成后立即推进时钟避免重放。
	nextCode := func() string {
		code := totpCodeAt(t, secret, f.clock.Now())
		f.clock.Advance(totp.Period)
		return code
	}
	base := app.AccountOperationInput{Actor: actor, TargetAccountID: login.AccountID}
	withCode := func() app.AccountOperationInput {
		in := base
		in.MFACode = nextCode()
		return in
	}

	if err := svc.Disable(ctx, base); err != nil {
		t.Fatalf("disable: %v", err)
	}
	requireAppCode(t, svc.Enable(ctx, base), apperr.CodeBadRequest)
	if err := svc.Enable(ctx, withCode()); err != nil {
		t.Fatalf("enable with step-up: %v", err)
	}

	requireAppCode(t, svc.Lock(ctx, app.LockAccountInput{AccountOperationInput: base}), apperr.CodeBadRequest)
	if err := svc.Lock(ctx, app.LockAccountInput{AccountOperationInput: withCode()}); err != nil {
		t.Fatalf("lock with step-up: %v", err)
	}
	requireAppCode(t, svc.Unlock(ctx, base), apperr.CodeBadRequest)
	if err := svc.Unlock(ctx, withCode()); err != nil {
		t.Fatalf("unlock with step-up: %v", err)
	}

	if err := svc.Delete(ctx, withCode()); err != nil {
		t.Fatalf("delete with step-up: %v", err)
	}
	requireAppCode(t, svc.Restore(ctx, base), apperr.CodeBadRequest)
	if err := svc.Restore(ctx, withCode()); err != nil {
		t.Fatalf("restore with step-up: %v", err)
	}

	loginWithMFA(t, f, "target", "Target#Pass12")
	page, err := svc.ListTargetSessions(ctx, actor, login.AccountID, "", 0)
	if err != nil {
		t.Fatalf("list target sessions: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatal("expected a live target session")
	}
	familyID := page.Items[0].ID
	requireAppCode(t, svc.RevokeTargetSession(ctx, base, familyID), apperr.CodeBadRequest)
	if err := svc.RevokeTargetSession(ctx, withCode(), familyID); err != nil {
		t.Fatalf("revoke target session with step-up: %v", err)
	}
}

// TestAccountServiceDisableEnableUnlock 验证管理员停用、启用与解锁。
func TestAccountServiceDisableEnableUnlock(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
	f.seedAccount(t, "acct-life", "bob", "Correct#Passw0rd")
	login := f.login(t, "bob", "Correct#Passw0rd")

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: "acct-admin", TraceID: "trace-life"}

	if err := svc.Disable(ctx, app.AccountOperationInput{
		Actor: actor, TargetAccountID: login.AccountID, Reason: "left_company",
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	account, err := f.accounts.FindByID(ctx, login.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Status != domain.AccountStatusDisabled {
		t.Fatalf("status = %s, want disabled", account.Status)
	}
	if session := f.sessionOf(t, login.SessionID); session.Status != domain.SessionStatusRevoked {
		t.Fatalf("session must be revoked on disable: %+v", session)
	}
	if len(capture.byAction(audit.ActionAuthAccountDisabled)) != 1 {
		t.Fatalf("expected auth.account.disabled, got %+v", capture.snapshot())
	}
	_, err = f.svc.Login(ctx, app.LoginInput{Subject: "bob", Password: "Correct#Passw0rd"})
	requireAppCode(t, err, apperr.CodeUnauthorized)

	if err := svc.Enable(ctx, app.AccountOperationInput{Actor: actor, TargetAccountID: login.AccountID}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	account, err = f.accounts.FindByID(ctx, login.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Status != domain.AccountStatusActive {
		t.Fatalf("status = %s, want active", account.Status)
	}
	if _, err := f.svc.Login(ctx, app.LoginInput{Subject: "bob", Password: "Correct#Passw0rd"}); err != nil {
		t.Fatalf("login after enable: %v", err)
	}

	// 触发锁定后由管理员解锁。
	for i := 0; i < app.DefaultLockoutMaxFailures; i++ {
		if _, err := f.svc.Login(ctx, app.LoginInput{Subject: "bob", Password: "wrong"}); !errors.Is(err, apperr.ErrUnauthorized) {
			t.Fatalf("attempt %d: expected 401, got %v", i+1, err)
		}
	}
	account, err = f.accounts.FindByID(ctx, login.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Status != domain.AccountStatusLocked {
		t.Fatalf("status = %s, want locked", account.Status)
	}
	if err := svc.Unlock(ctx, app.AccountOperationInput{Actor: actor, TargetAccountID: login.AccountID}); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	account, err = f.accounts.FindByID(ctx, login.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Status != domain.AccountStatusActive {
		t.Fatalf("status = %s, want active after unlock", account.Status)
	}
	if len(capture.byAction(audit.ActionAuthAccountUnlocked)) != 1 {
		t.Fatalf("expected auth.account.unlocked, got %+v", capture.snapshot())
	}
}

// TestAccountServiceListAndGet 验证账号列表分页、筛选、详情与读取审计。
func TestAccountServiceListAndGet(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
	f.seedAccount(t, "acct-list-1", "alice", "Correct#Passw0rd")
	f.seedAccount(t, "acct-list-2", "bob", "Correct#Passw0rd")
	f.seedAccount(t, "acct-list-3", "carol", "Correct#Passw0rd")

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: "acct-admin", TraceID: "trace-list"}
	items, total, err := svc.List(ctx, app.AccountListInput{Actor: actor, Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(items) != 2 {
		t.Fatalf("total=%d items=%d, want 3/2", total, len(items))
	}
	filtered, total, err := svc.List(ctx, app.AccountListInput{Actor: actor, Keyword: "ali"})
	if err != nil {
		t.Fatalf("list keyword: %v", err)
	}
	if total != 1 || len(filtered) != 1 || filtered[0].Subject != "alice" {
		t.Fatalf("keyword filter = %+v total=%d, want alice only", filtered, total)
	}
	// PERF-07：精确主体优先于关键词，且大小写 / 空白归一化。
	exact, total, err := svc.List(ctx, app.AccountListInput{
		Actor: actor, SubjectExact: "  ALICE ", Keyword: "bob",
	})
	if err != nil {
		t.Fatalf("list subject exact: %v", err)
	}
	if total != 1 || len(exact) != 1 || exact[0].Subject != "alice" {
		t.Fatalf("subject exact = %+v total=%d, want alice only", exact, total)
	}
	// 列表读取必须留痕，且只标记关键词存在，不记录主体原文（SEC-08）。
	listEvents := capture.byAction(audit.ActionAuthAccountRead)
	if len(listEvents) != 3 {
		t.Fatalf("list read audits = %d, want 3: %+v", len(listEvents), capture.snapshot())
	}
	if listEvents[1].Attrs["scope"] != "list" || listEvents[1].Attrs["keyword_present"] != "true" {
		t.Fatalf("list audit attrs = %+v", listEvents[1].Attrs)
	}
	if listEvents[2].Attrs["subject_exact_present"] != "true" {
		t.Fatalf("exact list audit attrs = %+v", listEvents[2].Attrs)
	}

	detail, err := svc.Get(ctx, app.SessionActor{AccountID: "acct-admin", TraceID: "trace-read"}, "acct-list-1")
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if !detail.HasPassword || detail.PhoneBound || detail.MFAEnabled || detail.MustChangePassword {
		t.Fatalf("unexpected detail: %+v", detail)
	}
}

// TestAccountServiceLockAndUnlock 验证管理员主动锁定与解锁。
func TestAccountServiceLockAndUnlock(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-lock", "dave", "Correct#Passw0rd")
	login := f.login(t, "dave", "Correct#Passw0rd")

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: "acct-admin", TraceID: "trace-lock"}
	if err := svc.Lock(ctx, app.LockAccountInput{
		AccountOperationInput: app.AccountOperationInput{
			Actor: actor, TargetAccountID: login.AccountID, Reason: "security_review",
		},
		Duration: 30 * time.Minute,
	}); err != nil {
		t.Fatalf("lock: %v", err)
	}
	account, err := f.accounts.FindByID(ctx, login.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Status != domain.AccountStatusLocked || account.TokenVersion != 2 {
		t.Fatalf("account = status %s token_version %d, want locked/2", account.Status, account.TokenVersion)
	}
	if session := f.sessionOf(t, login.SessionID); session.Status != domain.SessionStatusRevoked {
		t.Fatalf("sessions must be revoked on lock: %+v", session)
	}
	if _, err := f.svc.Login(ctx, app.LoginInput{Subject: "dave", Password: "Correct#Passw0rd"}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("locked login must be rejected, got %v", err)
	}
	if err := svc.Unlock(ctx, app.AccountOperationInput{Actor: actor, TargetAccountID: login.AccountID}); err != nil {
		t.Fatalf("unlock: %v", err)
	}
}

// TestAccountServiceDeleteAndRestore 验证软删与恢复。
func TestAccountServiceDeleteAndRestore(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-delete", "erin", "Correct#Passw0rd")
	login := f.login(t, "erin", "Correct#Passw0rd")

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: "acct-admin", TraceID: "trace-delete"}
	if err := svc.Delete(ctx, app.AccountOperationInput{Actor: actor, TargetAccountID: login.AccountID}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	account, err := f.accounts.FindByID(ctx, login.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.DeletedAt == nil || account.Status != domain.AccountStatusDisabled {
		t.Fatalf("account must be soft deleted: %+v", account)
	}
	if _, err := f.svc.Login(ctx, app.LoginInput{Subject: "erin", Password: "Correct#Passw0rd"}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("deleted login must be rejected, got %v", err)
	}
	detail, err := svc.Get(ctx, app.SessionActor{AccountID: login.AccountID, TraceID: "trace-read"}, login.AccountID)
	if err != nil {
		t.Fatalf("get deleted account: %v", err)
	}
	if detail.DeletedAt == nil {
		t.Fatalf("detail must expose deleted_at: %+v", detail)
	}
	if err := svc.Restore(ctx, app.AccountOperationInput{Actor: actor, TargetAccountID: login.AccountID}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	account, err = f.accounts.FindByID(ctx, login.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.DeletedAt != nil || account.Status != domain.AccountStatusActive {
		t.Fatalf("account must be restored: %+v", account)
	}
	if _, err := f.svc.Login(ctx, app.LoginInput{Subject: "erin", Password: "Correct#Passw0rd"}); err != nil {
		t.Fatalf("login after restore: %v", err)
	}
}

// TestAccountServiceTargetSessions 验证目标账号会话列表与撤销。
func TestAccountServiceTargetSessions(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-sess", "frank", "Correct#Passw0rd")
	first := f.login(t, "frank", "Correct#Passw0rd")
	second := f.login(t, "frank", "Correct#Passw0rd")

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	page, err := svc.ListTargetSessions(ctx, app.SessionActor{AccountID: "acct-admin", TraceID: "trace-read"}, first.AccountID, "", 0)
	if err != nil {
		t.Fatalf("list target sessions: %v", err)
	}
	views := page.Items
	if len(views) != 2 {
		t.Fatalf("sessions = %d, want 2", len(views))
	}
	actor := app.SessionActor{AccountID: "acct-admin", TraceID: "trace-sess"}
	if err := svc.RevokeTargetSession(ctx, app.AccountOperationInput{
		Actor: actor, TargetAccountID: first.AccountID,
	}, first.SessionID); err != nil {
		t.Fatalf("revoke target session: %v", err)
	}
	if session := f.sessionOf(t, first.SessionID); session.Status != domain.SessionStatusRevoked {
		t.Fatalf("target session must be revoked: %+v", session)
	}
	if err := svc.RevokeTargetSessions(ctx, app.AccountOperationInput{
		Actor: actor, TargetAccountID: first.AccountID,
	}); err != nil {
		t.Fatalf("revoke target sessions: %v", err)
	}
	if session := f.sessionOf(t, second.SessionID); session.Status != domain.SessionStatusRevoked {
		t.Fatalf("remaining session must be revoked: %+v", session)
	}
	account, err := f.accounts.FindByID(ctx, first.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.TokenVersion != 2 {
		t.Fatalf("token_version = %d, want 2", account.TokenVersion)
	}
}

// TestAccountServiceResetPasswordFlow 验证临时口令、强制改密与票据完成。
func TestAccountServiceResetPasswordFlow(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-reset", "grace", "Old#Password12")
	login := f.login(t, "grace", "Old#Password12")

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: "acct-admin", TraceID: "trace-reset"}
	temporary, err := svc.ResetPassword(ctx, app.AccountOperationInput{
		Actor: actor, TargetAccountID: login.AccountID, Reason: "lost_password",
	})
	if err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if len(temporary) != 20 {
		t.Fatalf("temporary password length = %d, want 20", len(temporary))
	}
	if session := f.sessionOf(t, login.SessionID); session.Status != domain.SessionStatusRevoked {
		t.Fatalf("sessions must be revoked on password reset: %+v", session)
	}

	challenge, err := f.svc.Login(ctx, app.LoginInput{Subject: "grace", Password: temporary})
	if err != nil {
		t.Fatalf("login with temporary password: %v", err)
	}
	if !challenge.PasswordChangeRequired || challenge.PasswordChangeTicket == "" {
		t.Fatalf("expected password_change_required, got %+v", challenge)
	}
	if challenge.AccessToken != "" || challenge.RefreshToken != "" {
		t.Fatal("temporary password login must not issue tokens")
	}

	completed, err := f.svc.CompletePasswordChange(ctx, app.CompletePasswordChangeInput{
		Ticket: challenge.PasswordChangeTicket, NewPassword: "New#Password12", TraceID: "trace-complete",
	})
	if err != nil {
		t.Fatalf("complete password change: %v", err)
	}
	if completed.AccessToken == "" || completed.RefreshToken == "" {
		t.Fatalf("expected tokens after password change, got %+v", completed)
	}
	account, err := f.accounts.FindByID(ctx, login.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.MustChangePassword || account.TokenVersion != 3 {
		t.Fatalf("account = must_change %v token_version %d, want false/3", account.MustChangePassword, account.TokenVersion)
	}
	if _, err := f.svc.Login(ctx, app.LoginInput{Subject: "grace", Password: "New#Password12"}); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	if _, err := f.svc.CompletePasswordChange(ctx, app.CompletePasswordChangeInput{
		Ticket: challenge.PasswordChangeTicket, NewPassword: "Another#Pass12", TraceID: "trace-replay",
	}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("ticket replay must be rejected, got %v", err)
	}
}

// TestAccountServiceResetMFA 验证管理员重置 MFA：删除绑定、撤销会话与审计。
func TestAccountServiceResetMFA(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
	f.seedAccount(t, "acct-mfa-reset", "carol", "Correct#Passw0rd")
	login := f.login(t, "carol", "Correct#Passw0rd")
	bindMFA(t, f, login.AccountID)

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	actor := app.SessionActor{AccountID: "acct-admin", TraceID: "trace-mfa-reset"}
	if err := svc.ResetMFA(ctx, app.AccountOperationInput{
		Actor: actor, TargetAccountID: login.AccountID, Reason: "lost_device",
	}); err != nil {
		t.Fatalf("reset mfa: %v", err)
	}
	if _, err := f.mfa.FindByAccount(ctx, login.AccountID); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("mfa enrollment must be deleted, got %v", err)
	}
	account, err := f.accounts.FindByID(ctx, login.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.TokenVersion != 2 {
		t.Fatalf("token_version = %d, want 2", account.TokenVersion)
	}
	if len(capture.byAction(audit.ActionAuthMFAReset)) != 1 {
		t.Fatalf("expected auth.mfa.reset, got %+v", capture.snapshot())
	}
	// 重置后可凭密码重新登录并重新绑定。
	if _, err := f.svc.Login(ctx, app.LoginInput{Subject: "carol", Password: "Correct#Passw0rd"}); err != nil {
		t.Fatalf("login after mfa reset: %v", err)
	}
}
