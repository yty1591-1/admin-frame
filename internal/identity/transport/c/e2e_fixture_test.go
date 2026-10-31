package c

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/contextx"
	securecrypto "github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/jwt"
	"github.com/admin-frame/admin-frame/pkg/password"
)

// cTraceID 是测试请求的固定关联 ID。
const cTraceID = "trace-c-e2e"

// authHeader 是测试鉴权中间件读取的账号头。
const authHeader = "X-Test-Account"

// cClock 是可推进的测试时钟，用于短信重发间隔与验证码 TTL。
type cClock struct{ now time.Time }

// Now 返回当前测试时间。
func (c *cClock) Now() time.Time { return c.now }

// advance 推进测试时间。
func (c *cClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// cIDs 生成递增的测试主键。
type cIDs struct {
	mu sync.Mutex
	n  int64
}

// NextString 返回下一个主键。
func (s *cIDs) NextString() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return "id-" + strconv.FormatInt(s.n, 10), nil
}

// cHasher 是确定性密码哈希器，避免单测消耗 Argon2id 计算资源。
type cHasher struct{}

// Hash 返回可逆测试编码。
func (cHasher) Hash(plain []byte) (string, error) { return "fake$" + string(plain), nil }

// Verify 比对测试编码。
func (cHasher) Verify(encoded string, plain []byte) error {
	if encoded != "fake$"+string(plain) {
		return password.ErrMismatch
	}
	return nil
}

// NeedsRehash 测试哈希始终视为最新参数。
func (cHasher) NeedsRehash(string) bool { return false }

// Algorithm 返回测试算法标识。
func (cHasher) Algorithm() password.Algorithm { return password.AlgorithmArgon2id }

// recordingSender 记录每个手机号最近一次验证码。
type recordingSender struct {
	mu    sync.Mutex
	codes map[string]string
}

// SendCode 记录验证码并返回成功。
func (s *recordingSender) SendCode(_ context.Context, phone, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.codes == nil {
		s.codes = make(map[string]string)
	}
	s.codes[phone] = code
	return nil
}

// codeOf 返回指定手机号最近一次验证码。
func (s *recordingSender) codeOf(t *testing.T, phone string) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	code := s.codes[phone]
	if code == "" {
		t.Fatalf("no sms code captured for %s", phone)
	}
	return code
}

// fakeOAuthProvider 是可控的 OAuth 提供方；主体由授权码派生，便于区分不同流程。
type fakeOAuthProvider struct {
	name    string
	failFor string
}

// Name 返回提供方名。
func (p *fakeOAuthProvider) Name() string { return p.name }

// AuthorizationURL 返回带 state 的授权地址。
func (p *fakeOAuthProvider) AuthorizationURL(state, codeVerifier string) (string, error) {
	return "https://idp.example.com/authorize?state=" + state + "&code_challenge=" + codeVerifier, nil
}

// Exchange 用授权码换取外部主体；failFor 指定时返回错误。
func (p *fakeOAuthProvider) Exchange(_ context.Context, code, codeVerifier string) (app.OAuthIdentityInfo, error) {
	if code == "" || codeVerifier == "" {
		return app.OAuthIdentityInfo{}, errOAuthExchange
	}
	if p.failFor != "" && code == p.failFor {
		return app.OAuthIdentityInfo{}, errOAuthExchange
	}
	return app.OAuthIdentityInfo{Subject: "sub:" + strings.TrimSpace(code)}, nil
}

// cFixture 聚合 C 端 Handler 所需的真实内存实现。
type cFixture struct {
	handler     *Handler
	restHandler *rest.Handler
	mux         *http.ServeMux
	auth        *app.AuthService
	sms         *app.SMSLoginService
	oauth       *app.OAuthService
	accounts    *persistence.MemoryAccountRepository
	credentials *persistence.MemoryCredentialRepository
	sessions    *persistence.MemorySessionRepository
	mfa         *persistence.MemoryMFAEnrollmentRepository
	identities  *persistence.MemoryOAuthIdentityRepository
	states      *cache.MemoryOAuthStateStore
	sender      *recordingSender
	provider    *fakeOAuthProvider
	clock       *cClock
	ids         *cIDs
	providers   []string
}

// cOption 允许用例在构造时关闭可选能力。
type cOption func(*cFixtureConfig)

// cFixtureConfig 是夹具的可选能力开关。
type cFixtureConfig struct {
	disableSMS   bool
	disableOAuth bool
}

// withoutSMS 关闭短信登录能力。
func withoutSMS() cOption {
	return func(cfg *cFixtureConfig) { cfg.disableSMS = true }
}

// withoutOAuth 关闭 OAuth 登录能力。
func withoutOAuth() cOption {
	return func(cfg *cFixtureConfig) { cfg.disableOAuth = true }
}

