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

// TestSMSDefaultRegionNormalization 验证本地号码按默认地区补国家码（BIZ-07）。
func TestSMSDefaultRegionNormalization(t *testing.T) {
	ctx := context.Background()
	f := newSMSFixture(t, func(cfg *app.SMSLoginConfig) {
		cfg.DefaultRegion = "CN"
	})
	// 带分隔符的本地号码应被规范化为 +8613800138000。
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "138-0013-8000"}); err != nil {
		t.Fatalf("send local phone: %v", err)
	}
	if _, err := f.svc.Register(ctx, app.SMSLoginInput{
		Phone: "13800138000", Code: f.sender.code, TraceID: "trace-region",
	}); err != nil {
		t.Fatalf("register local phone: %v", err)
	}
	// 规范化后与 E.164 输入必须指向同一账号 / 验证码；推进时钟跨过重发间隔。
	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: "+8613800138000"}); err != nil {
		t.Fatalf("send e164 phone: %v", err)
	}
	if _, err := f.svc.Login(ctx, app.SMSLoginInput{
		Phone: "+8613800138000", Code: f.sender.code, TraceID: "trace-region-login",
	}); err != nil {
		t.Fatalf("login with e164 phone: %v", err)
	}
}

// TestConsumerOpsRejectMissingActor 覆盖 C 端账号操作的鉴权与目标校验。
func TestConsumerOpsRejectMissingActor(t *testing.T) {
	ctx := context.Background()
	f := newSMSFixture(t, nil)
	requireAppCode(t, f.svc.DeleteAccount(ctx, app.SessionActor{}, "123456"), apperr.CodeUnauthorized)
	requireAppCode(t, f.svc.DeleteAccount(ctx, app.SessionActor{AccountID: "missing"}, "123456"), apperr.CodeUnauthorized)
	// 未知账号的换绑同样失败关闭。
	requireAppCode(t, f.svc.Rebind(ctx, app.SMSRebindInput{
		Actor: app.SessionActor{AccountID: "missing"}, Phone: "+8613800138000", Code: "123456", OldCode: "123456",
	}), apperr.CodeUnauthorized)
}

