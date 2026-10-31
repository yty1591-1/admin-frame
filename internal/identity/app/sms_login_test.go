package app_test

import (
	"context"
	"errors"
	"strings"
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

// TestSMSRegisterCreatesConsumerAccount 验证短信注册开户、重复注册与后续登录。
func TestSMSRegisterCreatesConsumerAccount(t *testing.T) {
	f := newSMSFixture(t, nil)
	ctx := context.Background()
	phone := "+8613800138000"

	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
		t.Fatalf("send: %v", err)
	}
	result, err := f.svc.Register(ctx, app.SMSLoginInput{
		Phone: phone, Code: f.sender.code, TraceID: "trace-register",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if result.AccessToken == "" || result.RefreshToken == "" || result.AccountID == "" {
		t.Fatalf("register must return tokens, got %+v", result)
	}

	account, err := f.accounts.FindByID(ctx, result.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Status != domain.AccountStatusActive || account.Channel != domain.ChannelC {
		t.Fatalf("account = %+v, want active C account", account)
	}
	credential, err := f.credentials.FindByAccount(ctx, account.ID, domain.CredentialTypePhone)
	if err != nil {
		t.Fatalf("phone credential must exist: %v", err)
	}
	if credential.Hash != app.HashAccountRef(domain.ChannelC, phone) {
		t.Fatalf("phone credential hash = %q, want account ref digest", credential.Hash)
	}

	// 重复注册：验证码有效但手机号已注册，统一 40100，不暴露账号存在。
	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
		t.Fatalf("resend: %v", err)
	}
	requireAppCode(t, mustRegister(f, phone), apperr.CodeUnauthorized)

	// 已开户手机号可正常验证码登录。
	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
		t.Fatalf("send for login: %v", err)
	}
	if _, err := f.svc.Login(ctx, app.SMSLoginInput{Phone: phone, Code: f.sender.code, TraceID: "trace-login"}); err != nil {
		t.Fatalf("login after register: %v", err)
	}
}

// TestSMSRebindPhone 验证手机号换绑：主体与凭据切换、旧会话撤销、旧号失效。
func TestSMSRebindPhone(t *testing.T) {
	f := newSMSFixture(t, nil)
	ctx := context.Background()
	oldPhone := "+8613800138001"
	newPhone := "+8613800138002"

	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: oldPhone}); err != nil {
		t.Fatalf("send old: %v", err)
	}
	registered, err := f.svc.Register(ctx, app.SMSLoginInput{Phone: oldPhone, Code: f.sender.code, TraceID: "trace-reg"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: oldPhone}); err != nil {
		t.Fatalf("send old for reauth: %v", err)
	}
	oldCode := f.sender.code
	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: newPhone}); err != nil {
		t.Fatalf("send new: %v", err)
	}
	actor := app.SessionActor{AccountID: registered.AccountID, SessionID: registered.SessionID, TraceID: "trace-rebind"}
	// 缺少旧号验证码必须拒绝：仅持有 Bearer 不能换绑。
	requireAppCode(t, f.svc.Rebind(ctx, app.SMSRebindInput{
		Actor: actor, Phone: newPhone, Code: f.sender.code,
	}), apperr.CodeBadRequest)
	// 旧号验证码已在上一步被拒绝路径消费，重新发送后再换绑。
	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: oldPhone}); err != nil {
		t.Fatalf("resend old for reauth: %v", err)
	}
	oldCode = f.sender.code
	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: newPhone}); err != nil {
		t.Fatalf("resend new: %v", err)
	}
	newCode := f.sender.code
	if err := f.svc.Rebind(ctx, app.SMSRebindInput{
		Actor: actor, Phone: newPhone, Code: newCode, OldCode: oldCode,
	}); err != nil {
		t.Fatalf("rebind: %v", err)
	}

	account, err := f.accounts.FindByID(ctx, registered.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Subject != "phone:"+app.HashAccountRef(domain.ChannelC, newPhone) {
		t.Fatalf("subject = %q, want new phone subject", account.Subject)
	}
	credential, err := f.credentials.FindByAccount(ctx, registered.AccountID, domain.CredentialTypePhone)
	if err != nil {
		t.Fatalf("load credential: %v", err)
	}
	if credential.Hash != app.HashAccountRef(domain.ChannelC, newPhone) {
		t.Fatalf("credential hash = %q, want new phone digest", credential.Hash)
	}
	if account.TokenVersion != 2 {
		t.Fatalf("token_version = %d, want 2", account.TokenVersion)
	}
	if session := f.sessionOf(t, registered.SessionID); session.Status != domain.SessionStatusRevoked {
		t.Fatalf("sessions must be revoked on rebind: %+v", session)
	}

	// 旧手机号不再能登录；新手机号可登录。
	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: oldPhone}); err != nil {
		t.Fatalf("send old after rebind: %v", err)
	}
	requireAppCode(t, mustLogin(f, oldPhone), apperr.CodeUnauthorized)

	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: newPhone}); err != nil {
		t.Fatalf("send new for login: %v", err)
	}
	if _, err := f.svc.Login(ctx, app.SMSLoginInput{Phone: newPhone, Code: f.sender.code, TraceID: "trace-new-login"}); err != nil {
		t.Fatalf("login with new phone: %v", err)
	}
}

