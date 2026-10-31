package app_test

import (
	"context"
	"errors"
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
