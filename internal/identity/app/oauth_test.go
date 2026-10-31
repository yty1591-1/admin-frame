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
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// fakeProvider 是可控的 OAuth 提供方。
type fakeProvider struct {
	name    string
	subject string
	err     error
}

// Name 返回提供方名。
func (p *fakeProvider) Name() string { return p.name }

// AuthorizationURL 返回带 state 的授权地址。
func (p *fakeProvider) AuthorizationURL(state, codeVerifier string) (string, error) {
	if codeVerifier == "" {
		return "", errors.New("missing verifier")
	}
	return "https://idp.example.com/authorize?state=" + state + "&code_challenge=" + codeVerifier, nil
}

// Exchange 返回预设主体或错误。
func (p *fakeProvider) Exchange(_ context.Context, code, codeVerifier string) (app.OAuthIdentityInfo, error) {
	if code == "" || codeVerifier == "" {
		return app.OAuthIdentityInfo{}, errors.New("missing code or verifier")
	}
	if p.err != nil {
		return app.OAuthIdentityInfo{}, p.err
	}
	return app.OAuthIdentityInfo{Subject: p.subject}, nil
}

// oauthFixture 聚合 OAuth 登录用例依赖。
type oauthFixture struct {
	*authFixture
	svc        *app.OAuthService
	provider   *fakeProvider
	states     *cache.MemoryOAuthStateStore
	identities *persistence.MemoryOAuthIdentityRepository
}

// newOAuthFixture 构造 OAuth 服务；provider 名称固定为 test。
func newOAuthFixture(t *testing.T, autoProvision bool, provider *fakeProvider, opts ...withAuthConfig) *oauthFixture {
	t.Helper()
	return newOAuthFixtureWithLimiter(t, autoProvision, provider, nil, opts...)
}

// newOAuthFixtureWithLimiter 构造带限流器的 OAuth 服务（IDN-P2-05）。
func newOAuthFixtureWithLimiter(
	t *testing.T,
	autoProvision bool,
	provider *fakeProvider,
	limiter app.LoginLimiter,
	opts ...withAuthConfig,
) *oauthFixture {
	t.Helper()
	base := newAuthFixture(t, domain.ChannelC, opts...)
	if provider == nil {
		provider = &fakeProvider{name: "test", subject: "sub-1"}
	}
	states := cache.NewMemoryOAuthStateStore(base.clock)
	identities := persistence.NewMemoryOAuthIdentityRepository()
	svc, err := app.NewOAuthService(app.OAuthServiceConfig{
		Providers:     map[string]app.OAuthProvider{"test": provider},
		States:        states,
		Identities:    identities,
		Accounts:      base.accounts,
		IDs:           base.ids,
		Auth:          base.svc,
		AutoProvision: autoProvision,
		StateTTL:      10 * time.Minute,
		Limiter:       limiter,
	})
	if err != nil {
		t.Fatalf("new oauth service: %v", err)
	}
	return &oauthFixture{authFixture: base, svc: svc, provider: provider, states: states, identities: identities}
}

// seedMappedAccount 写入 C 端账号与外部身份映射。
func (f *oauthFixture) seedMappedAccount(t *testing.T, providerName, subject string) *domain.Account {
	t.Helper()
	ctx := context.Background()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: "acct-oauth", Channel: domain.ChannelC, Subject: "oauth-user@example.com", Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := f.accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID: "oauth-id-1", AccountID: account.ID, Provider: providerName, ProviderSubject: subject,
	})
	if err != nil {
		t.Fatalf("new oauth identity: %v", err)
	}
	if err := f.identities.Save(ctx, identity); err != nil {
		t.Fatalf("save oauth identity: %v", err)
	}
	return account
}

// denyLimiter 始终拒绝，用于验证 OAuth 限流生效。
type denyLimiter struct{ retryAfter time.Duration }

// Allow 返回拒绝与建议等待时长。
func (l denyLimiter) Allow(string) (bool, time.Duration) { return false, l.retryAfter }

// TestOAuthRateLimit 验证 authorize / callback 超限返回 42900（IDN-P2-05）。
func TestOAuthRateLimit(t *testing.T) {
	ctx := context.Background()
	limiter := denyLimiter{retryAfter: 2 * time.Second}

	t.Run("authorize", func(t *testing.T) {
		f := newOAuthFixtureWithLimiter(t, false, nil, limiter)
		_, err := f.svc.AuthorizeWithClient(ctx, "test", "ip-hash", "")
		requireAppCode(t, err, apperr.CodeTooManyRequests)
		var limited *app.RateLimitedError
		if !errors.As(err, &limited) || limited.RetryAfter != limiter.retryAfter {
			t.Fatalf("err=%v want retry-after %s", err, limiter.retryAfter)
		}
	})

	t.Run("callback", func(t *testing.T) {
		f := newOAuthFixtureWithLimiter(t, false, nil, limiter)
		_, err := f.svc.Callback(ctx, app.OAuthCallbackInput{
			Provider: "test", Code: "code-1", State: "state-1", IPHash: "ip-hash",
		})
		requireAppCode(t, err, apperr.CodeTooManyRequests)
	})
}

func TestOAuthAuthorize(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		f := newOAuthFixture(t, false, nil)
		result, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		if result.State == "" || !strings.Contains(result.AuthorizeURL, result.State) {
			t.Fatalf("unexpected authorize result: %+v", result)
		}
		if names := f.svc.ProviderNames(); len(names) != 1 || names[0] != "test" {
			t.Fatalf("provider names = %v", names)
		}
	})

	t.Run("unknown_provider", func(t *testing.T) {
		f := newOAuthFixture(t, false, nil)
		_, err := f.svc.Authorize(ctx, "unknown")
		requireAppCode(t, err, 40400)
	})
}

