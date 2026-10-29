package app_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238 规定 TOTP 使用 HMAC-SHA1，测试需自行生成参考码
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/totp"
)

// mfaTestEncoding 与 pkg/totp 一致的无填充 Base32 编码。
var mfaTestEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// totpCodeAt 按 RFC 6238 生成测试时刻的 6 位动态码。
func totpCodeAt(t *testing.T, secretBase32 string, now time.Time) string {
	t.Helper()
	secret, err := mfaTestEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secretBase32)))
	if err != nil {
		t.Fatalf("decode totp secret: %v", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/int64(totp.Period/time.Second))) //nolint:gosec // G115: 测试时间戳固定为正，不存在溢出

	mac := hmac.New(sha1.New, secret)
	if _, err := mac.Write(counter[:]); err != nil {
		t.Fatalf("hmac write: %v", err)
	}
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

// bindMFA 完成绑定初始化与首次确认，返回密钥与恢复码。
func bindMFA(t *testing.T, f *authFixture, accountID string) (secret string, recoveryCodes []string) {
	t.Helper()
	ctx := context.Background()
	setup, err := f.svc.SetupMFA(ctx, app.MFASetupInput{Actor: app.SessionActor{AccountID: accountID, TraceID: "trace-setup"}})
	if err != nil {
		t.Fatalf("setup mfa: %v", err)
	}
	code := totpCodeAt(t, setup.Secret, f.clock.Now())
	result, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
		Code:    code,
		Actor:   app.SessionActor{AccountID: accountID, TraceID: "trace-confirm"},
		TraceID: "trace-confirm",
	})
	if err != nil {
		t.Fatalf("confirm mfa binding: %v", err)
	}
	if !result.BindingConfirmed {
		t.Fatalf("expected binding confirmed, got %+v", result)
	}
	// 首码已被消费（防重放），后续登录必须使用下一个时间步。
	f.clock.Advance(totp.Period)
	return setup.Secret, setup.RecoveryCodes
}

// loginWithMFA 完成一次两步登录并返回令牌。
func loginWithMFA(t *testing.T, f *authFixture, subject, password string) app.MFAVerifyResult {
	t.Helper()
	challenge := f.login(t, subject, password)
	if !challenge.MFARequired {
		t.Fatalf("expected MFA challenge, got %+v", challenge)
	}
	result, err := f.svc.VerifyMFA(context.Background(), app.MFAVerifyInput{
		Ticket:  challenge.MFATicket,
		Code:    totpCodeAt(t, f.mustSecret(t, challenge.AccountID), f.clock.Now()),
		TraceID: "trace-verify",
	})
	if err != nil {
		t.Fatalf("verify mfa: %v", err)
	}
	return result
}

// mustSecret 读取并解密仓储中的 TOTP 密钥（测试专用）。
func (f *authFixture) mustSecret(t *testing.T, accountID string) string {
	t.Helper()
	enrollment, err := f.mfa.FindByAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("load mfa enrollment: %v", err)
	}
	plain, err := f.mfaCipher.Open(enrollment.SecretCiphertext, []byte("identity:mfa:"+accountID))
	if err != nil {
		t.Fatalf("decrypt mfa secret: %v", err)
	}
	return totp.EncodeSecret(plain)
}

// mustListSessions 读取账号会话（测试专用），用于断言失败路径无副作用。
func mustListSessions(t *testing.T, f *authFixture, accountID string) []*domain.Session {
	t.Helper()
	sessions, err := f.sessions.ListByAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return sessions
}

