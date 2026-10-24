package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// captureEmitter 在内存中收集审计事件，供 AC-ENT-01-5 断言。
type captureEmitter struct {
	mu     sync.Mutex
	events []audit.Event
}

// Emit 记录事件；不模拟投递失败。
func (c *captureEmitter) Emit(_ context.Context, event audit.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	return nil
}

// snapshot 返回事件副本。
func (c *captureEmitter) snapshot() []audit.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.Event, len(c.events))
	copy(out, c.events)
	return out
}

// byAction 过滤指定 action 的事件。
func (c *captureEmitter) byAction(action string) []audit.Event {
	var out []audit.Event
	for _, event := range c.snapshot() {
		if event.Action == action {
			out = append(out, event)
		}
	}
	return out
}

// dump 把所有事件序列化为 JSON，用于敏感字段断言。
func (c *captureEmitter) dump(t *testing.T) string {
	t.Helper()
	payload, err := json.Marshal(c.snapshot())
	if err != nil {
		t.Fatalf("marshal audit events: %v", err)
	}
	return string(payload)
}

// withCapture 把捕获器注入认证服务配置。
func withCapture(c *captureEmitter) withAuthConfig {
	return func(cfg *app.AuthServiceConfig) {
		cfg.Audit = c
	}
}

// TestAuditLoginSuccessAndFailure 覆盖登录成功与密码错误的审计事件。
func TestAuditLoginSuccessAndFailure(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
	f.seedAccount(t, "acc-audit-1", "user@example.com", "Sup3rSecret!Passw0rd")

	if _, err := f.svc.Login(ctx, app.LoginInput{
		Subject: "user@example.com", Password: "Sup3rSecret!Passw0rd", TraceID: "trace-ok",
	}); err != nil {
		t.Fatalf("login: %v", err)
	}
	logins := capture.byAction(audit.ActionAuthLogin)
	if len(logins) != 1 || logins[0].Outcome != audit.OutcomeSuccess {
		t.Fatalf("expected one successful auth.login, got %+v", logins)
	}

	if _, err := f.svc.Login(ctx, app.LoginInput{
		Subject: "user@example.com", Password: "wrong-password", TraceID: "trace-bad",
	}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("expected 401 on wrong password, got %v", err)
	}
	failed := capture.byAction(audit.ActionAuthLoginFailed)
	if len(failed) != 1 {
		t.Fatalf("expected one auth.login_failed, got %+v", failed)
	}
	if failed[0].Outcome != audit.OutcomeFailure || failed[0].Reason != domain.LoginReasonBadCredential {
		t.Fatalf("unexpected login_failed event: %+v", failed[0])
	}
	if failed[0].ActorType != "staff" || failed[0].Channel != string(domain.ChannelAdmin) {
		t.Fatalf("unexpected actor/channel: %+v", failed[0])
	}
}

// TestAuditAccountLockedOnThreshold 覆盖连续失败触发锁定时的锁定事件。
func TestAuditAccountLockedOnThreshold(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
	f.seedAccount(t, "acc-audit-lock", "admin@example.com", "Sup3rSecret!Passw0rd")
	f.login(t, "admin@example.com", "Sup3rSecret!Passw0rd")

	for i := 0; i < app.DefaultLockoutMaxFailures; i++ {
		if _, err := f.svc.Login(ctx, app.LoginInput{
			Subject: "admin@example.com", Password: "wrong-password", TraceID: "trace-lock",
		}); !errors.Is(err, apperr.ErrUnauthorized) {
			t.Fatalf("attempt %d: expected 401, got %v", i+1, err)
		}
	}
	if got := len(capture.byAction(audit.ActionAuthLoginFailed)); got != app.DefaultLockoutMaxFailures {
		t.Fatalf("expected %d auth.login_failed, got %d", app.DefaultLockoutMaxFailures, got)
	}
	locked := capture.byAction(audit.ActionAuthAccountLocked)
	if len(locked) != 1 {
		t.Fatalf("expected one auth.account.locked, got %+v", locked)
	}
	if locked[0].Outcome != audit.OutcomeFailure || locked[0].Reason != domain.LoginReasonLocked {
		t.Fatalf("unexpected locked event: %+v", locked[0])
	}
}

