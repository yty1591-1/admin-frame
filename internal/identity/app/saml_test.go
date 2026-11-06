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

// fakeSAMLProvider 是可控的 SAML IdP 适配器。
type fakeSAMLProvider struct {
	name        string
	subject     string
	assertionID string
	err         error
	lastRequest string
}

// Name 返回提供方名。
func (p *fakeSAMLProvider) Name() string { return p.name }

// AuthorizationURL 记录 AuthnRequest ID 并返回带 RelayState 的跳转地址。
func (p *fakeSAMLProvider) AuthorizationURL(requestID, relayState string) (string, error) {
	p.lastRequest = requestID
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(relayState) == "" {
		return "", errors.New("missing request id or relay state")
	}
	return "https://idp.example.com/sso?SAMLRequest=" + requestID + "&RelayState=" + relayState, nil
}

// ParseResponse 返回预设断言主体或错误。
func (p *fakeSAMLProvider) ParseResponse(_ context.Context, samlResponse, expectedRequestID string) (app.SAMLIdentity, error) {
	if strings.TrimSpace(samlResponse) == "" {
		return app.SAMLIdentity{}, apperr.Unauthorized("invalid saml assertion")
	}
	if expectedRequestID != p.lastRequest {
		return app.SAMLIdentity{}, apperr.Unauthorized("invalid saml assertion")
	}
	if p.err != nil {
		return app.SAMLIdentity{}, p.err
	}
	return app.SAMLIdentity{Subject: p.subject, AssertionID: p.assertionID, Issuer: "idp"}, nil
}

// samlFixture 聚合 SAML 用例依赖。
type samlFixture struct {
	*authFixture
	svc        *app.SAMLService
	provider   *fakeSAMLProvider
	states     *cache.MemoryOAuthStateStore
	identities *persistence.MemoryOAuthIdentityRepository
	tickets    *cache.MemorySAMLTicketStore
}

// newSAMLFixture 构造 SAML 服务；provider 名称固定为 test。
func newSAMLFixture(t *testing.T, autoProvision bool, provider *fakeSAMLProvider) *samlFixture {
	t.Helper()
	return buildSAMLFixture(t, samlFixtureOptions{autoProvision: autoProvision, provider: provider})
}

// samlFixtureOptions 允许测试注入自定义重放存储与限流器。
type samlFixtureOptions struct {
	autoProvision bool
	provider      *fakeSAMLProvider
	replay        app.SAMLReplayStore
	limiter       app.LoginLimiter
	metadata      []byte
}

// buildSAMLFixture 按选项构造 SAML 服务。
func buildSAMLFixture(t *testing.T, opts samlFixtureOptions) *samlFixture {
	t.Helper()
	base := newAuthFixture(t, domain.ChannelC)
	provider := opts.provider
	if provider == nil {
		provider = &fakeSAMLProvider{
			name: "test", subject: "sub-1", assertionID: "_assertion-1",
		}
	}
	states := cache.NewMemoryOAuthStateStore(base.clock)
	identities := persistence.NewMemoryOAuthIdentityRepository()
	var replay app.SAMLReplayStore = cache.NewMemorySAMLReplayStore(base.clock)
	if opts.replay != nil {
		replay = opts.replay
	}
	tickets := cache.NewMemorySAMLTicketStore(base.clock)
	metadata := opts.metadata
	if metadata == nil {
		metadata = []byte("<metadata/>")
	}
	svc, err := app.NewSAMLService(app.SAMLServiceConfig{
		Providers:     map[string]app.SAMLProvider{"test": provider},
		States:        states,
		Identities:    identities,
		Accounts:      base.accounts,
		IDs:           base.ids,
		Auth:          base.svc,
		AutoProvision: opts.autoProvision,
		StateTTL:      10 * time.Minute,
		ReplayTTL:     10 * time.Minute,
		TicketTTL:     time.Minute,
		Replay:        replay,
		Tickets:       tickets,
		Limiter:       opts.limiter,
		SPMetadataXML: metadata,
	})
	if err != nil {
		t.Fatalf("new saml service: %v", err)
	}
	return &samlFixture{
		authFixture: base, svc: svc, provider: provider, states: states, identities: identities, tickets: tickets,
	}
}

// failingReplay 模拟重放存储不可用。
type failingReplay struct{}

// Reserve 实现 app.SAMLReplayStore。
func (failingReplay) Reserve(context.Context, string, string, time.Duration) error {
	return apperr.Unavailable("saml replay store is down")
}