func TestOAuthCallback(t *testing.T) {
	ctx := context.Background()

	t.Run("mapped_account", func(t *testing.T) {
		f := newOAuthFixture(t, false, nil)
		account := f.seedMappedAccount(t, "test", "sub-1")
		authorized, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		result, err := f.svc.Callback(ctx, app.OAuthCallbackInput{
			Provider: "test", Code: "code-1", State: authorized.State, TraceID: "trace-oauth",
		})
		if err != nil {
			t.Fatalf("callback: %v", err)
		}
		if result.AccessToken == "" || result.AccountID != account.ID {
			t.Fatalf("unexpected callback result: %+v", result)
		}
	})

	t.Run("state_replay", func(t *testing.T) {
		f := newOAuthFixture(t, false, nil)
		f.seedMappedAccount(t, "test", "sub-1")
		authorized, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		in := app.OAuthCallbackInput{Provider: "test", Code: "code-1", State: authorized.State}
		if _, err := f.svc.Callback(ctx, in); err != nil {
			t.Fatalf("first callback: %v", err)
		}
		_, err = f.svc.Callback(ctx, in)
		requireAppCode(t, err, 40100)
	})

	t.Run("provider_mismatch", func(t *testing.T) {
		f := newOAuthFixture(t, false, nil)
		authorized, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		_, err = f.svc.Callback(ctx, app.OAuthCallbackInput{Provider: "other", Code: "code-1", State: authorized.State})
		requireAppCode(t, err, 40100)
	})

	t.Run("exchange_unauthorized", func(t *testing.T) {
		f := newOAuthFixture(t, false, &fakeProvider{name: "test", err: apperr.Unauthorized("invalid")})
		authorized, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		_, err = f.svc.Callback(ctx, app.OAuthCallbackInput{Provider: "test", Code: "code-1", State: authorized.State})
		requireAppCode(t, err, 40100)
	})

	t.Run("exchange_unavailable", func(t *testing.T) {
		f := newOAuthFixture(t, false, &fakeProvider{name: "test", err: apperr.Unavailable("down")})
		authorized, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		_, err = f.svc.Callback(ctx, app.OAuthCallbackInput{Provider: "test", Code: "code-1", State: authorized.State})
		requireAppCode(t, err, 50300)
	})

	t.Run("unmapped_rejected_without_auto_provision", func(t *testing.T) {
		f := newOAuthFixture(t, false, nil)
		authorized, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		_, err = f.svc.Callback(ctx, app.OAuthCallbackInput{Provider: "test", Code: "code-1", State: authorized.State})
		requireAppCode(t, err, 40100)
	})

	t.Run("auto_provision_creates_account", func(t *testing.T) {
		f := newOAuthFixture(t, true, nil)
		authorized, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		result, err := f.svc.Callback(ctx, app.OAuthCallbackInput{Provider: "test", Code: "code-1", State: authorized.State})
		if err != nil {
			t.Fatalf("callback: %v", err)
		}
		if result.AccessToken == "" || result.AccountID == "" {
			t.Fatalf("unexpected callback result: %+v", result)
		}
		account, err := f.accounts.FindByID(ctx, result.AccountID)
		if err != nil {
			t.Fatalf("load account: %v", err)
		}
		if !strings.HasPrefix(account.Subject, "oauth:test:") {
			t.Fatalf("subject = %q, want oauth:test: prefix", account.Subject)
		}
		if _, err := f.identities.FindByProviderSubject(ctx, "test", "sub-1"); err != nil {
			t.Fatalf("oauth identity mapping: %v", err)
		}
	})

	t.Run("auto_provision_audits_account_created", func(t *testing.T) {
		capture := &captureEmitter{}
		f := newOAuthFixture(t, true, nil, withCapture(capture))
		authorized, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		if _, err := f.svc.Callback(ctx, app.OAuthCallbackInput{
			Provider: "test", Code: "code-1", State: authorized.State, TraceID: "trace-oauth-audit",
		}); err != nil {
			t.Fatalf("callback: %v", err)
		}

		created := capture.byAction(audit.ActionAuthAccountCreated)
		if len(created) != 1 {
			t.Fatalf("expected exactly one auth.account.created, got %+v", created)
		}
		if created[0].ActorType != "system" || created[0].MerchantID != "" {
			t.Fatalf("unexpected account.created event: %+v", created[0])
		}
		if created[0].Attrs["provider"] != "test" {
			t.Fatalf("provider attr = %q, want test", created[0].Attrs["provider"])
		}
		// 审计不得记录外部主体原文。
		if strings.Contains(capture.dump(t), "sub-1") {
			t.Fatalf("audit dump leaks provider subject: %s", capture.dump(t))
		}
	})

	t.Run("disabled_account", func(t *testing.T) {
		f := newOAuthFixture(t, false, nil)
		account := f.seedMappedAccount(t, "test", "sub-1")
		if err := account.Activate(f.clock.Now()); err != nil {
			t.Fatalf("activate: %v", err)
		}
		if err := f.accounts.Save(ctx, account, account.Version-1); err != nil {
			t.Fatalf("save active account: %v", err)
		}
		if err := account.Disable(f.clock.Now()); err != nil {
			t.Fatalf("disable: %v", err)
		}
		if err := f.accounts.Save(ctx, account, account.Version-1); err != nil {
			t.Fatalf("save disabled account: %v", err)
		}

		authorized, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		_, err = f.svc.Callback(ctx, app.OAuthCallbackInput{Provider: "test", Code: "code-1", State: authorized.State})
		requireAppCode(t, err, 40100)
	})
}