func TestMFASetupAndBindingConfirmation(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-mfa", "admin@example.com", "Correct#Passw0rd")
	f.login(t, "admin@example.com", "Correct#Passw0rd")

	setup, err := f.svc.SetupMFA(ctx, app.MFASetupInput{Actor: app.SessionActor{AccountID: "acct-mfa", TraceID: "trace-setup"}})
	if err != nil {
		t.Fatalf("setup mfa: %v", err)
	}
	if setup.Secret == "" || !strings.HasPrefix(setup.OTPAuthURI, "otpauth://totp/") {
		t.Fatalf("unexpected setup result: %+v", setup)
	}
	if len(setup.RecoveryCodes) != 10 {
		t.Fatalf("recovery codes = %d, want 10", len(setup.RecoveryCodes))
	}

	t.Run("wrong code keeps binding unconfirmed", func(t *testing.T) {
		_, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Code:    "000000",
			Actor:   app.SessionActor{AccountID: "acct-mfa"},
			TraceID: "trace-confirm-failed",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
		enrollment, loadErr := f.mfa.FindByAccount(ctx, "acct-mfa")
		if loadErr != nil {
			t.Fatalf("load enrollment: %v", loadErr)
		}
		if enrollment.IsConfirmed() {
			t.Fatal("binding must stay unconfirmed after wrong code")
		}
	})

	t.Run("correct code confirms binding", func(t *testing.T) {
		result, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Code:    totpCodeAt(t, setup.Secret, f.clock.Now()),
			Actor:   app.SessionActor{AccountID: "acct-mfa"},
			TraceID: "trace-confirm",
		})
		if err != nil {
			t.Fatalf("confirm binding: %v", err)
		}
		if !result.BindingConfirmed || result.AccessToken != "" {
			t.Fatalf("unexpected confirm result: %+v", result)
		}
	})

	t.Run("second setup returns conflict", func(t *testing.T) {
		_, err := f.svc.SetupMFA(ctx, app.MFASetupInput{Actor: app.SessionActor{AccountID: "acct-mfa"}})
		requireAppCode(t, err, apperr.CodeConflict)
	})
}

func TestMFATwoStepLogin(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-login", "admin@example.com", "Correct#Passw0rd")
	secret, recoveryCodes := bindMFA(t, f, "acct-login")
	_ = secret

	challenge := f.login(t, "admin@example.com", "Correct#Passw0rd")
	if !challenge.MFARequired {
		t.Fatalf("expected MFA challenge, got %+v", challenge)
	}
	if challenge.AccessToken != "" || challenge.RefreshToken != "" {
		t.Fatal("challenge response must not contain tokens")
	}
	if challenge.MFATicket == "" {
		t.Fatal("challenge response must contain mfa ticket")
	}

	t.Run("wrong code burns ticket", func(t *testing.T) {
		_, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Ticket:  challenge.MFATicket,
			Code:    "000000",
			TraceID: "trace-bad-code",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
		_, err = f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Ticket:  challenge.MFATicket,
			Code:    totpCodeAt(t, f.mustSecret(t, "acct-login"), f.clock.Now()),
			TraceID: "trace-replay",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
	})

	t.Run("correct code issues tokens", func(t *testing.T) {
		next := f.login(t, "admin@example.com", "Correct#Passw0rd")
		result, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Ticket:  next.MFATicket,
			Code:    totpCodeAt(t, f.mustSecret(t, "acct-login"), f.clock.Now()),
			TraceID: "trace-verify",
		})
		if err != nil {
			t.Fatalf("verify mfa: %v", err)
		}
		if result.AccessToken == "" || result.RefreshToken == "" || result.SessionID == "" {
			t.Fatalf("expected token pair and session, got %+v", result)
		}

		_, err = f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Ticket:  next.MFATicket,
			Code:    totpCodeAt(t, f.mustSecret(t, "acct-login"), f.clock.Now()),
			TraceID: "trace-replay",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
	})

	t.Run("recovery code is one time", func(t *testing.T) {
		first := f.login(t, "admin@example.com", "Correct#Passw0rd")
		result, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Ticket:  first.MFATicket,
			Code:    recoveryCodes[0],
			TraceID: "trace-recovery",
		})
		if err != nil {
			t.Fatalf("verify with recovery code: %v", err)
		}
		if result.AccessToken == "" {
			t.Fatal("recovery code login must issue tokens")
		}

		second := f.login(t, "admin@example.com", "Correct#Passw0rd")
		_, err = f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Ticket:  second.MFATicket,
			Code:    recoveryCodes[0],
			TraceID: "trace-recovery-replay",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
	})
}

