package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// capturedSender 记录验证码并可选注入发送失败。
type capturedSender struct {
	phone string
	code  string
	err   error
}

// SendCode 实现 app.CodeSender。
func (s *capturedSender) SendCode(_ context.Context, phone, code string) error {
	if s.err != nil {
		return s.err
	}
	s.phone, s.code = phone, code
	return nil
}

// smsFixture 聚合短信登录用例依赖。
type smsFixture struct {
	*authFixture
	svc     *app.SMSLoginService
	codes   *cache.MemorySMSCodeStore
	limiter *cache.MemorySMSRateLimiter
	sender  *capturedSender
}

// newSMSFixture 构造 C 端短信登录服务；cfgMut 可覆盖限流参数。
func newSMSFixture(t *testing.T, cfgMut func(*app.SMSLoginConfig)) *smsFixture {
	t.Helper()
	base := newAuthFixture(t, domain.ChannelC)
	guard, err := app.NewLoginGuard(base.accounts,
		persistence.NewMemoryLoginAttemptRepository(), base.ids, app.DefaultLockoutPolicy())
	if err != nil {
		t.Fatalf("new login guard: %v", err)
	}
	codes := cache.NewMemorySMSCodeStore(base.clock)
	limiter := cache.NewMemorySMSRateLimiter(base.clock)
	sender := &capturedSender{}
	cfg := app.SMSLoginConfig{
		Accounts:       base.accounts,
		Guard:          guard,
		Codes:          codes,
		Limiter:        limiter,
		Sender:         sender,
		Auth:           base.svc,
		CodeTTL:        5 * time.Minute,
		ResendInterval: 60 * time.Second,
		MaxAttempts:    5,
		DailyPerPhone:  10,
		HourlyPerIP:    30,
	}
	if cfgMut != nil {
		cfgMut(&cfg)
	}
	svc, err := app.NewSMSLoginService(cfg)
	if err != nil {
		t.Fatalf("new sms login service: %v", err)
	}
	return &smsFixture{authFixture: base, svc: svc, codes: codes, limiter: limiter, sender: sender}
}

// seedPhoneAccount 写入手机号摘要主体的 pending 账号。
func (f *smsFixture) seedPhoneAccount(t *testing.T, phone string) *domain.Account {
	t.Helper()
	subject := "phone:" + app.HashAccountRef(domain.ChannelC, phone)
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: "acct-phone", Channel: domain.ChannelC, Subject: subject, Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := f.accounts.Save(context.Background(), account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	return account
}

func TestSMSLoginSendValidation(t *testing.T) {
	cases := []struct {
		name  string
		phone string
		code  int
	}{
		{name: "empty", phone: "", code: 40000},
		{name: "no_plus", phone: "13800138000", code: 40000},
		{name: "leading_zero", phone: "+012345678", code: 40000},
		{name: "valid", phone: "+8613800138000", code: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSMSFixture(t, nil)
			err := f.svc.Send(context.Background(), app.SendSMSCodeInput{Phone: tc.phone})
			if tc.code != 0 {
				requireAppCode(t, err, tc.code)
				return
			}
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			if f.sender.phone != tc.phone || len(f.sender.code) != 6 {
				t.Fatalf("sender got phone=%q code=%q", f.sender.phone, f.sender.code)
			}
		})
	}
}

func TestSMSLoginSendThrottle(t *testing.T) {
	t.Run("resend_interval", func(t *testing.T) {
		f := newSMSFixture(t, nil)
		ctx := context.Background()
		if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "+8613800138000"}); err != nil {
			t.Fatalf("first send: %v", err)
		}
		requireAppCode(t, f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "+8613800138000"}), 42900)
		f.clock.Advance(61 * time.Second)
		if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "+8613800138000"}); err != nil {
			t.Fatalf("send after interval: %v", err)
		}
	})

	t.Run("daily_limit", func(t *testing.T) {
		f := newSMSFixture(t, func(cfg *app.SMSLoginConfig) { cfg.DailyPerPhone = 1 })
		ctx := context.Background()
		if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "+8613800138000"}); err != nil {
			t.Fatalf("first send: %v", err)
		}
		f.clock.Advance(61 * time.Second)
		requireAppCode(t, f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "+8613800138000"}), 42900)
	})

	t.Run("hourly_ip", func(t *testing.T) {
		f := newSMSFixture(t, func(cfg *app.SMSLoginConfig) { cfg.HourlyPerIP = 1 })
		ctx := context.Background()
		if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "+8613800138000", IPHash: "ip-1"}); err != nil {
			t.Fatalf("first send: %v", err)
		}
		f.clock.Advance(61 * time.Second)
		requireAppCode(t, f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "+8613800138001", IPHash: "ip-1"}), 42900)
	})
}