// mustLogin 使用最近一次发送的验证码执行登录。
func mustLogin(f *smsFixture, phone string) error {
	_, err := f.svc.Login(context.Background(), app.SMSLoginInput{
		Phone: phone, Code: f.sender.code, TraceID: "trace-login",
	})
	return err
}

// TestDeleteConsumerAccount 验证消费者自助注销：软删、会话撤销与登录拒绝。
func TestDeleteConsumerAccount(t *testing.T) {
	f := newSMSFixture(t, nil)
	ctx := context.Background()
	phone := "+8613800138003"

	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
		t.Fatalf("send: %v", err)
	}
	registered, err := f.svc.Register(ctx, app.SMSLoginInput{Phone: phone, Code: f.sender.code, TraceID: "trace-reg"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
		t.Fatalf("send for delete reauth: %v", err)
	}
	deleteCode := f.sender.code
	if err := f.svc.DeleteAccount(ctx, app.SessionActor{
		AccountID: registered.AccountID, SessionID: registered.SessionID, TraceID: "trace-delete",
	}, deleteCode); err != nil {
		t.Fatalf("delete account: %v", err)
	}

	account, err := f.accounts.FindByID(ctx, registered.AccountID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.DeletedAt == nil || account.Status != domain.AccountStatusDisabled {
		t.Fatalf("account must be soft deleted: %+v", account)
	}
	if session := f.sessionOf(t, registered.SessionID); session.Status != domain.SessionStatusRevoked {
		t.Fatalf("sessions must be revoked on deletion: %+v", session)
	}
	requireAppCode(t, mustLogin(f, phone), apperr.CodeUnauthorized)
}

// mustRegister 使用最近一次发送的验证码执行注册。
func mustRegister(f *smsFixture, phone string) error {
	_, err := f.svc.Register(context.Background(), app.SMSLoginInput{
		Phone: phone, Code: f.sender.code, TraceID: "trace-register-dup",
	})
	return err
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
	// 失败必须回滚重发窗口：立即重试应仍是供应商故障（50300），而不是被 42900 锁住。
	requireAppCode(t, f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}), 50300)
}

// TestSMSStoresKeyedDigests 验证手机号与验证码以带密钥摘要存储。
func TestSMSStoresKeyedDigests(t *testing.T) {
	hasher := testHasher(t)
	f := newSMSFixture(t, func(cfg *app.SMSLoginConfig) { cfg.Hasher = hasher })
	ctx := context.Background()
	phone := "+8613800138000"
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
		t.Fatalf("send sms: %v", err)
	}
	phoneHash := app.PhoneRef(hasher, phone)
	stored, err := f.codes.Get(ctx, phoneHash)
	if err != nil {
		t.Fatalf("load stored code: %v", err)
	}
	if !strings.HasPrefix(stored, "v1:") || len(stored) != len("v1:")+64 {
		t.Fatalf("stored code digest = %q, want keyed format", stored)
	}
	if strings.Contains(stored, f.sender.code) {
		t.Fatalf("stored digest must not contain plaintext code")
	}
	if !app.SMSCodeMatches(hasher, stored, phoneHash, f.sender.code) {
		t.Fatalf("stored digest must verify sender code")
	}
}

// TestSMSLoginGlobalDailyBudget 验证全局日发送预算生效。
func TestSMSLoginGlobalDailyBudget(t *testing.T) {
	f := newSMSFixture(t, func(cfg *app.SMSLoginConfig) { cfg.DailyBudget = 1 })
	ctx := context.Background()
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "+8613800138000"}); err != nil {
		t.Fatalf("first send: %v", err)
	}
	requireAppCode(t, f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "+8613800138001"}), 42900)
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