// TestMFABindingRequiredForAdmin 覆盖 AC-ENT-01-4 / AC-ENT-01-6：
// A 端未完成绑定时登录返回 200 + mfa_setup_ticket，绑定完成后直接签发令牌（§7.2.1）。
func TestMFABindingRequiredForAdmin(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.MFAAdminRequired = true
	})
	f.seedAccount(t, "acct-required", "admin@example.com", "Correct#Passw0rd")

	login := func(t *testing.T) app.LoginResult {
		t.Helper()
		result, err := f.svc.Login(ctx, app.LoginInput{
			Subject: "admin@example.com", Password: "Correct#Passw0rd", TraceID: "trace-required",
		})
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		return result
	}
	setup := func(t *testing.T, ticket string) app.MFASetupResult {
		t.Helper()
		result, err := f.svc.SetupMFA(ctx, app.MFASetupInput{SetupTicket: ticket, IPHash: "ip-hash"})
		if err != nil {
			t.Fatalf("setup mfa: %v", err)
		}
		return result
	}
	verify := func(t *testing.T, ticket, code string) (app.MFAVerifyResult, error) {
		t.Helper()
		return f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			SetupTicket: ticket, Code: code, IPHash: "ip-hash", UAHash: "ua-hash", TraceID: "trace-setup-verify",
		})
	}

	first := login(t)
	if !first.MFABindingRequired || first.MFASetupTicket == "" {
		t.Fatalf("expected binding ticket, got %+v", first)
	}
	if first.MFARequired || first.AccessToken != "" || first.RefreshToken != "" || first.SessionID != "" {
		t.Fatalf("unbound admin must not receive tokens: %+v", first)
	}

	// 绑定票据不能当作两步登录票据使用，两步登录票据也不能用于绑定。
	if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{Ticket: first.MFASetupTicket, Code: "000000"}); err == nil {
		t.Fatal("setup ticket must not be accepted as mfa_ticket")
	}

	pending := setup(t, first.MFASetupTicket)
	if pending.Secret == "" || !strings.HasPrefix(pending.OTPAuthURI, "otpauth://totp/") {
		t.Fatalf("unexpected setup result: %+v", pending)
	}
	// 重复 setup 覆盖未确认绑定，不消耗票据。
	latest := setup(t, first.MFASetupTicket)
	if latest.Secret == pending.Secret {
		t.Fatal("repeat setup must rotate the pending secret")
	}

	// 首码错误：票据作废、绑定保持未确认。
	_, err := verify(t, first.MFASetupTicket, "000000")
	requireAppCode(t, err, apperr.CodeUnauthorized)
	_, err = verify(t, first.MFASetupTicket, "123456")
	requireAppCode(t, err, apperr.CodeUnauthorized)

	// 重新登录获取新票据，首码正确后直接拿到令牌。
	second := login(t)
	if !second.MFABindingRequired || second.MFASetupTicket == "" {
		t.Fatalf("expected a fresh binding ticket, got %+v", second)
	}
	fresh := setup(t, second.MFASetupTicket)
	result, err := verify(t, second.MFASetupTicket, totpCodeAt(t, fresh.Secret, f.clock.Now()))
	if err != nil {
		t.Fatalf("complete mfa setup: %v", err)
	}
	if result.BindingConfirmed {
		t.Fatal("setup ticket path must return tokens, not 204")
	}
	if result.AccessToken == "" || result.RefreshToken == "" || result.SessionID == "" {
		t.Fatalf("binding completion must issue tokens: %+v", result)
	}
	enrollment, err := f.mfa.FindByAccount(ctx, "acct-required")
	if err != nil {
		t.Fatalf("load enrollment: %v", err)
	}
	if !enrollment.IsConfirmed() {
		t.Fatal("binding must be confirmed after successful setup verify")
	}

	// 重放同一张绑定票据：40100 且无副作用（不新增会话、不改变 token_version）。
	sessionCountBefore := len(mustListSessions(t, f, "acct-required"))
	if _, err := verify(t, second.MFASetupTicket, totpCodeAt(t, fresh.Secret, f.clock.Now())); err == nil {
		t.Fatal("replayed setup ticket must be rejected")
	}
	if got := len(mustListSessions(t, f, "acct-required")); got != sessionCountBefore {
		t.Fatalf("replayed setup ticket must not create sessions: %d -> %d", sessionCountBefore, got)
	}

	// 后续登录进入正常两步登录。
	challenge := login(t)
	if !challenge.MFARequired || challenge.MFATicket == "" || challenge.MFABindingRequired {
		t.Fatalf("expected two-step challenge, got %+v", challenge)
	}
}