func TestSMSLoginSendFailureCleansCode(t *testing.T) {
	f := newSMSFixture(t, nil)
	f.sender.err = errors.New("provider down")
	ctx := context.Background()
	phone := "+8613800138000"
	requireAppCode(t, f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}), 50300)

	phoneHash := app.HashAccountRef(domain.ChannelC, phone)
	if _, err := f.codes.Get(ctx, phoneHash); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("code should be removed after send failure, got %v", err)
	}
}

func TestSMSLogin(t *testing.T) {
	ctx := context.Background()
	phone := "+8613800138000"

	t.Run("success_activates_pending", func(t *testing.T) {
		f := newSMSFixture(t, nil)
		account := f.seedPhoneAccount(t, phone)
		if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
			t.Fatalf("send: %v", err)
		}
		result, err := f.svc.Login(ctx, app.SMSLoginInput{Phone: phone, Code: f.sender.code, TraceID: "trace-sms"})
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		if result.AccessToken == "" || result.RefreshToken == "" || result.AccountID != account.ID {
			t.Fatalf("unexpected login result: %+v", result)
		}
		loaded, err := f.accounts.FindByID(ctx, account.ID)
		if err != nil {
			t.Fatalf("load account: %v", err)
		}
		if loaded.Status != domain.AccountStatusActive {
			t.Fatalf("status = %s, want active", loaded.Status)
		}
	})

	t.Run("invalid_code", func(t *testing.T) {
		f := newSMSFixture(t, nil)
		f.seedPhoneAccount(t, phone)
		if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
			t.Fatalf("send: %v", err)
		}
		requireAppCode(t, func() error {
			_, err := f.svc.Login(ctx, app.SMSLoginInput{Phone: phone, Code: "000000"})
			return err
		}(), 40100)
	})

	t.Run("max_attempts_invalidates_code", func(t *testing.T) {
		f := newSMSFixture(t, func(cfg *app.SMSLoginConfig) { cfg.MaxAttempts = 2 })
		if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
			t.Fatalf("send: %v", err)
		}
		phoneHash := app.HashAccountRef(domain.ChannelC, phone)
		for i := 0; i < 2; i++ {
			_, err := f.svc.Login(ctx, app.SMSLoginInput{Phone: phone, Code: "000000"})
			requireAppCode(t, err, 40100)
		}
		if _, err := f.codes.Get(ctx, phoneHash); !errors.Is(err, apperr.ErrNotFound) {
			t.Fatalf("code should be invalidated after max attempts, got %v", err)
		}
	})

	t.Run("unknown_phone", func(t *testing.T) {
		f := newSMSFixture(t, nil)
		if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
			t.Fatalf("send: %v", err)
		}
		_, err := f.svc.Login(ctx, app.SMSLoginInput{Phone: phone, Code: f.sender.code})
		requireAppCode(t, err, 40100)
	})

	t.Run("expired_code", func(t *testing.T) {
		f := newSMSFixture(t, nil)
		f.seedPhoneAccount(t, phone)
		if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
			t.Fatalf("send: %v", err)
		}
		f.clock.Advance(5*time.Minute + time.Second)
		_, err := f.svc.Login(ctx, app.SMSLoginInput{Phone: phone, Code: f.sender.code})
		requireAppCode(t, err, 40100)
	})

	t.Run("invalid_formats", func(t *testing.T) {
		f := newSMSFixture(t, nil)
		_, err := f.svc.Login(ctx, app.SMSLoginInput{Phone: phone, Code: "12ab"})
		requireAppCode(t, err, 40000)
	})
}
