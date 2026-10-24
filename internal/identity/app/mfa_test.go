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
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
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
	setup, err := f.svc.SetupMFA(ctx, app.SessionActor{AccountID: accountID, TraceID: "trace-setup"})
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

func TestMFASetupAndBindingConfirmation(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-mfa", "admin@example.com", "Correct#Passw0rd")
	f.login(t, "admin@example.com", "Correct#Passw0rd")

	setup, err := f.svc.SetupMFA(ctx, app.SessionActor{AccountID: "acct-mfa", TraceID: "trace-setup"})
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
		_, err := f.svc.SetupMFA(ctx, app.SessionActor{AccountID: "acct-mfa"})
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

// TestMFABindingRequiredForAdmin 覆盖 AC-ENT-01-4：A 端未完成 MFA 不得获得管理令牌。
func TestMFABindingRequiredForAdmin(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.MFAAdminRequired = true
	})
	f.seedAccount(t, "acct-required", "admin@example.com", "Correct#Passw0rd")

	result, err := f.svc.Login(ctx, app.LoginInput{
		Subject: "admin@example.com", Password: "Correct#Passw0rd", TraceID: "trace-required",
	})
	requireAppCode(t, err, apperr.CodeUnauthorized)
	if result.AccessToken != "" || result.RefreshToken != "" || result.SessionID != "" {
		t.Fatalf("unbound admin must not receive tokens: %+v", result)
	}
	if !errors.Is(err, domain.ErrMFARequired) {
		t.Fatalf("error %v must wrap domain.ErrMFARequired", err)
	}

	if _, err := f.svc.SetupMFA(ctx, app.SessionActor{AccountID: "acct-required"}); err != nil {
		t.Fatalf("setup mfa: %v", err)
	}
	if _, err := f.svc.VerifyMFA(ctx, app.MFAVerifyInput{
		Code:    totpCodeAt(t, f.mustSecret(t, "acct-required"), f.clock.Now()),
		Actor:   app.SessionActor{AccountID: "acct-required"},
		TraceID: "trace-confirm-required",
	}); err != nil {
		t.Fatalf("confirm binding: %v", err)
	}
	enrollment, err := f.mfa.FindByAccount(ctx, "acct-required")
	if err != nil {
		t.Fatalf("load enrollment: %v", err)
	}
	if !enrollment.IsConfirmed() {
		t.Fatal("binding must be confirmed before two-step login")
	}
	challenge, err := f.svc.Login(ctx, app.LoginInput{
		Subject: "admin@example.com", Password: "Correct#Passw0rd", TraceID: "trace-pending",
	})
	if err != nil {
		t.Fatalf("login after setup: %v", err)
	}
	if !challenge.MFARequired {
		t.Fatalf("expected MFA challenge, got %+v", challenge)
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

	_, err = f.svc.SetupMFA(ctx, app.SessionActor{AccountID: "acct-biz"})
	requireAppCode(t, err, apperr.CodeBadRequest)
}