// TestMFASetupTicketExpiryAndScopeIsolation 覆盖绑定票据的有效期与 scope 隔离。
func TestMFASetupTicketExpiryAndScopeIsolation(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.MFAAdminRequired = true
	})
	f.seedAccount(t, "acct-scope", "admin@example.com", "Correct#Passw0rd")

	challenge, err := f.svc.Login(ctx, app.LoginInput{
		Subject: "admin@example.com", Password: "Correct#Passw0rd", TraceID: "trace-scope",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// 过期票据（>10 分钟）在 setup 与 verify 两侧都必须被拒绝。
	f.clock.Advance(app.MFASetupTicketTTL + time.Second)
	if _, err := f.svc.SetupMFA(ctx, app.MFASetupInput{SetupTicket: challenge.MFASetupTicket}); err == nil {
		t.Fatal("expired setup ticket must be rejected by setup")
	}
	if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
		SetupTicket: challenge.MFASetupTicket, Code: "123456",
	}); err == nil {
		t.Fatal("expired setup ticket must be rejected by verify")
	}

	// 已绑定账号的两步登录票据不能用于绑定。
	f.clock.Advance(-(app.MFASetupTicketTTL + time.Second))
	secret, _ := bindMFA(t, f, "acct-scope")
	_ = secret
	login := f.login(t, "admin@example.com", "Correct#Passw0rd")
	if !login.MFARequired || login.MFATicket == "" {
		t.Fatalf("expected two-step challenge, got %+v", login)
	}
	_, err = f.svc.SetupMFA(ctx, app.MFASetupInput{SetupTicket: login.MFATicket})
	requireAppCode(t, err, apperr.CodeUnauthorized)
}

// TestMFATOTPCodeReplayRejected 验证同一 TOTP 码在窗口内第二次使用被拒绝。
func TestMFATOTPCodeReplayRejected(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-replay", "admin@example.com", "Correct#Passw0rd")
	secret, _ := bindMFA(t, f, "acct-replay")
	loginWithMFA(t, f, "admin@example.com", "Correct#Passw0rd")

	challenge := f.login(t, "admin@example.com", "Correct#Passw0rd")
	if !challenge.MFARequired {
		t.Fatalf("expected mfa challenge, got %+v", challenge)
	}
	_, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
		Ticket:  challenge.MFATicket,
		Code:    totpCodeAt(t, secret, f.clock.Now()),
		TraceID: "trace-replay",
	})
	requireAppCode(t, err, apperr.CodeUnauthorized)
}