// TestSMSDeleteAccountReauth 验证 C 端注销的再认证分支（BIZ-02 / BIZ-05）。
func TestSMSDeleteAccountReauth(t *testing.T) {
	ctx := context.Background()
	f := newSMSFixture(t, nil)
	const phone = "+8613800138000"
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
		t.Fatalf("send: %v", err)
	}
	registered, err := f.svc.Register(ctx, app.SMSLoginInput{
		Phone: phone, Code: f.sender.code, TraceID: "trace-delete-reg",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	actor := app.SessionActor{AccountID: registered.AccountID, TraceID: "trace-delete"}
	// 无验证码 / 格式错误 → 40000；错误验证码 → 40100。
	requireAppCode(t, f.svc.DeleteAccount(ctx, actor, ""), apperr.CodeBadRequest)
	requireAppCode(t, f.svc.DeleteAccount(ctx, actor, "000000"), apperr.CodeUnauthorized)
	// 正确验证码 → 软删成功。
	f.clock.Advance(61 * time.Second)
	if err := f.svc.Send(ctx, app.SendSMSCodeInput{Phone: phone}); err != nil {
		t.Fatalf("re-send: %v", err)
	}
	if err := f.svc.DeleteAccount(ctx, actor, f.sender.code); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	// OAuth-only 账号（主体不是 phone: 前缀）不能走手机号再认证。
	f2 := newSMSFixture(t, nil)
	oauthAccount := mustSeedOAuthAccount(t, f2)
	requireAppCode(t, f2.svc.DeleteAccount(ctx, app.SessionActor{AccountID: oauthAccount.ID}, "123456"), apperr.CodeConflict)
}

// TestDeleteConsumerAccountRequiresRecentLogin 验证 OAuth-only 账号仅凭 Bearer
// 注销时必须最近登录（8.4 R-8.4-04）。
func TestDeleteConsumerAccountRequiresRecentLogin(t *testing.T) {
	ctx := context.Background()
	f := newSMSFixture(t, nil)
	account := mustSeedOAuthAccount(t, f)

	seedDevice := func(id string, createdAt time.Time) app.SessionActor {
		t.Helper()
		family, err := domain.NewSessionFamily(domain.NewSessionFamilyParams{
			ID: "family-" + id, AccountID: account.ID,
			ExpiresAt: createdAt.Add(app.DefaultSessionAbsoluteTTL), Now: createdAt,
		})
		if err != nil {
			t.Fatalf("new session family: %v", err)
		}
		if err := f.sessions.SaveFamily(ctx, family, 0); err != nil {
			t.Fatalf("save session family: %v", err)
		}
		session, err := domain.NewSession(domain.NewSessionParams{
			ID: "sess-" + id, AccountID: account.ID, FamilyID: family.ID,
			RefreshHash: "refresh-" + id, TokenVersion: account.TokenVersion,
			ExpiresAt: createdAt.Add(15 * time.Minute), Now: createdAt,
		})
		if err != nil {
			t.Fatalf("new session: %v", err)
		}
		if err := f.sessions.Save(ctx, session, 0); err != nil {
			t.Fatalf("save session: %v", err)
		}
		return app.SessionActor{AccountID: account.ID, SessionID: session.ID, TraceID: "trace-delete"}
	}

	// 超过再认证窗口的历史登录：仅凭 Bearer 必须拒绝。
	stale := seedDevice("old", f.clock.Now().Add(-11*time.Minute))
	requireAppCode(t, f.authFixture.svc.DeleteConsumerAccount(ctx, stale), apperr.CodeUnauthorized)

	// 无会话上下文（例如令牌来自已清理会话）同样失败关闭。
	requireAppCode(t, f.authFixture.svc.DeleteConsumerAccount(ctx, app.SessionActor{AccountID: account.ID}),
		apperr.CodeUnauthorized)

	// 会话存在但设备家庭缺失：同样失败关闭。
	orphan, err := domain.NewSession(domain.NewSessionParams{
		ID: "sess-orphan", AccountID: account.ID, FamilyID: "family-missing",
		RefreshHash: "refresh-orphan", TokenVersion: account.TokenVersion,
		ExpiresAt: f.clock.Now().Add(15 * time.Minute), Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new orphan session: %v", err)
	}
	if err := f.sessions.Save(ctx, orphan, 0); err != nil {
		t.Fatalf("save orphan session: %v", err)
	}
	requireAppCode(t, f.authFixture.svc.DeleteConsumerAccount(ctx,
		app.SessionActor{AccountID: account.ID, SessionID: orphan.ID}), apperr.CodeUnauthorized)

	// 设备已被撤销：即使家庭创建时间较新也必须拒绝。
	revokedActor := seedDevice("revoked", f.clock.Now())
	revokedFamily, err := f.sessions.FindFamily(ctx, "family-revoked")
	if err != nil {
		t.Fatalf("load family: %v", err)
	}
	expected := revokedFamily.Version
	if err := revokedFamily.Revoke(domain.RevokeReasonLogout, f.clock.Now()); err != nil {
		t.Fatalf("revoke family: %v", err)
	}
	if err := f.sessions.SaveFamily(ctx, revokedFamily, expected); err != nil {
		t.Fatalf("save revoked family: %v", err)
	}
	requireAppCode(t, f.authFixture.svc.DeleteConsumerAccount(ctx, revokedActor), apperr.CodeUnauthorized)

	// 最近登录：允许注销。
	fresh := seedDevice("new", f.clock.Now())
	if err := f.authFixture.svc.DeleteConsumerAccount(ctx, fresh); err != nil {
		t.Fatalf("recent login must allow deletion: %v", err)
	}
	loaded, err := f.accounts.FindByID(ctx, account.ID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if loaded.DeletedAt == nil {
		t.Fatal("account must be soft deleted")
	}
}

// mustSeedOAuthAccount 写入一个无手机号的 C 端账号。
func mustSeedOAuthAccount(t *testing.T, f *smsFixture) *domain.Account {
	t.Helper()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: "acct-oauth-only", Channel: domain.ChannelC, Subject: "oauth:sub-1", Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new oauth account: %v", err)
	}
	if err := f.accounts.Save(context.Background(), account, 0); err != nil {
		t.Fatalf("save oauth account: %v", err)
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