// newCFixture 构造启用真实令牌签发、短信与 OAuth 内存实现的 C 端 Handler。
func newCFixture(t *testing.T, opts ...cOption) *cFixture {
	t.Helper()
	clock := &cClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	ids := &cIDs{}
	accounts := persistence.NewMemoryAccountRepository()
	credentials := persistence.NewMemoryCredentialRepository()
	sessions := persistence.NewMemorySessionRepository()
	attempts := persistence.NewMemoryLoginAttemptRepository()
	mfaRepo := persistence.NewMemoryMFAEnrollmentRepository()
	mfaTickets := cache.NewMemoryMFATicketStore()
	revocation := cache.NewMemoryRevocationStore()
	identities := persistence.NewMemoryOAuthIdentityRepository()
	states := cache.NewMemoryOAuthStateStore(clock)
	sender := &recordingSender{}
	provider := &fakeOAuthProvider{name: "test"}

	mfaCipher, err := securecrypto.NewAES256GCM([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatalf("new mfa cipher: %v", err)
	}
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "https://identity.example.com",
		Audience: []string{string(domain.ChannelC)},
		Secret:   []byte("01234567890123456789012345678901"),
		TTL:      time.Minute,
		Clock:    clock,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	tokens, err := app.NewTokenService(signer, domain.ChannelC, 15*time.Minute, 168*time.Hour, ids,
		app.WithTokenClock(clock), app.WithTokenParser(signer))
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	passwordSvc, err := app.NewPasswordService(cHasher{}, app.DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("new password service: %v", err)
	}
	guard, err := app.NewLoginGuard(accounts, attempts, ids, app.DefaultLockoutPolicy())
	if err != nil {
		t.Fatalf("new login guard: %v", err)
	}
	svc, err := app.NewAuthService(app.AuthServiceConfig{
		Accounts:    accounts,
		Credentials: credentials,
		Sessions:    sessions,
		Guard:       guard,
		Password:    passwordSvc,
		Tokens:      tokens,
		Revocation:  revocation,
		Audit:       audit.SlogEmitter{Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		IDs:         ids,
		Channel:     domain.ChannelC,
		IdleTimeout: 72 * time.Hour,
		Service:     "identity-c-test",
		Clock:       clock,
		MFA:         mfaRepo,
		MFACipher:   mfaCipher,
		MFATickets:  mfaTickets,
		MFAIssuer:   "identity-test",
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	restHandler, err := rest.NewHandler(rest.HandlerConfig{Auth: svc, Channel: domain.ChannelC})
	if err != nil {
		t.Fatalf("new rest handler: %v", err)
	}

	config := cFixtureConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}

	fixture := &cFixture{
		restHandler: restHandler, auth: svc, accounts: accounts, credentials: credentials,
		sessions: sessions, mfa: mfaRepo, identities: identities, states: states,
		sender: sender, provider: provider, clock: clock, ids: ids,
	}

	if !config.disableSMS {
		smsService, err := app.NewSMSLoginService(app.SMSLoginConfig{
			Accounts:       accounts,
			Guard:          guard,
			Codes:          cache.NewMemorySMSCodeStore(clock),
			Limiter:        cache.NewMemorySMSRateLimiter(clock),
			Sender:         sender,
			Auth:           svc,
			CodeTTL:        5 * time.Minute,
			ResendInterval: 60 * time.Second,
			MaxAttempts:    5,
			DailyPerPhone:  10,
			HourlyPerIP:    30,
		})
		if err != nil {
			t.Fatalf("new sms login service: %v", err)
		}
		fixture.sms = smsService
	}

	if !config.disableOAuth {
		oauthService, err := app.NewOAuthService(app.OAuthServiceConfig{
			Providers:  map[string]app.OAuthProvider{"test": provider},
			States:     states,
			Identities: identities,
			Accounts:   accounts,
			IDs:        ids,
			Auth:       svc,
			StateTTL:   10 * time.Minute,
		})
		if err != nil {
			t.Fatalf("new oauth service: %v", err)
		}
		fixture.oauth = oauthService
		fixture.providers = []string{"test"}
	}

	handlerConfig := HandlerConfig{
		Rest:      restHandler,
		SMS:       fixture.sms,
		OAuth:     fixture.oauth,
		Auth:      svc,
		Providers: fixture.providers,
	}
	handler, err := NewHandler(handlerConfig)
	if err != nil {
		t.Fatalf("new c handler: %v", err)
	}
	fixture.handler = handler

	mux := http.NewServeMux()
	Register(mux, handler, testAuthMiddleware)
	fixture.mux = mux
	return fixture
}

// testAuthMiddleware 依据测试头注入鉴权上下文；缺失头时保持未认证。
func testAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if accountID := strings.TrimSpace(r.Header.Get(authHeader)); accountID != "" {
			ctx := contextx.WithRequestID(r.Context(), cTraceID)
			ctx = auth.WithPrincipal(ctx, auth.Principal{
				Channel:        auth.ChannelC,
				SubjectID:      accountID,
				SessionID:      "family-" + accountID,
				TokenID:        "jti-" + accountID,
				TokenExpiresAt: time.Now().Add(time.Minute),
			})
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// do 通过路由执行一次请求；accountID 为空表示未认证。
func (f *cFixture) do(t *testing.T, method, target, body, accountID string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if accountID != "" {
		request.Header.Set(authHeader, accountID)
	}
	recorder := httptest.NewRecorder()
	f.mux.ServeHTTP(recorder, request)
	return recorder
}

// invoke 直调 Handler（用于路由未注册的关闭能力分支）。
func (f *cFixture) invoke(
	fn http.HandlerFunc, method, target, body, accountID string, values map[string]string,
) *httptest.ResponseRecorder {
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range values {
		request.SetPathValue(key, value)
	}
	ctx := contextx.WithRequestID(request.Context(), cTraceID)
	if accountID != "" {
		ctx = auth.WithPrincipal(ctx, auth.Principal{
			Channel:        auth.ChannelC,
			SubjectID:      accountID,
			SessionID:      "family-" + accountID,
			TokenID:        "jti-" + accountID,
			TokenExpiresAt: f.clock.now.Add(time.Minute),
		})
	}
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	fn(recorder, request)
	return recorder
}

// seedAccount 写入 C 端账号；withPassword 控制是否附带密码凭据。
func (f *cFixture) seedAccount(t *testing.T, id, subject string, withPassword bool) *domain.Account {
	t.Helper()
	ctx := context.Background()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: id, Channel: domain.ChannelC, Subject: subject, Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := f.accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	if withPassword {
		credential, err := domain.NewCredential(domain.NewCredentialParams{
			ID:        "cred-" + id,
			AccountID: id,
			Type:      domain.CredentialTypePassword,
			Hash:      "fake$Consumer#Passw0rd12",
			Algorithm: "fake",
			Now:       f.clock.Now(),
		})
		if err != nil {
			t.Fatalf("new credential: %v", err)
		}
		if err := f.credentials.Save(ctx, credential); err != nil {
			t.Fatalf("save credential: %v", err)
		}
	}
	return account
}

// seedIdentity 写入外部身份映射。
func (f *cFixture) seedIdentity(t *testing.T, accountID, provider, subject string) {
	t.Helper()
	identity, err := domain.NewOAuthIdentity(domain.NewOAuthIdentityParams{
		ID:              "oauth-" + accountID,
		AccountID:       accountID,
		Provider:        provider,
		ProviderSubject: subject,
	})
	if err != nil {
		t.Fatalf("new oauth identity: %v", err)
	}
	if err := f.identities.Save(context.Background(), identity); err != nil {
		t.Fatalf("save oauth identity: %v", err)
	}
}

// sendCode 直接调用短信用例发送验证码并返回手机号对应的验证码。
func (f *cFixture) sendCode(t *testing.T, phone string) string {
	t.Helper()
	if err := f.sms.Send(context.Background(), app.SendSMSCodeInput{Phone: phone, TraceID: "trace-send"}); err != nil {
		t.Fatalf("send sms code: %v", err)
	}
	return f.sender.codeOf(t, phone)
}

// registerPhone 直接调用短信用例完成注册并返回登录结果。
func (f *cFixture) registerPhone(t *testing.T, phone string) app.LoginResult {
	t.Helper()
	code := f.sendCode(t, phone)
	result, err := f.sms.Register(context.Background(), app.SMSLoginInput{
		Phone: phone, Code: code, IPHash: "ip-hash", UAHash: "ua-hash", TraceID: "trace-register",
	})
	if err != nil {
		t.Fatalf("register phone %s: %v", phone, err)
	}
	return result
}

// errOAuthExchange 是测试用 OAuth 换取失败错误。
var errOAuthExchange = errors.New("oauth exchange rejected")

// requireCStatus 断言 HTTP 状态码。
func requireCStatus(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("status = %d, want %d (body=%s)", recorder.Code, want, recorder.Body.String())
	}
}

// requireCErrorCode 断言统一错误信封中的业务码。
func requireCErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if got := errorCode(t, recorder); got != want {
		t.Fatalf("error code = %d, want %d (body=%s)", got, want, recorder.Body.String())
	}
}

// decodeCData 把成功响应 data 段解码到目标结构。
func decodeCData(t *testing.T, recorder *httptest.ResponseRecorder, dst any) {
	t.Helper()
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope from %q: %v", recorder.Body.String(), err)
	}
	if envelope.Code != 0 {
		t.Fatalf("unexpected business code %d (body=%s)", envelope.Code, recorder.Body.String())
	}
	if len(envelope.Data) == 0 {
		t.Fatalf("response data is empty (body=%s)", recorder.Body.String())
	}
	if err := json.Unmarshal(envelope.Data, dst); err != nil {
		t.Fatalf("decode data %s: %v", envelope.Data, err)
	}
}