// TestAuditSessionLifecycle 覆盖登出、刷新成功与刷新令牌重放。
func TestAuditSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelC, withCapture(capture))
	f.seedAccount(t, "acc-audit-session", "+8613800138000", "Sup3rSecret!Passw0rd")
	login := f.login(t, "+8613800138000", "Sup3rSecret!Passw0rd")

	pair, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken, TraceID: "trace-refresh"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(capture.byAction(audit.ActionAuthTokenRefresh)) != 1 {
		t.Fatalf("expected auth.token.refresh, got %+v", capture.snapshot())
	}

	if err := f.svc.Logout(ctx, app.SessionActor{
		AccountID: login.AccountID, SessionID: sessionIDForRefresh(t, f, pair), TraceID: "trace-logout",
	}); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if len(capture.byAction(audit.ActionAuthLogout)) != 1 {
		t.Fatalf("expected auth.logout, got %+v", capture.snapshot())
	}

	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken, TraceID: "trace-replay"}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("expected 401 on refresh replay, got %v", err)
	}
	reused := capture.byAction(audit.ActionAuthTokenReused)
	if len(reused) != 1 || reused[0].Reason != domain.LoginReasonReplay {
		t.Fatalf("expected auth.token.reused, got %+v", reused)
	}
}

// sessionIDForRefresh 按刷新令牌找到对应会话主键。
func sessionIDForRefresh(t *testing.T, f *authFixture, pair app.TokenPair) string {
	t.Helper()
	hash, err := app.HashRefreshToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("hash refresh token: %v", err)
	}
	session, err := f.sessions.FindByRefreshHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("load refreshed session: %v", err)
	}
	return session.ID
}

// TestAuditMFAEvents 覆盖绑定、失败与解绑事件。
func TestAuditMFAEvents(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
	f.seedAccount(t, "acc-audit-mfa", "admin-mfa@example.com", "Sup3rSecret!Passw0rd")
	f.login(t, "admin-mfa@example.com", "Sup3rSecret!Passw0rd")

	bindMFA(t, f, "acc-audit-mfa")
	if len(capture.byAction(audit.ActionAuthMFABound)) != 1 {
		t.Fatalf("expected auth.mfa.bound, got %+v", capture.snapshot())
	}

	challenge := f.login(t, "admin-mfa@example.com", "Sup3rSecret!Passw0rd")
	if !challenge.MFARequired {
		t.Fatalf("expected mfa challenge, got %+v", challenge)
	}
	secret := f.mustSecret(t, "acc-audit-mfa")
	if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
		Ticket: challenge.MFATicket, Code: wrongTOTPCode(t, secret, f.clock.Now()), TraceID: "trace-mfa-bad",
	}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("expected 401 on wrong mfa code, got %v", err)
	}
	if len(capture.byAction(audit.ActionAuthMFAFailed)) != 1 {
		t.Fatalf("expected auth.mfa.failed, got %+v", capture.snapshot())
	}

	if err := f.svc.DisableMFA(ctx, app.SessionActor{
		AccountID: "acc-audit-mfa", TraceID: "trace-mfa-disable",
	}, totpCodeAt(t, secret, f.clock.Now())); err != nil {
		t.Fatalf("disable mfa: %v", err)
	}
	if len(capture.byAction(audit.ActionAuthMFADisabled)) != 1 {
		t.Fatalf("expected auth.mfa.disabled, got %+v", capture.snapshot())
	}
}

// wrongTOTPCode 生成一个与当前动态码不同的 6 位码。
func wrongTOTPCode(t *testing.T, secret string, now time.Time) string {
	t.Helper()
	if totpCodeAt(t, secret, now) == "000000" {
		return "000001"
	}
	return "000000"
}

// TestAuditEventsExcludeSecrets 断言审计事件不含任何明文凭据。
func TestAuditEventsExcludeSecrets(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
	const plainPassword = "Sup3rSecret!Passw0rd"
	f.seedAccount(t, "acc-audit-secret", "+8613900139000", plainPassword)

	login := f.login(t, "+8613900139000", plainPassword)
	secret, recoveryCodes := bindMFA(t, f, "acc-audit-secret")
	pair, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken, TraceID: "trace-secret"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken, TraceID: "trace-secret"}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("expected reuse rejection, got %v", err)
	}
	_ = pair
	if err := f.svc.DisableMFA(ctx, app.SessionActor{
		AccountID: "acc-audit-secret", TraceID: "trace-secret",
	}, totpCodeAt(t, secret, f.clock.Now())); err != nil {
		t.Fatalf("disable mfa: %v", err)
	}

	dump := capture.dump(t)
	forbidden := []string{
		plainPassword,
		login.RefreshToken,
		pair.RefreshToken,
		secret,
		recoveryCodes[0],
		"+8613900139000",
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(dump, value) {
			t.Fatalf("audit events leaked sensitive value %q: %s", value, dump)
		}
	}
}

// TestKnownActionsIncludeAccountEvents 断言新增账号事件已登记在目录中。
func TestKnownActionsIncludeAccountEvents(t *testing.T) {
	known := make(map[string]struct{}, len(audit.KnownActions()))
	for _, action := range audit.KnownActions() {
		known[action] = struct{}{}
	}
	for _, action := range []string{audit.ActionAuthAccountLocked, audit.ActionAuthAccountDisabled} {
		if _, ok := known[action]; !ok {
			t.Fatalf("action %q missing from KnownActions", action)
		}
	}
}
