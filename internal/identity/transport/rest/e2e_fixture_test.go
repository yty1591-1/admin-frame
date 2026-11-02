package rest

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238 规定 TOTP 使用 HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
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
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/contextx"
	securecrypto "github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/jwt"
	"github.com/admin-frame/admin-frame/pkg/password"
	"github.com/admin-frame/admin-frame/pkg/totp"
)

// e2eTraceID 是测试请求的固定关联 ID。
const e2eTraceID = "trace-e2e"

// e2eClock 是可推进的测试时钟，保证令牌与 MFA 时间步可控。
type e2eClock struct{ now time.Time }

// Now 返回当前测试时间。
func (c *e2eClock) Now() time.Time { return c.now }

// advance 推进测试时间。
func (c *e2eClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// e2eIDs 生成递增的测试主键，避免依赖真实雪花 ID。
type e2eIDs struct {
	mu sync.Mutex
	n  int64
}

// NextString 返回下一个主键。
func (s *e2eIDs) NextString() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return "id-" + strconv.FormatInt(s.n, 10), nil
}

// e2eHasher 是确定性密码哈希器，避免单测消耗 Argon2id 计算资源。
type e2eHasher struct{}

// Hash 返回可逆测试编码。
func (e2eHasher) Hash(plain []byte) (string, error) { return "fake$" + string(plain), nil }

// Verify 比对测试编码。
func (e2eHasher) Verify(encoded string, plain []byte) error {
	if encoded != "fake$"+string(plain) {
		return password.ErrMismatch
	}
	return nil
}

// NeedsRehash 测试哈希始终视为最新参数。
func (e2eHasher) NeedsRehash(string) bool { return false }

// Algorithm 返回测试算法标识。
func (e2eHasher) Algorithm() password.Algorithm { return password.AlgorithmArgon2id }

// recordingLimiter 记录端点限流键并始终放行。
type recordingLimiter struct {
	mu   sync.Mutex
	keys []string
}

// Allow 记录限流键并放行。
func (l *recordingLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
	return true, 0
}

// snapshot 返回已记录的限流键副本。
func (l *recordingLimiter) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.keys))
	copy(out, l.keys)
	return out
}

// e2eFixture 聚合认证 Handler 所需的真实内存实现。
type e2eFixture struct {
	handler     *Handler
	auth        *app.AuthService
	accounts    *persistence.MemoryAccountRepository
	credentials *persistence.MemoryCredentialRepository
	sessions    *persistence.MemorySessionRepository
	mfa         *persistence.MemoryMFAEnrollmentRepository
	clock       *e2eClock
	ids         *e2eIDs
	channel     domain.Channel
	limiter     *recordingLimiter
}

// e2eOption 允许用例在构造时覆盖 AuthService / Handler 配置。
type e2eOption func(*app.AuthServiceConfig, *HandlerConfig)

// withE2EMFARequired 强制当前端别绑定 MFA 后才能登录。
func withE2EMFARequired() e2eOption {
	return func(cfg *app.AuthServiceConfig, _ *HandlerConfig) {
		cfg.MFARequired = true
	}
}

// withE2EDenyLimiter 让端点限流器始终拒绝。
func withE2EDenyLimiter(retry time.Duration) e2eOption {
	return func(_ *app.AuthServiceConfig, hc *HandlerConfig) {
		hc.EndpointLimiter = denyLimiter{retry: retry}
	}
}

// denyLimiter 始终拒绝并返回建议等待时长。
type denyLimiter struct{ retry time.Duration }

// Allow 始终拒绝。
func (l denyLimiter) Allow(string) (bool, time.Duration) { return false, l.retry }