// TestMFATOTPConcurrentSameCodeOnlyOneSucceeds 验证并发使用同一 TOTP 码只有一次成功。
func TestMFATOTPConcurrentSameCodeOnlyOneSucceeds(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-race", "admin@example.com", "Correct#Passw0rd")
	secret, _ := bindMFA(t, f, "acct-race")

	tickets := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		challenge := f.login(t, "admin@example.com", "Correct#Passw0rd")
		tickets = append(tickets, challenge.MFATicket)
	}
	code := totpCodeAt(t, secret, f.clock.Now())
	var success int32
	var wg sync.WaitGroup
	for _, ticket := range tickets {
		wg.Add(1)
		go func(tk string) {
			defer wg.Done()
			if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{Ticket: tk, Code: code}); err == nil {
				atomic.AddInt32(&success, 1)
			}
		}(ticket)
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("same TOTP code successes = %d, want 1", success)
	}
}

// TestMFARecoveryCodesConcurrentEachOnce 验证并发消费不同恢复码时每个码只成功一次。
func TestMFARecoveryCodesConcurrentEachOnce(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-recovery", "admin@example.com", "Correct#Passw0rd")
	_, codes := bindMFA(t, f, "acct-recovery")

	tickets := make([]string, 0, len(codes))
	for range codes {
		challenge := f.login(t, "admin@example.com", "Correct#Passw0rd")
		tickets = append(tickets, challenge.MFATicket)
	}
	var success int32
	var wg sync.WaitGroup
	for i, code := range codes {
		wg.Add(1)
		go func(ticket, recovery string) {
			defer wg.Done()
			if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{Ticket: ticket, Code: recovery}); err == nil {
				atomic.AddInt32(&success, 1)
			}
		}(tickets[i], code)
	}
	wg.Wait()
	if int(success) != len(codes) {
		t.Fatalf("recovery code successes = %d, want %d", success, len(codes))
	}

	// 任一已消费的恢复码不可再次使用。
	challenge := f.login(t, "admin@example.com", "Correct#Passw0rd")
	_, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{Ticket: challenge.MFATicket, Code: codes[0]})
	requireAppCode(t, err, apperr.CodeUnauthorized)
}

// TestMFADisableAndVerifyConcurrency 验证并发解绑与校验后绑定最终为删除状态。
func TestMFADisableAndVerifyConcurrency(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-concurrent", "admin@example.com", "Correct#Passw0rd")
	secret, codes := bindMFA(t, f, "acct-concurrent")

	challenge := f.login(t, "admin@example.com", "Correct#Passw0rd")
	if !challenge.MFARequired {
		t.Fatalf("expected mfa challenge, got %+v", challenge)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Ticket: challenge.MFATicket,
			Code:   totpCodeAt(t, secret, f.clock.Now()),
		})
	}()
	go func() {
		defer wg.Done()
		_ = f.svc.DisableMFA(ctx, app.SessionActor{AccountID: "acct-concurrent", TraceID: "trace-disable"}, codes[0])
	}()
	wg.Wait()

	if _, err := f.mfa.FindByAccount(ctx, "acct-concurrent"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("binding must be deleted after concurrent disable/verify, got %v", err)
	}
}

// TestMFASetupTicketReplayNoLockout 验证票据重放不锁定账号且产生独立审计。
func TestMFASetupTicketReplayNoLockout(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.MFAAdminRequired = true
	}, withCapture(capture))
	f.seedAccount(t, "acct-replay-ticket", "admin@example.com", "Correct#Passw0rd")
	login := f.login(t, "admin@example.com", "Correct#Passw0rd")
	if !login.MFABindingRequired || login.MFASetupTicket == "" {
		t.Fatalf("expected mfa binding challenge, got %+v", login)
	}
	setup, err := f.svc.SetupMFA(ctx, app.MFASetupInput{SetupTicket: login.MFASetupTicket})
	if err != nil {
		t.Fatalf("setup mfa: %v", err)
	}
	code := totpCodeAt(t, setup.Secret, f.clock.Now())
	if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{SetupTicket: login.MFASetupTicket, Code: code}); err != nil {
		t.Fatalf("first setup verify: %v", err)
	}
	if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{SetupTicket: login.MFASetupTicket, Code: code}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("replayed setup ticket err = %v, want 401", err)
	}
	account, err := f.accounts.FindByID(ctx, "acct-replay-ticket")
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Status == domain.AccountStatusLocked {
		t.Fatalf("ticket replay must not lock the account")
	}
	if events := capture.byAction(audit.ActionAuthMFATicketReplay); len(events) != 1 {
		t.Fatalf("ticket replay audit = %d, want 1", len(events))
	}
}

