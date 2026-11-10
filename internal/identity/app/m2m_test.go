package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// m2mClock 是可推进的测试时钟。
type m2mClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now 返回当前测试时间。
func (c *m2mClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 推进测试时间。
func (c *m2mClock) Advance(delta time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(delta)
}

// m2mFixture 是 M2M 用例的测试装配。
type m2mFixture struct {
	svc     *app.M2MService
	signer  *jwt.Manager
	clock   *m2mClock
	clients domain.M2MClientRepository
	store   *cache.MemoryRevocationStore
}

// newM2MFixture 构造基于内存仓储的 M2M 服务。
func newM2MFixture(t *testing.T) *m2mFixture {
	t.Helper()
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "https://identity.example.com",
		Audience: []string{"admin"},
		Secret:   []byte("01234567890123456789012345678901"),
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	clock := &m2mClock{now: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}
	tokens, err := app.NewTokenService(signer, domain.ChannelAdmin, 15*time.Minute, 168*time.Hour, &seqIDs{},
		app.WithTokenParser(signer), app.WithClientTokenTTL(15*time.Minute), app.WithTokenClock(clock))
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	clients, secrets := persistence.NewMemoryM2MRepositories()
	store := cache.NewMemoryRevocationStore()
	svc, err := app.NewM2MService(app.M2MServiceConfig{
		Clients:            clients,
		Secrets:            secrets,
		Tokens:             tokens,
		IDs:                &seqIDs{},
		ClientStates:       store,
		RevocationStateTTL: 30 * time.Second,
		Channel:            domain.ChannelAdmin,
		TokenTTL:           15 * time.Minute,
		RotateGrace:        time.Hour,
		MaxSecretAge:       2160 * time.Hour,
		AllowedScopes:      []string{"order:read", "order:write"},
		Service:            "identity-test",
		Clock:              clock,
	})
	if err != nil {
		t.Fatalf("new m2m service: %v", err)
	}
	return &m2mFixture{svc: svc, signer: signer, clock: clock, clients: clients, store: store}
}

// createClient 创建一个只允许 order:read 的客户端。
func (f *m2mFixture) createClient(t *testing.T, scopes []string) *app.CreatedM2MClient {
	t.Helper()
	created, err := f.svc.CreateClient(context.Background(), app.CreateM2MClientInput{
		Name:     "billing-worker",
		Owner:    "platform",
		Channels: []domain.Channel{domain.ChannelAdmin},
		Scopes:   scopes,
		ActorID:  "1",
		TraceID:  "trace-create",
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	return created
}

// TestM2MIssueTokenClaims 验证 client_credentials 签发成功与声明约束（§21.2.3）。
func TestM2MIssueTokenClaims(t *testing.T) {
	fixture := newM2MFixture(t)
	created := fixture.createClient(t, []string{"order:read"})
	if created.ClientSecret == "" || len(created.Client.ClientID) != len("svc_")+22 {
		t.Fatalf("unexpected credentials: len(client_id)=%d secret_empty=%v",
			len(created.Client.ClientID), created.ClientSecret == "")
	}

	token, err := fixture.svc.IssueToken(context.Background(), app.IssueTokenInput{
		ClientID:     created.Client.ClientID,
		ClientSecret: created.ClientSecret,
		Scope:        "order:read",
		TraceID:      "trace-issue",
	})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if token.ExpiresIn != int64((15 * time.Minute).Seconds()) {
		t.Fatalf("expires_in = %d, want 900", token.ExpiresIn)
	}
	claims, err := fixture.signer.Parse(token.AccessToken)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if claims.Subject != app.ClientSubject(created.Client.ClientID) {
		t.Fatalf("sub = %q, want client:%s", claims.Subject, created.Client.ClientID)
	}
	if got := claims.GetString("token_use"); got != app.TokenUseClient {
		t.Fatalf("token_use = %q, want client", got)
	}
	if got := claims.GetString("client_id"); got != created.Client.ClientID {
		t.Fatalf("client_id = %q, want %q", got, created.Client.ClientID)
	}
	if got := claims.GetString("scope"); got != "order:read" {
		t.Fatalf("scope = %q, want order:read", got)
	}
	if got := claims.GetStringSlice("roles"); len(got) != 0 {
		t.Fatalf("roles must be absent on client tokens, got %v", got)
	}
	if claims.GetString("merchant_id") != "" || claims.GetString("sid") != "" {
		t.Fatalf("client token must not carry merchant_id / sid")
	}
}

// TestM2MScopeEscalationRejected 验证越权 scope 返回 invalid_scope 且不签发（AC-P16-M2M-2）。
func TestM2MScopeEscalationRejected(t *testing.T) {
	fixture := newM2MFixture(t)
	created := fixture.createClient(t, []string{"order:read"})

	_, err := fixture.svc.IssueToken(context.Background(), app.IssueTokenInput{
		ClientID:     created.Client.ClientID,
		ClientSecret: created.ClientSecret,
		Scope:        "order:read order:write",
		TraceID:      "trace-scope",
	})
	if !errors.Is(err, app.ErrM2MInvalidScope) {
		t.Fatalf("err = %v, want ErrM2MInvalidScope", err)
	}
}

// TestM2MUnknownClientRejected 验证不存在 / 密钥错误统一 invalid_client（AC-P16-M2M-1）。
func TestM2MUnknownClientRejected(t *testing.T) {
	fixture := newM2MFixture(t)
	created := fixture.createClient(t, []string{"order:read"})

	cases := []app.IssueTokenInput{
		{ClientID: "svc_doesnotexist", ClientSecret: "whatever", Scope: "order:read"},
		{ClientID: created.Client.ClientID, ClientSecret: strings.Repeat("x", 43), Scope: "order:read"},
		{ClientID: created.Client.ClientID, ClientSecret: created.ClientSecret, Scope: ""},
	}
	for i, input := range cases {
		input.TraceID = "trace-unknown"
		_, err := fixture.svc.IssueToken(context.Background(), input)
		if i == 2 {
			// 空 scope 在凭据校验之后判定，仍属 invalid_scope。
			if !errors.Is(err, app.ErrM2MInvalidScope) {
				t.Fatalf("case %d: err = %v, want ErrM2MInvalidScope", i, err)
			}
			continue
		}
		if !errors.Is(err, app.ErrM2MInvalidClient) {
			t.Fatalf("case %d: err = %v, want ErrM2MInvalidClient", i, err)
		}
	}
}

// TestM2MRotateGrace 验证轮换后旧 secret 宽限内可用、到期失效（AC-P16-M2M-4）。
func TestM2MRotateGrace(t *testing.T) {
	fixture := newM2MFixture(t)
	created := fixture.createClient(t, []string{"order:read"})

	rotated, err := fixture.svc.RotateSecret(context.Background(), created.Client.ClientID, "1", "trace-rotate")
	if err != nil {
		t.Fatalf("rotate secret: %v", err)
	}
	if rotated.ClientSecret == created.ClientSecret {
		t.Fatalf("rotated secret must differ from the original")
	}

	// 新 secret 立即可用。
	if _, err := fixture.svc.IssueToken(context.Background(), app.IssueTokenInput{
		ClientID: created.Client.ClientID, ClientSecret: rotated.ClientSecret,
		Scope: "order:read", TraceID: "trace-new",
	}); err != nil {
		t.Fatalf("issue with new secret: %v", err)
	}
	// 旧 secret 在宽限内仍可用。
	if _, err := fixture.svc.IssueToken(context.Background(), app.IssueTokenInput{
		ClientID: created.Client.ClientID, ClientSecret: created.ClientSecret,
		Scope: "order:read", TraceID: "trace-old-grace",
	}); err != nil {
		t.Fatalf("issue with retiring secret within grace: %v", err)
	}
	// 超过宽限后旧 secret 失效。
	fixture.clock.Advance(2 * time.Hour)
	if _, err := fixture.svc.IssueToken(context.Background(), app.IssueTokenInput{
		ClientID: created.Client.ClientID, ClientSecret: created.ClientSecret,
		Scope: "order:read", TraceID: "trace-old-expired",
	}); !errors.Is(err, app.ErrM2MInvalidClient) {
		t.Fatalf("err = %v, want ErrM2MInvalidClient after grace", err)
	}
}

// TestM2MDisableRejectsIssue 验证停用后不再签发（§21.2.4）。
func TestM2MDisableRejectsIssue(t *testing.T) {
	fixture := newM2MFixture(t)
	created := fixture.createClient(t, []string{"order:read"})

	if err := fixture.svc.DisableClient(context.Background(), created.Client.ClientID, "1", "trace-disable"); err != nil {
		t.Fatalf("disable client: %v", err)
	}
	if _, err := fixture.svc.IssueToken(context.Background(), app.IssueTokenInput{
		ClientID: created.Client.ClientID, ClientSecret: created.ClientSecret,
		Scope: "order:read", TraceID: "trace-disabled",
	}); !errors.Is(err, app.ErrM2MInvalidClient) {
		t.Fatalf("err = %v, want ErrM2MInvalidClient", err)
	}
}

// TestRevocationCheckerRejectsDisabledClient 验证停用后机器令牌被撤销检查拒绝（AC-P16-M2M-5）。
func TestRevocationCheckerRejectsDisabledClient(t *testing.T) {
	fixture := newM2MFixture(t)
	created := fixture.createClient(t, []string{"order:read"})
	token, err := fixture.svc.IssueToken(context.Background(), app.IssueTokenInput{
		ClientID: created.Client.ClientID, ClientSecret: created.ClientSecret,
		Scope: "order:read", TraceID: "trace-check",
	})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	claims, err := fixture.signer.Parse(token.AccessToken)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	checker, err := app.NewRevocationChecker(
		persistence.NewMemoryAccountRepository(),
		fixture.store,
		app.RevocationFailClosed,
		30*time.Second,
		app.WithRevocationClients(fixture.clients),
		app.WithRevocationClock(fixture.clock),
	)
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	if err := checker.Check(context.Background(), claims); err != nil {
		t.Fatalf("check active client token: %v", err)
	}
	if err := fixture.svc.DisableClient(context.Background(), created.Client.ClientID, "1", "trace-disable"); err != nil {
		t.Fatalf("disable client: %v", err)
	}
	if err := checker.Check(context.Background(), claims); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("err = %v, want unauthorized after disable", err)
	}
}

// TestM2MCreateRejectsScopeOutsideWhitelist 验证创建时越权 scope 被拒。
func TestM2MCreateRejectsScopeOutsideWhitelist(t *testing.T) {
	fixture := newM2MFixture(t)
	_, err := fixture.svc.CreateClient(context.Background(), app.CreateM2MClientInput{
		Name:     "bad",
		Channels: []domain.Channel{domain.ChannelAdmin},
		Scopes:   []string{"config:write"},
		TraceID:  "trace-bad-scope",
	})
	if err == nil {
		t.Fatalf("create with scope outside whitelist must fail")
	}
}