// newE2EFixture 构造启用真实令牌签发与内存仓储的认证服务。
func newE2EFixture(t *testing.T, channel domain.Channel, opts ...e2eOption) *e2eFixture {
	t.Helper()
	clock := &e2eClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	ids := &e2eIDs{}
	accounts := persistence.NewMemoryAccountRepository()
	credentials := persistence.NewMemoryCredentialRepository()
	sessions := persistence.NewMemorySessionRepository()
	attempts := persistence.NewMemoryLoginAttemptRepository()
	mfaRepo := persistence.NewMemoryMFAEnrollmentRepository()
	mfaTickets := cache.NewMemoryMFATicketStore()
	revocation := cache.NewMemoryRevocationStore()
	limiter := &recordingLimiter{}

	mfaCipher, err := securecrypto.NewAES256GCM([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatalf("new mfa cipher: %v", err)
	}
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "https://identity.example.com",
		Audience: []string{string(channel)},
		Secret:   []byte("01234567890123456789012345678901"),
		TTL:      time.Minute,
		Clock:    clock,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	tokens, err := app.NewTokenService(signer, channel, 15*time.Minute, 168*time.Hour, ids,
		app.WithTokenClock(clock), app.WithTokenParser(signer))
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	passwordSvc, err := app.NewPasswordService(e2eHasher{}, app.DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("new password service: %v", err)
	}
	guard, err := app.NewLoginGuard(accounts, attempts, ids, app.DefaultLockoutPolicy())
	if err != nil {
		t.Fatalf("new login guard: %v", err)
	}
	serviceCfg := app.AuthServiceConfig{
		Accounts:    accounts,
		Credentials: credentials,
		Sessions:    sessions,
		Guard:       guard,
		Password:    passwordSvc,
		Tokens:      tokens,
		Revocation:  revocation,
		Audit:       audit.SlogEmitter{Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		IDs:         ids,
		Channel:     channel,
		IdleTimeout: 72 * time.Hour,
		Service:     "identity-test",
		Clock:       clock,
		MFA:         mfaRepo,
		MFACipher:   mfaCipher,
		MFATickets:  mfaTickets,
		MFAIssuer:   "identity-test",
	}
	handlerCfg := HandlerConfig{
		Channel:         channel,
		EndpointLimiter: limiter,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&serviceCfg, &handlerCfg)
		}
	}
	svc, err := app.NewAuthService(serviceCfg)
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	handlerCfg.Auth = svc
	handler, err := NewHandler(handlerCfg)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return &e2eFixture{
		handler: handler, auth: svc, accounts: accounts, credentials: credentials,
		sessions: sessions, mfa: mfaRepo, clock: clock, ids: ids, channel: channel,
		limiter: limiter,
	}
}