// TestMFAKeyRotationReencryptsLegacyCiphertext 验证旧密文可解密并在登录后重加密为 active key。
func TestMFAKeyRotationReencryptsLegacyCiphertext(t *testing.T) {
	ctx := context.Background()
	legacyKey := []byte("01234567890123456789012345678901")
	activeKey := []byte("abcdefghijklmnopqrstuvwxyz012345")
	legacy, err := crypto.NewAES256GCM(legacyKey)
	if err != nil {
		t.Fatalf("legacy cipher: %v", err)
	}
	ring, err := crypto.NewKeyRing(map[string][]byte{"active": activeKey}, "active", legacyKey)
	if err != nil {
		t.Fatalf("key ring: %v", err)
	}
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.MFACipher = ring
	})
	f.seedAccount(t, "acct-rotate", "admin@example.com", "Correct#Passw0rd")
	secret, err := totp.GenerateSecret()
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}
	legacyCT, err := legacy.Seal(secret, []byte("identity:mfa:acct-rotate"))
	if err != nil {
		t.Fatalf("seal legacy: %v", err)
	}
	enrollment, err := domain.NewMFAEnrollment("acct-rotate", legacyCT, f.clock.Now())
	if err != nil {
		t.Fatalf("new enrollment: %v", err)
	}
	if err := enrollment.SetRecoveryCodes([]string{"hash-1"}, f.clock.Now()); err != nil {
		t.Fatalf("set recovery codes: %v", err)
	}
	if err := enrollment.Confirm(f.clock.Now()); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := f.mfa.Save(ctx, enrollment, 0); err != nil {
		t.Fatalf("save enrollment: %v", err)
	}

	challenge := f.login(t, "admin@example.com", "Correct#Passw0rd")
	if !challenge.MFARequired {
		t.Fatalf("expected mfa challenge, got %+v", challenge)
	}
	if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
		Ticket: challenge.MFATicket,
		Code:   totpCodeAt(t, totp.EncodeSecret(secret), f.clock.Now()),
	}); err != nil {
		t.Fatalf("verify with legacy ciphertext: %v", err)
	}
	stored, err := f.mfa.FindByAccount(ctx, "acct-rotate")
	if err != nil {
		t.Fatalf("reload enrollment: %v", err)
	}
	if ring.KeyID(stored.SecretCiphertext) != "active" {
		t.Fatalf("ciphertext key id = %q, want active", ring.KeyID(stored.SecretCiphertext))
	}
	if _, err := ring.Open(stored.SecretCiphertext, []byte("identity:mfa:acct-rotate")); err != nil {
		t.Fatalf("re-encrypted ciphertext must decrypt: %v", err)
	}
}

