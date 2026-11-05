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

// TestOAuthBindingLifecycle 验证绑定、列表与解绑闭环（BIZ-02）。
func TestOAuthBindingLifecycle(t *testing.T) {
	ctx := context.Background()
	f := newOAuthFixture(t, false, &fakeProvider{name: "test", subject: "sub-bind"})
	f.seedAccount(t, "acct-bind", "bind-user", "Bind#Passw0rd")
	actor := app.SessionActor{AccountID: "acct-bind", TraceID: "trace-bind"}

	auth, err := f.svc.AuthorizeBinding(ctx, actor, "test", "", "trace-bind")
	if err != nil {
		t.Fatalf("authorize binding: %v", err)
	}
	if err := f.svc.CompleteBinding(ctx, actor, app.OAuthCallbackInput{
		Provider: "test", Code: "code-1", State: auth.State, TraceID: "trace-bind-cb",
	}); err != nil {
		t.Fatalf("complete binding: %v", err)
	}
	bindings, err := f.svc.ListBindings(ctx, actor)
	if err != nil || len(bindings) != 1 || bindings[0].Provider != "test" {
		t.Fatalf("bindings = %+v, err=%v", bindings, err)
	}
	if err := f.svc.Unbind(ctx, actor, "test"); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	bindings, err = f.svc.ListBindings(ctx, actor)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("bindings after unbind = %+v, err=%v", bindings, err)
	}
}

// TestOAuthUnbindRejectsLastCredential 验证 OAuth-only 账号不允许解绑最后一个凭据（BIZ-02）。
func TestOAuthUnbindRejectsLastCredential(t *testing.T) {
	f := newOAuthFixture(t, false, &fakeProvider{name: "test", subject: "sub-1"})
	account := f.seedMappedAccount(t, "test", "sub-1")
	err := f.svc.Unbind(context.Background(), app.SessionActor{AccountID: account.ID}, "test")
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("unbind last credential error = %v, want conflict", err)
	}
}

// TestOAuthBindingRejectsCrossAccountState 验证绑定 state 不能跨账号使用（BIZ-02）。
func TestOAuthBindingRejectsCrossAccountState(t *testing.T) {
	ctx := context.Background()
	f := newOAuthFixture(t, false, &fakeProvider{name: "test", subject: "sub-bind"})
	f.seedAccount(t, "acct-a", "user-a", "UserA#Passw0rd")
	f.seedAccount(t, "acct-b", "user-b", "UserB#Passw0rd")
	auth, err := f.svc.AuthorizeBinding(ctx, app.SessionActor{AccountID: "acct-a"}, "test", "", "trace-a")
	if err != nil {
		t.Fatalf("authorize binding: %v", err)
	}
	if err := f.svc.CompleteBinding(ctx, app.SessionActor{AccountID: "acct-b"}, app.OAuthCallbackInput{
		Provider: "test", Code: "code-1", State: auth.State,
	}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("cross-account binding error = %v, want unauthorized", err)
	}
}

// TestOAuthBindingRejectsDuplicate 验证同一账号重复绑定同一主体返回冲突（BIZ-02）。
func TestOAuthBindingRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	f := newOAuthFixture(t, false, &fakeProvider{name: "test", subject: "sub-dup"})
	f.seedAccount(t, "acct-dup", "dup-user", "Dup#Passw0rd")
	actor := app.SessionActor{AccountID: "acct-dup"}
	first, err := f.svc.AuthorizeBinding(ctx, actor, "test", "", "trace-1")
	if err != nil {
		t.Fatalf("authorize first: %v", err)
	}
	if err := f.svc.CompleteBinding(ctx, actor, app.OAuthCallbackInput{
		Provider: "test", Code: "code-1", State: first.State,
	}); err != nil {
		t.Fatalf("first binding: %v", err)
	}
	second, err := f.svc.AuthorizeBinding(ctx, actor, "test", "", "trace-2")
	if err != nil {
		t.Fatalf("authorize second: %v", err)
	}
	if err := f.svc.CompleteBinding(ctx, actor, app.OAuthCallbackInput{
		Provider: "test", Code: "code-2", State: second.State,
	}); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("duplicate binding error = %v, want conflict", err)
	}
}

// TestOAuthBindingRejectsInvalidRequests 覆盖绑定 / 解绑的参数与鉴权错误分支。
func TestOAuthBindingRejectsInvalidRequests(t *testing.T) {
	ctx := context.Background()
	f := newOAuthFixture(t, false, &fakeProvider{name: "test", subject: "sub-1"})
	unbound := app.SessionActor{AccountID: "missing-account"}
	if _, err := f.svc.ListBindings(ctx, app.SessionActor{}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("list bindings without actor = %v", err)
	}
	if err := f.svc.Unbind(ctx, unbound, "test"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("unbind unknown binding = %v", err)
	}
	if err := f.svc.Unbind(ctx, app.SessionActor{AccountID: "acct-x"}, " "); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("unbind empty provider = %v", err)
	}
	if _, err := f.svc.AuthorizeBinding(ctx, unbound, "missing", "", "trace"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("authorize unknown provider = %v", err)
	}
}