// seedSAMLAccount 写入 C 端账号与 saml:test 外部身份映射。
func (f *samlFixture) seedSAMLAccount(t *testing.T, subject string) *domain.Account {
	t.Helper()
	ctx := context.Background()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: "acct-saml", Channel: domain.ChannelC, Subject: "saml-user@example.com", Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := f.accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID: "saml-id-1", AccountID: account.ID, Provider: "saml:test", ProviderSubject: subject,
	})
	if err != nil {
		t.Fatalf("new saml identity: %v", err)
	}
	if err := f.identities.Save(ctx, identity); err != nil {
		t.Fatalf("save saml identity: %v", err)
	}
	return account
}

// TestNewSAMLServiceRequiresProvider 校验至少一个 provider 与依赖完整。
func TestNewSAMLServiceRequiresProvider(t *testing.T) {
	t.Parallel()
	_, err := app.NewSAMLService(app.SAMLServiceConfig{})
	if err == nil {
		t.Fatalf("expected error for empty config")
	}
}

// TestSAMLAuthorizeSavesOneTimeState 校验 state 与 AuthnRequest ID 写入存储。
func TestSAMLAuthorizeSavesOneTimeState(t *testing.T) {
	t.Parallel()
	f := newSAMLFixture(t, false, nil)
	result, err := f.svc.AuthorizeWithClient(context.Background(), "test", "ip-hash", "trace-1")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if result.State == "" || !strings.Contains(result.AuthorizeURL, result.State) {
		t.Fatalf("unexpected authorize result: %+v", result)
	}
	saved, err := f.states.Consume(context.Background(), result.State)
	if err != nil {
		t.Fatalf("consume state: %v", err)
	}
	if saved.Provider != "test" || saved.RequestID == "" || !strings.HasPrefix(saved.RequestID, "_") {
		t.Fatalf("unexpected state: %+v", saved)
	}
	// state 一次性：再次消费必须失败。
	if _, err := f.states.Consume(context.Background(), result.State); err == nil {
		t.Fatalf("expected state replay to fail")
	}
}

// TestSAMLACSRejectsReplayedAssertion 校验同一断言 ID 只能兑换一次。
func TestSAMLACSRejectsReplayedAssertion(t *testing.T) {
	t.Parallel()
	f := newSAMLFixture(t, false, nil)
	f.seedSAMLAccount(t, "sub-1")

	first := f.authorize(t)
	ticket, err := f.svc.ACS(context.Background(), app.SAMLACSInput{
		SAMLResponse: "signed-assertion", RelayState: first, TraceID: "trace-acs",
	})
	if err != nil {
		t.Fatalf("first acs: %v", err)
	}
	if ticket == "" {
		t.Fatalf("expected ticket")
	}
	second := f.authorize(t)
	_, err = f.svc.ACS(context.Background(), app.SAMLACSInput{
		SAMLResponse: "signed-assertion", RelayState: second, TraceID: "trace-acs-2",
	})
	if !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("expected unauthorized on replay, got %v", err)
	}
}

// TestSAMLACSRejectsUnknownState 校验 state 无效统一 40100。
func TestSAMLACSRejectsUnknownState(t *testing.T) {
	t.Parallel()
	f := newSAMLFixture(t, false, nil)
	_, err := f.svc.ACS(context.Background(), app.SAMLACSInput{
		SAMLResponse: "signed-assertion", RelayState: "missing-state",
	})
	if !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}

// TestSAMLACSRejectsMissingParameters 校验缺少 SAMLResponse / RelayState 返回 40000。
func TestSAMLACSRejectsMissingParameters(t *testing.T) {
	t.Parallel()
	f := newSAMLFixture(t, false, nil)
	_, err := f.svc.ACS(context.Background(), app.SAMLACSInput{SAMLResponse: "signed-assertion"})
	if !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("expected bad request for missing relay state, got %v", err)
	}
	_, err = f.svc.ACS(context.Background(), app.SAMLACSInput{RelayState: "state"})
	if !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("expected bad request for missing saml response, got %v", err)
	}
}

// TestSAMLExchangeConsumesTicketOnce 校验 ticket 一次性并签发令牌。
func TestSAMLExchangeConsumesTicketOnce(t *testing.T) {
	t.Parallel()
	f := newSAMLFixture(t, false, nil)
	f.seedSAMLAccount(t, "sub-1")

	ticket, err := f.svc.ACS(context.Background(), app.SAMLACSInput{
		SAMLResponse: "signed-assertion", RelayState: f.authorize(t), TraceID: "trace-acs",
	})
	if err != nil {
		t.Fatalf("acs: %v", err)
	}
	result, err := f.svc.Exchange(context.Background(), ticket, domain.DeviceInfo{})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if result.AccessToken == "" || result.RefreshToken == "" {
		t.Fatalf("expected token pair")
	}
	if _, err := f.svc.Exchange(context.Background(), ticket, domain.DeviceInfo{}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("expected ticket replay to fail, got %v", err)
	}
}