func TestMFADisable(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-disable", "admin@example.com", "Correct#Passw0rd")
	secret, _ := bindMFA(t, f, "acct-disable")
	login := loginWithMFA(t, f, "admin@example.com", "Correct#Passw0rd")

	t.Run("wrong code keeps binding", func(t *testing.T) {
		err := f.svc.DisableMFA(ctx, app.SessionActor{AccountID: "acct-disable"}, "000000")
		requireAppCode(t, err, apperr.CodeUnauthorized)
		if _, loadErr := f.mfa.FindByAccount(ctx, "acct-disable"); loadErr != nil {
			t.Fatalf("binding must remain after failed disable: %v", loadErr)
		}
	})

	t.Run("correct code removes binding and revokes sessions", func(t *testing.T) {
		// 登录已消费当前时间步；解绑必须使用下一个码，验证防重放生效。
		f.clock.Advance(totp.Period)
		err := f.svc.DisableMFA(ctx, app.SessionActor{AccountID: "acct-disable", TraceID: "trace-disable"},
			totpCodeAt(t, secret, f.clock.Now()))
		if err != nil {
			t.Fatalf("disable mfa: %v", err)
		}
		if _, loadErr := f.mfa.FindByAccount(ctx, "acct-disable"); !errors.Is(loadErr, apperr.ErrNotFound) {
			t.Fatalf("binding must be deleted, got %v", loadErr)
		}
		session := f.sessionOf(t, login.SessionID)
		if session.RevokedAt == nil {
			t.Fatal("all sessions must be revoked after disable")
		}
	})
}

func TestMFASetupRejectedOnBizChannel(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelBiz)
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: "acct-biz", Channel: domain.ChannelBiz, Subject: "staff@example.com",
		MerchantID: "m-1", Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := f.accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	credential, err := domain.NewCredential(domain.NewCredentialParams{
		ID: "cred-acct-biz", AccountID: account.ID, Type: domain.CredentialTypePassword,
		Hash: "fake$Correct#Passw0rd", Algorithm: "fake", Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new credential: %v", err)
	}
	if err := f.credentials.Save(ctx, credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	f.login(t, "staff@example.com", "Correct#Passw0rd")

	_, err = f.svc.SetupMFA(ctx, app.MFASetupInput{Actor: app.SessionActor{AccountID: "acct-biz"}})
	requireAppCode(t, err, apperr.CodeBadRequest)
}

// lockAccountForAudit 把账号置为锁定，用于验证 MFA 授权失败审计（IDN-P2-02）。
func lockAccountForAudit(t *testing.T, f *authFixture, accountID string) {
	t.Helper()
	ctx := context.Background()
	account, err := f.accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	expected := account.Version
	now := f.clock.Now()
	if err := account.Lock(now.Add(time.Hour), now); err != nil {
		t.Fatalf("lock account: %v", err)
	}
	if err := f.accounts.Save(ctx, account, expected); err != nil {
		t.Fatalf("save locked account: %v", err)
	}
}

// requireSingleLoginFailed 断言指定原因恰好一条 login_failed；忽略登录阶段已产生的其它原因。
func requireSingleLoginFailed(t *testing.T, capture *captureEmitter, reason string) {
	t.Helper()
	var matched []audit.Event
	for _, event := range capture.byAction(audit.ActionAuthLoginFailed) {
		if event.Reason == reason {
			matched = append(matched, event)
		}
	}
	if len(matched) != 1 {
		t.Fatalf("expected exactly one auth.login_failed(reason=%s), got %+v", reason, matched)
	}
	if matched[0].Outcome != audit.OutcomeFailure {
		t.Fatalf("unexpected login_failed event: %+v", matched[0])
	}
}

// TestMFARecoveryCodeAudited 验证恢复码消费写入 auth.mfa.recovery_used（含剩余数量、不含码值）。
func TestMFARecoveryCodeAudited(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
	f.seedAccount(t, "acct-recovery-audit", "admin@example.com", "Correct#Passw0rd")
	_, codes := bindMFA(t, f, "acct-recovery-audit")
	challenge := f.login(t, "admin@example.com", "Correct#Passw0rd")
	if !challenge.MFARequired || challenge.MFATicket == "" {
		t.Fatalf("expected mfa challenge, got %+v", challenge)
	}
	if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
		Ticket: challenge.MFATicket, Code: codes[0], TraceID: "trace-recovery-audit",
	}); err != nil {
		t.Fatalf("verify with recovery code: %v", err)
	}

	events := capture.byAction(audit.ActionAuthMFARecoveryUsed)
	if len(events) != 1 {
		t.Fatalf("expected exactly one auth.mfa.recovery_used, got %+v", events)
	}
	if events[0].Outcome != audit.OutcomeSuccess || events[0].Attrs["remaining"] != "9" {
		t.Fatalf("unexpected recovery_used event: %+v", events[0])
	}
	if strings.Contains(capture.dump(t), codes[0]) {
		t.Fatalf("audit dump leaks recovery code: %s", capture.dump(t))
	}
}