// seedAccount 写入 pending 账号与密码凭据；首次登录会激活账号。
func (f *e2eFixture) seedAccount(t *testing.T, id, subject, plainPassword string) *domain.Account {
	t.Helper()
	ctx := context.Background()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: id, Channel: f.channel, Subject: subject, Now: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := f.accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	if plainPassword != "" {
		credential, err := domain.NewCredential(domain.NewCredentialParams{
			ID:        "cred-" + id,
			AccountID: id,
			Type:      domain.CredentialTypePassword,
			Hash:      "fake$" + plainPassword,
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

// reload 重新读取账号。
func (f *e2eFixture) reload(t *testing.T, id string) *domain.Account {
	t.Helper()
	account, err := f.accounts.FindByID(context.Background(), id)
	if err != nil {
		t.Fatalf("load account %s: %v", id, err)
	}
	return account
}

// login 直接调用用例登录，用于准备会话状态。
func (f *e2eFixture) login(t *testing.T, subject, plainPassword string) app.LoginResult {
	t.Helper()
	result, err := f.auth.Login(context.Background(), app.LoginInput{
		Subject:  subject,
		Password: plainPassword,
		IPHash:   "ip-hash",
		UAHash:   "ua-hash",
		TraceID:  "trace-login",
	})
	if err != nil {
		t.Fatalf("login %s: %v", subject, err)
	}
	return result
}

// mustChangePasswordByAdmin 模拟管理员重置后的强制改密标记。
func (f *e2eFixture) forceMustChangePassword(t *testing.T, id string) {
	t.Helper()
	account := f.reload(t, id)
	account.MustChangePassword = true
	if err := f.accounts.Save(context.Background(), account, account.Version); err != nil {
		t.Fatalf("save must_change_password: %v", err)
	}
}

// bindMFA 完成 MFA 绑定初始化与首码确认，返回 Base32 密钥。
func (f *e2eFixture) bindMFA(t *testing.T, accountID string) string {
	t.Helper()
	ctx := context.Background()
	setup, err := f.auth.SetupMFA(ctx, app.MFASetupInput{
		Actor: app.SessionActor{AccountID: accountID, TraceID: "trace-setup"},
	})
	if err != nil {
		t.Fatalf("setup mfa: %v", err)
	}
	result, err := f.auth.VerifyMFA(ctx, app.MFAVerifyInput{
		Code:    totpCode(t, setup.Secret, f.clock.Now()),
		Actor:   app.SessionActor{AccountID: accountID, TraceID: "trace-confirm"},
		TraceID: "trace-confirm",
	})
	if err != nil {
		t.Fatalf("confirm mfa binding: %v", err)
	}
	if !result.BindingConfirmed {
		t.Fatalf("expected binding confirmed, got %+v", result)
	}
	f.clock.advance(totp.Period)
	return setup.Secret
}

// totpCode 按 RFC 6238 计算指定时间步的动态码。
func totpCode(t *testing.T, secretBase32 string, now time.Time) string {
	t.Helper()
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).
		DecodeString(strings.ToUpper(strings.TrimSpace(secretBase32)))
	if err != nil {
		t.Fatalf("decode totp secret: %v", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/int64(totp.Period/time.Second))) //nolint:gosec // G115: 测试时间戳固定为正
	mac := hmac.New(sha1.New, secret)
	if _, err := mac.Write(counter[:]); err != nil {
		t.Fatalf("hmac write: %v", err)
	}
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

// principal 构造与登录会话对齐的鉴权上下文。
func (f *e2eFixture) principal(accountID, sessionID string) *auth.Principal {
	return &auth.Principal{
		Channel:        auth.Channel(string(f.channel)),
		SubjectID:      accountID,
		SessionID:      sessionID,
		TokenID:        "jti-" + accountID,
		TokenExpiresAt: f.clock.now.Add(15 * time.Minute),
	}
}

// e2eCall 描述一次直接 Handler 调用。
type e2eCall struct {
	method      string
	target      string
	body        string
	contentType string
	principal   *auth.Principal
	pathValues  map[string]string
}

// invoke 直调 Handler 并返回响应记录器。
func (f *e2eFixture) invoke(fn http.HandlerFunc, call e2eCall) *httptest.ResponseRecorder {
	var reader io.Reader = http.NoBody
	if call.body != "" {
		reader = strings.NewReader(call.body)
	}
	request := httptest.NewRequest(call.method, call.target, reader)
	if call.body != "" {
		contentType := call.contentType
		if contentType == "" {
			contentType = "application/json"
		}
		request.Header.Set("Content-Type", contentType)
	}
	for key, value := range call.pathValues {
		request.SetPathValue(key, value)
	}
	ctx := contextx.WithRequestID(request.Context(), e2eTraceID)
	if call.principal != nil {
		ctx = auth.WithPrincipal(ctx, *call.principal)
	}
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	fn(recorder, request)
	return recorder
}

// requireStatus 断言 HTTP 状态码。
func requireStatus(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("status = %d, want %d (body=%s)", recorder.Code, want, recorder.Body.String())
	}
}

// envelopeCode 解析统一错误信封中的业务码。
func envelopeCode(t *testing.T, recorder *httptest.ResponseRecorder) int {
	t.Helper()
	var envelope httpx.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope from %q: %v", recorder.Body.String(), err)
	}
	return envelope.Code
}

// requireErrorCode 断言业务错误码。
func requireErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if got := envelopeCode(t, recorder); got != want {
		t.Fatalf("error code = %d, want %d (body=%s)", got, want, recorder.Body.String())
	}
}

// decodeData 把成功响应 data 段解码到目标结构。
func decodeData(t *testing.T, recorder *httptest.ResponseRecorder, dst any) {
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