// TestSAMLACSRejectsProviderMismatch 校验 RelayState provider 与入参不一致时拒绝。
func TestSAMLACSRejectsProviderMismatch(t *testing.T) {
	t.Parallel()
	f := newSAMLFixture(t, false, nil)
	f.seedSAMLAccount(t, "sub-1")
	_, err := f.svc.ACS(context.Background(), app.SAMLACSInput{
		Provider:     "other",
		SAMLResponse: "signed-assertion",
		RelayState:   f.authorize(t),
	})
	if !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}

// TestSAMLServiceExposesProvidersAndMetadata 校验 provider 列表与元数据读取。
func TestSAMLServiceExposesProvidersAndMetadata(t *testing.T) {
	t.Parallel()
	f := buildSAMLFixture(t, samlFixtureOptions{metadata: []byte("<EntityDescriptor/>")})
	names := f.svc.ProviderNames()
	if len(names) != 1 || names[0] != "test" {
		t.Fatalf("unexpected providers: %v", names)
	}
	if !f.svc.HasProvider("test") || f.svc.HasProvider("missing") {
		t.Fatalf("HasProvider mismatch")
	}
	raw, err := f.svc.SPMetadata()
	if err != nil || string(raw) != "<EntityDescriptor/>" {
		t.Fatalf("metadata = %q err=%v", raw, err)
	}
}

// TestSAMLAuthorizeRejectsUnknownProvider 校验未配置 provider 返回 40400。
func TestSAMLAuthorizeRejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	f := newSAMLFixture(t, false, nil)
	_, err := f.svc.AuthorizeWithClient(context.Background(), "missing", "ip", "trace")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

// TestSAMLAuthorizeRateLimited 校验限流拒绝返回 42900。
func TestSAMLAuthorizeRateLimited(t *testing.T) {
	t.Parallel()
	f := buildSAMLFixture(t, samlFixtureOptions{limiter: denyLimiter{retryAfter: time.Second}})
	_, err := f.svc.AuthorizeWithClient(context.Background(), "test", "ip", "trace")
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("expected rate limited, got %v", err)
	}
}

// TestSAMLACSFailsClosedWhenReplayStoreDown 校验重放存储故障返回 50300。
func TestSAMLACSFailsClosedWhenReplayStoreDown(t *testing.T) {
	t.Parallel()
	f := buildSAMLFixture(t, samlFixtureOptions{replay: failingReplay{}})
	f.seedSAMLAccount(t, "sub-1")
	_, err := f.svc.ACS(context.Background(), app.SAMLACSInput{
		SAMLResponse: "signed-assertion", RelayState: f.authorize(t), TraceID: "trace",
	})
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

// TestSAMLACSRejectsUnmappedSubject 校验默认不自动建号。
func TestSAMLACSRejectsUnmappedSubject(t *testing.T) {
	t.Parallel()
	f := newSAMLFixture(t, false, nil)
	_, err := f.svc.ACS(context.Background(), app.SAMLACSInput{
		SAMLResponse: "signed-assertion", RelayState: f.authorize(t), TraceID: "trace",
	})
	if !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}

// TestSAMLACSAutoProvisionsAccount 校验 auto_provision 开启时建号并完成兑换。
func TestSAMLACSAutoProvisionsAccount(t *testing.T) {
	t.Parallel()
	f := newSAMLFixture(t, true, nil)
	ticket, err := f.svc.ACS(context.Background(), app.SAMLACSInput{
		SAMLResponse: "signed-assertion", RelayState: f.authorize(t), TraceID: "trace",
	})
	if err != nil {
		t.Fatalf("acs auto provision: %v", err)
	}
	result, err := f.svc.Exchange(context.Background(), ticket, domain.DeviceInfo{})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if result.AccountID == "" {
		t.Fatalf("expected provisioned account id")
	}
}

// TestSAMLExchangeRejectsUnknownTicket 校验未知 ticket 返回 40100。
func TestSAMLExchangeRejectsUnknownTicket(t *testing.T) {
	t.Parallel()
	f := newSAMLFixture(t, false, nil)
	if _, err := f.svc.Exchange(context.Background(), "missing", domain.DeviceInfo{}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}

// authorize 发起一次授权并返回 state。
func (f *samlFixture) authorize(t *testing.T) string {
	t.Helper()
	result, err := f.svc.AuthorizeWithClient(context.Background(), "test", "ip-hash", "trace-authorize")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	return result.State
}