// TestOAuthProviderBreakerOpensAfterFailures 验证 provider 连续失败后熔断（PERF-06）。
func TestOAuthProviderBreakerOpensAfterFailures(t *testing.T) {
	ctx := context.Background()
	provider := &fakeProvider{name: "test", subject: "sub-1", err: apperr.Unavailable("provider down")}
	f := newOAuthFixture(t, true, provider)
	for i := 0; i < 5; i++ {
		authorize, err := f.svc.Authorize(ctx, "test")
		if err != nil {
			t.Fatalf("authorize %d: %v", i, err)
		}
		_, err = f.svc.Callback(ctx, app.OAuthCallbackInput{Provider: "test", Code: "code", State: authorize.State})
		if err == nil {
			t.Fatalf("callback %d must fail", i)
		}
	}
	authorize, err := f.svc.Authorize(ctx, "test")
	if err != nil {
		t.Fatalf("authorize after breaker: %v", err)
	}
	_, err = f.svc.Callback(ctx, app.OAuthCallbackInput{Provider: "test", Code: "code", State: authorize.State})
	if err == nil || !strings.Contains(err.Error(), "temporarily unavailable") {
		t.Fatalf("open breaker error = %v, want temporarily unavailable", err)
	}
	requireAppCode(t, err, apperr.CodeServiceUnavailable)
}

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

// fakeOIDCProvider 记录 nonce 并模拟 IdP 的 nonce 校验。
type fakeOIDCProvider struct {
	fakeProvider
	nonce              string
	exchangedNonce     string
	plainAuthCalled    bool
	plainExchangeTaken bool
}

func (p *fakeOIDCProvider) OIDCEnabled() bool { return true }

func (p *fakeOIDCProvider) AuthorizationURLWithNonce(state, _, nonce string) (string, error) {
	p.nonce = nonce
	return "https://idp.example.com/authorize?state=" + state + "&nonce=" + nonce, nil
}

func (p *fakeOIDCProvider) ExchangeWithNonce(_ context.Context, code, codeVerifier, nonce string) (app.OAuthIdentityInfo, error) {
	if code == "" || codeVerifier == "" || nonce != p.nonce {
		return app.OAuthIdentityInfo{}, apperr.Unauthorized("invalid oauth authorization")
	}
	p.exchangedNonce = nonce
	return app.OAuthIdentityInfo{Subject: p.subject}, nil
}

func (p *fakeOIDCProvider) AuthorizationURL(state, codeVerifier string) (string, error) {
	p.plainAuthCalled = true
	return p.fakeProvider.AuthorizationURL(state, codeVerifier)
}

func (p *fakeOIDCProvider) Exchange(ctx context.Context, code, codeVerifier string) (app.OAuthIdentityInfo, error) {
	p.plainExchangeTaken = true
	return p.fakeProvider.Exchange(ctx, code, codeVerifier)
}

// TestOIDCAuthorizePropagatesNonce 验证服务在授权发起时生成 nonce、持久化并在回调中回传校验。
func TestOIDCAuthorizePropagatesNonce(t *testing.T) {
	ctx := context.Background()
	base := newAuthFixture(t, domain.ChannelC)
	provider := &fakeOIDCProvider{fakeProvider: fakeProvider{name: "oidc", subject: "sub-1"}}
	svc, err := app.NewOAuthService(app.OAuthServiceConfig{
		Providers:     map[string]app.OAuthProvider{"oidc": provider},
		States:        cache.NewMemoryOAuthStateStore(base.clock),
		Identities:    persistence.NewMemoryOAuthIdentityRepository(),
		Accounts:      base.accounts,
		IDs:           base.ids,
		Auth:          base.svc,
		AutoProvision: true,
		StateTTL:      10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("new oauth service: %v", err)
	}

	authorized, err := svc.Authorize(ctx, "oidc")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if provider.nonce == "" || !strings.Contains(authorized.AuthorizeURL, "nonce="+provider.nonce) {
		t.Fatalf("nonce not propagated: nonce=%q url=%q", provider.nonce, authorized.AuthorizeURL)
	}
	if provider.plainAuthCalled {
		t.Fatal("OIDC provider must use AuthorizationURLWithNonce")
	}

	if _, err := svc.Callback(ctx, app.OAuthCallbackInput{Provider: "oidc", Code: "code-1", State: authorized.State}); err != nil {
		t.Fatalf("callback: %v", err)
	}
	if provider.exchangedNonce != provider.nonce {
		t.Fatalf("callback nonce = %q, want %q", provider.exchangedNonce, provider.nonce)
	}
	if provider.plainExchangeTaken {
		t.Fatal("OIDC provider must use ExchangeWithNonce")
	}
}