// TestMFAAuthorizeFailuresAudited 验证 MFA 校验遇到 locked / disabled 时先写审计再拒绝。
func TestMFAAuthorizeFailuresAudited(t *testing.T) {
	ctx := context.Background()

	t.Run("complete login locked", func(t *testing.T) {
		capture := &captureEmitter{}
		f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
		f.seedAccount(t, "acct-mfa-locked", "admin@example.com", "Correct#Passw0rd")
		bindMFA(t, f, "acct-mfa-locked")
		challenge := f.login(t, "admin@example.com", "Correct#Passw0rd")
		if !challenge.MFARequired || challenge.MFATicket == "" {
			t.Fatalf("expected mfa challenge, got %+v", challenge)
		}
		lockAccountForAudit(t, f, "acct-mfa-locked")

		_, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Ticket: challenge.MFATicket, Code: "000000", TraceID: "trace-mfa-locked",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
		requireSingleLoginFailed(t, capture, domain.LoginReasonLocked)
	})

	t.Run("complete setup disabled", func(t *testing.T) {
		capture := &captureEmitter{}
		f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture), func(cfg *app.AuthServiceConfig) {
			cfg.MFAAdminRequired = true
		})
		f.seedAccount(t, "acct-setup-disabled", "admin@example.com", "Correct#Passw0rd")
		login, err := f.svc.Login(ctx, app.LoginInput{
			Subject: "admin@example.com", Password: "Correct#Passw0rd", TraceID: "trace-setup-disabled",
		})
		if err != nil || login.MFASetupTicket == "" {
			t.Fatalf("login: %v result=%+v", err, login)
		}
		account, loadErr := f.accounts.FindByID(ctx, "acct-setup-disabled")
		if loadErr != nil {
			t.Fatalf("load account: %v", loadErr)
		}
		expected := account.Version
		if disableErr := account.Disable(f.clock.Now()); disableErr != nil {
			t.Fatalf("disable account: %v", disableErr)
		}
		if saveErr := f.accounts.Save(ctx, account, expected); saveErr != nil {
			t.Fatalf("save disabled account: %v", saveErr)
		}

		_, err = f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			SetupTicket: login.MFASetupTicket, Code: "000000", TraceID: "trace-setup-disabled",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
		requireSingleLoginFailed(t, capture, domain.LoginReasonDisabled)
	})

	t.Run("confirm binding locked", func(t *testing.T) {
		capture := &captureEmitter{}
		f := newAuthFixture(t, domain.ChannelAdmin, withCapture(capture))
		f.seedAccount(t, "acct-confirm-locked", "admin@example.com", "Correct#Passw0rd")
		setup, err := f.svc.SetupMFA(ctx, app.MFASetupInput{
			Actor: app.SessionActor{AccountID: "acct-confirm-locked", TraceID: "trace-confirm-setup"},
		})
		if err != nil {
			t.Fatalf("setup mfa: %v", err)
		}
		lockAccountForAudit(t, f, "acct-confirm-locked")

		_, err = f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
			Actor: app.SessionActor{AccountID: "acct-confirm-locked", TraceID: "trace-confirm-locked"},
			Code:  totpCodeAt(t, setup.Secret, f.clock.Now()), TraceID: "trace-confirm-locked",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
		requireSingleLoginFailed(t, capture, domain.LoginReasonLocked)
	})
}
