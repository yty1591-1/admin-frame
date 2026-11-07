package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	platformaudit "github.com/admin-frame/admin-frame/internal/platform/audit"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/jwt"
	"github.com/admin-frame/admin-frame/pkg/password"
)

// fixedClock 是可推进的测试时钟，保证令牌与用例时间一致。
type fixedClock struct{ now time.Time }

// Now 返回当前测试时间。
func (c *fixedClock) Now() time.Time { return c.now }

// Advance 推进测试时间。
func (c *fixedClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// fakeHasher 是确定性密码哈希器，避免单测消耗 Argon2id 计算资源。
type fakeHasher struct{}

// Hash 返回可逆的测试编码，仅用于断言是否透传。
func (fakeHasher) Hash(plain []byte) (string, error) { return "fake$" + string(plain), nil }

// Verify 比对测试编码；不一致返回 pkg/password 的哨兵错误。
func (fakeHasher) Verify(encoded string, plain []byte) error {
	if encoded != "fake$"+string(plain) {
		return password.ErrMismatch
	}
	return nil
}

// NeedsRehash 测试哈希始终视为最新参数。
func (fakeHasher) NeedsRehash(string) bool { return false }

// countingHasher 记录 Verify 次数，用于断言时序对齐的占位校验确实执行（IDN-P2-04）。
type countingHasher struct {
	mu       sync.Mutex
	verifies int
}

// Hash 返回可逆测试编码。
func (h *countingHasher) Hash(plain []byte) (string, error) { return "count$" + string(plain), nil }

// Verify 记录调用次数并比对测试编码。
func (h *countingHasher) Verify(encoded string, plain []byte) error {
	h.mu.Lock()
	h.verifies++
	h.mu.Unlock()
	if encoded != "count$"+string(plain) {
		return password.ErrMismatch
	}
	return nil
}

// NeedsRehash 测试哈希始终视为最新参数。
func (h *countingHasher) NeedsRehash(string) bool { return false }

// Algorithm 返回测试用算法标识。
func (h *countingHasher) Algorithm() password.Algorithm { return password.AlgorithmBcrypt }

// count 返回累计 Verify 次数。
func (h *countingHasher) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.verifies
}

// Algorithm 返回测试算法标识。
func (fakeHasher) Algorithm() password.Algorithm { return password.AlgorithmArgon2id }

// authFixture 聚合认证用例所需的真实内存实现。
type authFixture struct {
	svc         *app.AuthService
	tokens      *app.TokenService
	accounts    *persistence.MemoryAccountRepository
	credentials *persistence.MemoryCredentialRepository
	sessions    *persistence.MemorySessionRepository
	store       *cache.MemoryRevocationStore
	mfa         *persistence.MemoryMFAEnrollmentRepository
	mfaTickets  *cache.MemoryMFATicketStore
	mfaCipher   *crypto.AEAD
	signer      *jwt.Manager
	clock       *fixedClock
	ids         *seqIDs
	channel     domain.Channel
}

// withAuthConfig 允许用例在构造时覆盖 AuthServiceConfig（MFA 等可选依赖）。
type withAuthConfig func(*app.AuthServiceConfig)

// newAuthFixture 构造启用真实令牌签发与内存仓储的认证服务。
func newAuthFixture(t *testing.T, channel domain.Channel, opts ...withAuthConfig) *authFixture {
	t.Helper()
	clock := &fixedClock{now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	ids := &seqIDs{}
	accounts := persistence.NewMemoryAccountRepository()
	credentials := persistence.NewMemoryCredentialRepository()
	sessions := persistence.NewMemorySessionRepository()
	attempts := persistence.NewMemoryLoginAttemptRepository()
	mfa := persistence.NewMemoryMFAEnrollmentRepository()
	mfaTickets := cache.NewMemoryMFATicketStore()
	store := cache.NewMemoryRevocationStore()

	mfaCipher, err := crypto.NewAES256GCM([]byte("01234567890123456789012345678901"))
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
	passwordSvc, err := app.NewPasswordService(fakeHasher{}, app.DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("new password service: %v", err)
	}
	guard, err := app.NewLoginGuard(accounts, attempts, ids, app.DefaultLockoutPolicy())
	if err != nil {
		t.Fatalf("new login guard: %v", err)
	}
	cfg := app.AuthServiceConfig{
		Accounts:    accounts,
		Credentials: credentials,
		Sessions:    sessions,
		Guard:       guard,
		Password:    passwordSvc,
		Tokens:      tokens,
		Revocation:  store,
		Audit:       platformaudit.SlogEmitter{Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		IDs:         ids,
		Channel:     channel,
		IdleTimeout: 72 * time.Hour,
		Service:     "identity-test",
		Clock:       clock,
		MFA:         mfa,
		MFACipher:   mfaCipher,
		MFATickets:  mfaTickets,
		MFAIssuer:   "identity-test",
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	svc, err := app.NewAuthService(cfg)
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	return &authFixture{
		svc:         svc,
		tokens:      tokens,
		accounts:    accounts,
		credentials: credentials,
		sessions:    sessions,
		store:       store,
		mfa:         mfa,
		mfaTickets:  mfaTickets,
		mfaCipher:   mfaCipher,
		signer:      signer,
		clock:       clock,
		ids:         ids,
		channel:     channel,
	}
}

// seedAccount 写入 pending 账号与密码凭据；首次登录会激活账号。
func (f *authFixture) seedAccount(t *testing.T, id, subject, plainPassword string) *domain.Account {
	t.Helper()
	merchantID := ""
	if f.channel == domain.ChannelBiz {
		merchantID = "m-1"
	}
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:         id,
		Channel:    f.channel,
		Subject:    subject,
		MerchantID: merchantID,
		Now:        f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := f.accounts.Save(context.Background(), account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
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
	if err := f.credentials.Save(context.Background(), credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	return account
}

// login 执行一次成功登录并返回结果。
func (f *authFixture) login(t *testing.T, subject, plainPassword string) app.LoginResult {
	t.Helper()
	merchant := ""
	if f.channel == domain.ChannelBiz {
		merchant = "m-1"
	}
	result, err := f.svc.Login(context.Background(), app.LoginInput{
		Subject:    subject,
		MerchantID: merchant,
		Password:   plainPassword,
		TraceID:    "trace-login",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return result
}

// sessionOf 加载会话用于断言。
func (f *authFixture) sessionOf(t *testing.T, id string) *domain.Session {
	t.Helper()
	session, err := f.sessions.FindByID(context.Background(), id)
	if err != nil {
		t.Fatalf("load session %s: %v", id, err)
	}
	return session
}

// requireAppCode 断言错误携带指定业务数字码。
func requireAppCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %d, got nil", want)
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error %v is not *apperr.AppError", err)
	}
	if appErr.Code != want {
		t.Fatalf("error code = %d, want %d (%v)", appErr.Code, want, err)
	}
}

// TestLoginRejectsInvalidInputs 覆盖登录输入长度与端别约束（SEC-03 / BIZ-06）。
func TestLoginRejectsInvalidInputs(t *testing.T) {
	ctx := context.Background()
	adminFixture := newAuthFixture(t, domain.ChannelAdmin)
	adminFixture.seedAccount(t, "acct-input", "alice", "Correct#Passw0rd")
	cases := []struct {
		name  string
		input app.LoginInput
	}{
		{"empty_subject", app.LoginInput{Subject: " ", Password: "x"}},
		{"empty_password", app.LoginInput{Subject: "alice", Password: " "}},
		{"overlong_subject", app.LoginInput{Subject: strings.Repeat("x", 256), Password: "Correct#Passw0rd"}},
		{"overlong_password", app.LoginInput{Subject: "alice", Password: strings.Repeat("x", 5000)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := adminFixture.svc.Login(ctx, tc.input)
			if !errors.Is(err, apperr.ErrUnauthorized) {
				t.Fatalf("login error = %v, want unauthorized", err)
			}
		})
	}
	// C 端不支持密码登录。
	cFixture := newAuthFixture(t, domain.ChannelC)
	if _, err := cFixture.svc.Login(ctx, app.LoginInput{Subject: "alice", Password: "x"}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("c channel password login error = %v", err)
	}
	// B 端缺少 merchant_id 必须与凭据错误不可区分。
	bizFixture := newAuthFixture(t, domain.ChannelBiz)
	bizFixture.seedAccount(t, "acct-biz", "biz-user", "Biz#Passw0rd")
	if _, err := bizFixture.svc.Login(ctx, app.LoginInput{
		Subject: "biz-user", Password: "Biz#Passw0rd",
	}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("biz login without merchant error = %v", err)
	}
}

func TestAuthServiceLogin(t *testing.T) {
	ctx := context.Background()
	for _, channel := range []domain.Channel{domain.ChannelAdmin, domain.ChannelBiz} {
		t.Run(string(channel)+"/success", func(t *testing.T) {
			f := newAuthFixture(t, channel)
			merchant := ""
			if channel == domain.ChannelBiz {
				merchant = "m-1"
			}
			account, err := domain.NewAccount(domain.NewAccountParams{
				ID: "acct-1", Channel: channel, Subject: "Alice@Example.com", MerchantID: merchant, Now: f.clock.Now(),
			})
			if err != nil {
				t.Fatalf("new account: %v", err)
			}
			if err := f.accounts.Save(ctx, account, 0); err != nil {
				t.Fatalf("save account: %v", err)
			}
			credential, err := domain.NewCredential(domain.NewCredentialParams{
				ID: "cred-1", AccountID: account.ID, Type: domain.CredentialTypePassword,
				Hash: "fake$" + "Correct#Passw0rd", Algorithm: "fake", Now: f.clock.Now(),
			})
			if err != nil {
				t.Fatalf("new credential: %v", err)
			}
			if err := f.credentials.Save(ctx, credential); err != nil {
				t.Fatalf("save credential: %v", err)
			}

			result, err := f.svc.Login(ctx, app.LoginInput{
				Subject: "  Alice@Example.com  ", MerchantID: merchant, Password: "Correct#Passw0rd",
			})
			if err != nil {
				t.Fatalf("login: %v", err)
			}
			if result.AccessToken == "" || result.RefreshToken == "" || result.RefreshHash == "" {
				t.Fatal("login must return a full token pair")
			}
			if result.SessionID == "" || result.AccountID != account.ID {
				t.Fatalf("unexpected login result: %+v", result)
			}

			loaded, err := f.accounts.FindByID(ctx, account.ID)
			if err != nil {
				t.Fatalf("load account: %v", err)
			}
			if loaded.Status != domain.AccountStatusActive {
				t.Fatalf("pending account should be activated on first login, got %s", loaded.Status)
			}

			session := f.sessionOf(t, result.SessionID)
			if !session.IsActive(f.clock.Now()) || session.RefreshHash != result.RefreshHash {
				t.Fatalf("unexpected session: %+v", session)
			}
			if session.FamilyID == "" || session.TokenVersion != loaded.TokenVersion {
				t.Fatalf("session family / version mismatch: %+v", session)
			}
		})
	}

	t.Run("unknown_subject_and_wrong_password_share_error", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelAdmin)
		f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")

		_, err := f.svc.Login(ctx, app.LoginInput{Subject: "nobody", Password: "Correct#Passw0rd"})
		requireAppCode(t, err, apperr.CodeUnauthorized)
		_, err = f.svc.Login(ctx, app.LoginInput{Subject: "alice", Password: "Wrong#Passw0rd"})
		requireAppCode(t, err, apperr.CodeUnauthorized)

		sessions, err := f.sessions.ListByAccount(ctx, "acct-1")
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}
		if len(sessions) != 0 {
			t.Fatalf("failed login must not create sessions, got %d", len(sessions))
		}
	})
}

func TestAuthServiceCompleteVerifiedLogin(t *testing.T) {
	ctx := context.Background()

	t.Run("pending_account_activated_and_session_created", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelC)
		f.seedAccount(t, "acct-c1", "phone:hash", "unused")

		result, err := f.svc.CompleteVerifiedLogin(ctx, app.VerifiedLoginInput{
			AccountID: "acct-c1",
			IPHash:    app.HashClientRef("127.0.0.1"),
			UAHash:    app.HashClientRef("ua"),
			TraceID:   "trace-sms",
		})
		if err != nil {
			t.Fatalf("complete verified login: %v", err)
		}
		if result.MFARequired || result.MFATicket != "" {
			t.Fatalf("channel c must not require mfa: %+v", result)
		}
		if result.AccessToken == "" || result.RefreshToken == "" || result.SessionID == "" {
			t.Fatalf("unexpected result: %+v", result)
		}
		loaded, err := f.accounts.FindByID(ctx, "acct-c1")
		if err != nil {
			t.Fatalf("load account: %v", err)
		}
		if loaded.Status != domain.AccountStatusActive {
			t.Fatalf("status = %s, want active", loaded.Status)
		}
		session := f.sessionOf(t, result.SessionID)
		if !session.IsActive(f.clock.Now()) || session.RefreshHash != result.RefreshHash {
			t.Fatalf("unexpected session: %+v", session)
		}
	})

	t.Run("locked_and_disabled_rejected", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelC)
		locked := f.seedAccount(t, "acct-locked", "phone:locked", "unused")
		if err := locked.Activate(f.clock.Now()); err != nil {
			t.Fatalf("activate: %v", err)
		}
		if err := f.accounts.Save(ctx, locked, locked.Version-1); err != nil {
			t.Fatalf("save active: %v", err)
		}
		if err := locked.Lock(f.clock.Now().Add(15*time.Minute), f.clock.Now()); err != nil {
			t.Fatalf("lock: %v", err)
		}
		if err := f.accounts.Save(ctx, locked, locked.Version-1); err != nil {
			t.Fatalf("save locked: %v", err)
		}

		disabled := f.seedAccount(t, "acct-disabled", "phone:disabled", "unused")
		if err := disabled.Disable(f.clock.Now()); err != nil {
			t.Fatalf("disable: %v", err)
		}
		if err := f.accounts.Save(ctx, disabled, disabled.Version-1); err != nil {
			t.Fatalf("save disabled: %v", err)
		}

		for _, id := range []string{"acct-locked", "acct-disabled"} {
			_, err := f.svc.CompleteVerifiedLogin(ctx, app.VerifiedLoginInput{AccountID: id, TraceID: "trace-denied"})
			requireAppCode(t, err, apperr.CodeUnauthorized)
			sessions, listErr := f.sessions.ListByAccount(ctx, id)
			if listErr != nil {
				t.Fatalf("list sessions: %v", listErr)
			}
			if len(sessions) != 0 {
				t.Fatalf("%s: rejected login must not create sessions", id)
			}
		}
	})

	t.Run("missing_or_cross_channel_account_rejected", func(t *testing.T) {
		f := newAuthFixture(t, domain.ChannelC)
		adminAccount, err := domain.NewAccount(domain.NewAccountParams{
			ID: "acct-admin", Channel: domain.ChannelAdmin, Subject: "alice", Now: f.clock.Now(),
		})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := f.accounts.Save(ctx, adminAccount, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}

		_, err = f.svc.CompleteVerifiedLogin(ctx, app.VerifiedLoginInput{AccountID: "missing", TraceID: "trace-missing"})
		requireAppCode(t, err, apperr.CodeUnauthorized)
		_, err = f.svc.CompleteVerifiedLogin(ctx, app.VerifiedLoginInput{AccountID: "acct-admin", TraceID: "trace-cross"})
		requireAppCode(t, err, apperr.CodeUnauthorized)
		_, err = f.svc.CompleteVerifiedLogin(ctx, app.VerifiedLoginInput{AccountID: " ", TraceID: "trace-empty"})
		requireAppCode(t, err, apperr.CodeUnauthorized)
	})
}

func TestAuthServiceRefreshRotatesAndRevokesReplay(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	first := f.login(t, "alice", "Correct#Passw0rd")

	second, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: first.RefreshToken, TraceID: "trace-refresh"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken || second.AccessToken == "" {
		t.Fatal("refresh must rotate the refresh token")
	}

	oldSession := f.sessionOf(t, first.SessionID)
	if oldSession.Status != domain.SessionStatusRotated {
		t.Fatalf("old session status = %s, want rotated", oldSession.Status)
	}
	newSession, err := f.sessions.FindByRefreshHash(ctx, second.RefreshHash)
	if err != nil {
		t.Fatalf("load rotated session: %v", err)
	}
	if newSession.Status != domain.SessionStatusActive || newSession.FamilyID != oldSession.FamilyID {
		t.Fatalf("new session must stay in the same family: %+v", newSession)
	}

	// 旧刷新令牌再次出现即重放：整条家庭链被撤销。
	_, err = f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: first.RefreshToken})
	requireAppCode(t, err, apperr.CodeUnauthorized)

	for _, id := range []string{first.SessionID, newSession.ID} {
		session := f.sessionOf(t, id)
		if session.Status != domain.SessionStatusRevoked || session.RevokedReason != domain.RevokeReasonReplay {
			t.Fatalf("session %s should be revoked by replay: %+v", id, session)
		}
	}
}

// TestRefreshRejectsUnknownAndAbsoluteExpiry 覆盖刷新未知令牌与设备绝对到期分支。
func TestRefreshRejectsUnknownAndAbsoluteExpiry(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.SessionAbsoluteTTL = 10 * time.Minute
	})
	f.seedAccount(t, "acct-abs", "abs-user", "Abs#Passw0rd")
	login := f.login(t, "abs-user", "Abs#Passw0rd")
	tampered := login.RefreshToken[:len(login.RefreshToken)-1] + "x"
	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: tampered}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("unknown refresh token error = %v, want unauthorized", err)
	}
}

// TestRefreshRejectsRevokedAndExpired 覆盖刷新令牌已撤销 / 过期分支。
func TestRefreshRejectsRevokedAndExpired(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-refresh-edge", "alice", "Correct#Passw0rd")
	login := f.login(t, "alice", "Correct#Passw0rd")
	actor := app.SessionActor{
		AccountID: login.AccountID,
		SessionID: login.SessionID,
		TraceID:   "trace-refresh-edge",
	}
	if err := f.svc.Logout(ctx, actor); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("refresh revoked token error = %v, want unauthorized", err)
	}
	// 超过刷新有效期后必须拒绝。
	f.clock.Advance(200 * time.Hour)
	login = f.login(t, "alice", "Correct#Passw0rd")
	f.clock.Advance(200 * time.Hour)
	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("refresh expired token error = %v, want unauthorized", err)
	}
}

func TestAuthServiceRefreshRejectsMismatchedPrefix(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelBiz)
	_, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: "ar_invalid"})
	requireAppCode(t, err, apperr.CodeUnauthorized)
}

func TestAuthServiceLogoutBlacklistsTokenAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	login := f.login(t, "alice", "Correct#Passw0rd")

	actor := app.SessionActor{
		AccountID:      login.AccountID,
		SessionID:      login.SessionID,
		TokenID:        "jti-1",
		TokenExpiresAt: f.clock.Now().Add(15 * time.Minute),
	}
	if err := f.svc.Logout(ctx, actor); err != nil {
		t.Fatalf("logout: %v", err)
	}
	session := f.sessionOf(t, login.SessionID)
	if session.Status != domain.SessionStatusRevoked || session.RevokedReason != domain.RevokeReasonLogout {
		t.Fatalf("session should be revoked: %+v", session)
	}
	revoked, err := f.store.IsTokenRevoked(ctx, "jti-1")
	if err != nil || !revoked {
		t.Fatalf("jti must be blacklisted, revoked=%v err=%v", revoked, err)
	}

	// 重复登出必须幂等成功。
	if err := f.svc.Logout(ctx, actor); err != nil {
		t.Fatalf("second logout must be idempotent: %v", err)
	}

	wrongActor := actor
	wrongActor.AccountID = "acct-other"
	requireAppCode(t, f.svc.Logout(ctx, wrongActor), apperr.CodeUnauthorized)
}

func TestAuthServiceLogoutAllBumpsVersionAndRevokesSessions(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	first := f.login(t, "alice", "Correct#Passw0rd")
	second := f.login(t, "alice", "Correct#Passw0rd")

	actor := app.SessionActor{
		AccountID:      second.AccountID,
		SessionID:      second.SessionID,
		TokenID:        "jti-current",
		TokenExpiresAt: f.clock.Now().Add(15 * time.Minute),
	}
	if err := f.svc.LogoutAll(ctx, actor); err != nil {
		t.Fatalf("logout all: %v", err)
	}

	account, err := f.accounts.FindByID(ctx, "acct-1")
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.TokenVersion != 2 {
		t.Fatalf("token version = %d, want 2", account.TokenVersion)
	}
	for _, id := range []string{first.SessionID, second.SessionID} {
		session := f.sessionOf(t, id)
		if session.Status != domain.SessionStatusRevoked || session.RevokedReason != domain.RevokeReasonLogoutAll {
			t.Fatalf("session %s should be revoked by logout-all: %+v", id, session)
		}
	}
	state, hit, err := f.store.LoadAccountState(ctx, "acct-1")
	if err != nil || !hit {
		t.Fatalf("revocation state must be refreshed, hit=%v err=%v", hit, err)
	}
	if state.Version != 2 || state.Status != domain.AccountStatusActive {
		t.Fatalf("unexpected revocation state: %+v", state)
	}
	revoked, err := f.store.IsTokenRevoked(ctx, "jti-current")
	if err != nil || !revoked {
		t.Fatalf("current jti must be blacklisted, revoked=%v err=%v", revoked, err)
	}
}

func TestAuthServiceListAndRevokeSessions(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	f.seedAccount(t, "acct-2", "bob", "Correct#Passw0rd")
	first := f.login(t, "alice", "Correct#Passw0rd")
	second := f.login(t, "alice", "Correct#Passw0rd")
	other := f.login(t, "bob", "Correct#Passw0rd")

	actor := app.SessionActor{
		AccountID:      second.AccountID,
		SessionID:      second.SessionID,
		TokenID:        "jti-current",
		TokenExpiresAt: f.clock.Now().Add(15 * time.Minute),
	}
	page, err := f.svc.ListSessions(ctx, app.SessionListInput{SessionActor: actor})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	views := page.Items
	if len(views) != 2 {
		t.Fatalf("session count = %d, want 2", len(views))
	}
	for _, view := range views {
		if want := view.ID == second.SessionID; view.Current != want {
			t.Fatalf("session %s current = %v, want %v", view.ID, view.Current, want)
		}
	}

	// 跨账号撤销不暴露他人会话是否存在。
	requireAppCode(t, f.svc.RevokeSession(ctx, actor, other.SessionID), apperr.CodeNotFound)
	requireAppCode(t, f.svc.RevokeSession(ctx, actor, "missing-session"), apperr.CodeNotFound)

	// 撤销非当前会话不写 jti 黑名单。
	if err := f.svc.RevokeSession(ctx, actor, first.SessionID); err != nil {
		t.Fatalf("revoke other session: %v", err)
	}
	if session := f.sessionOf(t, first.SessionID); session.Status != domain.SessionStatusRevoked {
		t.Fatalf("session should be revoked: %+v", session)
	}
	revoked, err := f.store.IsTokenRevoked(ctx, "jti-current")
	if err != nil || revoked {
		t.Fatalf("non-current logout must not blacklist jti, revoked=%v err=%v", revoked, err)
	}
}

// TestSessionsListIsFamilyCentric 覆盖连续刷新只产生一台设备，列表按设备分页返回。
func TestSessionsListIsFamilyCentric(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	login := f.login(t, "alice", "Correct#Passw0rd")
	token := login.RefreshToken
	for i := 0; i < 100; i++ {
		refreshed, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: token})
		if err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
		token = refreshed.RefreshToken
	}

	rows, err := f.sessions.ListByAccount(ctx, login.AccountID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(rows) != 101 {
		t.Fatalf("token generations = %d, want 101", len(rows))
	}

	page, err := f.svc.ListSessions(ctx, app.SessionListInput{SessionActor: app.SessionActor{
		AccountID: login.AccountID,
		SessionID: login.SessionID,
	}})
	if err != nil {
		t.Fatalf("list device sessions: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("device sessions = %d, want 1", len(page.Items))
	}
	if page.Items[0].ID != login.SessionID || !page.Items[0].Current {
		t.Fatalf("unexpected device view: %+v", page.Items[0])
	}
	if page.NextCursor != "" {
		t.Fatalf("single page must not return cursor, got %q", page.NextCursor)
	}
}

// TestSessionsListPagination 覆盖设备列表游标分页。
func TestSessionsListPagination(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	first := f.login(t, "alice", "Correct#Passw0rd")
	f.login(t, "alice", "Correct#Passw0rd")
	f.login(t, "alice", "Correct#Passw0rd")

	actor := app.SessionActor{AccountID: first.AccountID}
	page, err := f.svc.ListSessions(ctx, app.SessionListInput{SessionActor: actor, Limit: 2})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page 1 = %d items, cursor %q", len(page.Items), page.NextCursor)
	}
	next, err := f.svc.ListSessions(ctx, app.SessionListInput{
		SessionActor: actor,
		Cursor:       page.NextCursor,
		Limit:        2,
	})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(next.Items) != 1 || next.NextCursor != "" {
		t.Fatalf("page 2 = %d items, cursor %q", len(next.Items), next.NextCursor)
	}
	if next.Items[0].ID == page.Items[0].ID || next.Items[0].ID == page.Items[1].ID {
		t.Fatalf("pagination returned duplicate device %s", next.Items[0].ID)
	}
}

// TestLoginEvictsLeastRecentlyUsedDevice 覆盖单账号设备上限与 auth.session.evicted 审计。
func TestLoginEvictsLeastRecentlyUsedDevice(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.MaxPerAccount = 2
	}, withCapture(capture))
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	first := f.login(t, "alice", "Correct#Passw0rd")
	f.login(t, "alice", "Correct#Passw0rd")
	f.login(t, "alice", "Correct#Passw0rd")

	active, err := f.sessions.ListFamiliesByAccount(ctx, first.AccountID, time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("list families: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active devices = %d, want 2", len(active))
	}
	evicted, err := f.sessions.FindFamily(ctx, first.SessionID)
	if err != nil {
		t.Fatalf("load evicted family: %v", err)
	}
	if evicted.Status != domain.SessionStatusRevoked || evicted.RevokedReason != domain.RevokeReasonEvicted {
		t.Fatalf("oldest family must be evicted: %+v", evicted)
	}
	if events := capture.byAction(audit.ActionAuthSessionEvicted); len(events) != 1 {
		t.Fatalf("expected one auth.session.evicted, got %d", len(events))
	}
}

// TestRefreshEnforcesAbsoluteSessionTTL 覆盖设备绝对生命上限：刷新不延长上限，超限后撤销整链。
func TestRefreshEnforcesAbsoluteSessionTTL(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.SessionAbsoluteTTL = 200 * time.Hour
	})
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	login := f.login(t, "alice", "Correct#Passw0rd")
	wantAbsolute := f.clock.Now().Add(200 * time.Hour)
	if !login.AbsoluteExpiresAt.Equal(wantAbsolute) {
		t.Fatalf("login absolute_expires_at = %v, want %v", login.AbsoluteExpiresAt, wantAbsolute)
	}
	family, err := f.sessions.FindFamily(ctx, login.SessionID)
	if err != nil {
		t.Fatalf("load family: %v", err)
	}
	if !family.AbsoluteExpiresAt.Equal(wantAbsolute) {
		t.Fatalf("family absolute expiry = %v, want %v", family.AbsoluteExpiresAt, wantAbsolute)
	}

	// 刷新只能滑动令牌代，不得延长设备绝对上限。
	token := login.RefreshToken
	f.clock.Advance(30 * time.Minute)
	refreshed, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: token})
	if err != nil {
		t.Fatalf("refresh within absolute ttl: %v", err)
	}
	if !refreshed.AbsoluteExpiresAt.Equal(wantAbsolute) {
		t.Fatalf("refresh extended absolute expiry to %v, want %v", refreshed.AbsoluteExpiresAt, wantAbsolute)
	}

	// 越过绝对上限：拒绝刷新并撤销整条设备链。
	f.clock.Advance(200 * time.Hour)
	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: refreshed.RefreshToken}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("refresh after absolute expiry err = %v, want 401", err)
	}
	family, err = f.sessions.FindFamily(ctx, login.SessionID)
	if err != nil {
		t.Fatalf("reload family: %v", err)
	}
	if family.Status != domain.SessionStatusRevoked || family.RevokedReason != domain.RevokeReasonExpired {
		t.Fatalf("family = %s/%s, want revoked/expired", family.Status, family.RevokedReason)
	}
}

// failingRevokeSessions 模拟仓储撤销失败，用于验证失败关闭与回滚（IDN-P1-03）。
type failingRevokeSessions struct {
	domain.SessionRepository
	fail bool
}

// RevokeFamily 按 fail 决定是否返回存储错误。
func (s *failingRevokeSessions) RevokeFamily(
	ctx context.Context,
	familyID string,
	reason domain.RevokeReason,
	now time.Time,
) (int, error) {
	if s.fail {
		return 0, errStoreDown
	}
	return s.SessionRepository.RevokeFamily(ctx, familyID, reason, now)
}

// recordingMetrics 记录指标事件名与标签，供失败路径断言。
type recordingMetrics struct {
	names  []string
	labels [][]string
}

// add 追加一条指标事件。
func (m *recordingMetrics) add(name string, labels ...string) {
	m.names = append(m.names, name)
	m.labels = append(m.labels, labels)
}

// count 返回指定指标事件的出现次数。
func (m *recordingMetrics) count(name string) int {
	n := 0
	for _, item := range m.names {
		if item == name {
			n++
		}
	}
	return n
}

// ObserveLogin 实现 app.Metrics。
func (m *recordingMetrics) ObserveLogin(_ context.Context, channel domain.Channel, result, reason string, _ time.Duration) {
	m.add("login", channel.String(), result, reason)
}

// ObserveLoginTxDuration 实现 app.Metrics。
func (m *recordingMetrics) ObserveLoginTxDuration(_ context.Context, channel domain.Channel, _ time.Duration) {
	m.add("login_tx", channel.String())
}

// ObserveLockout 实现 app.Metrics。
func (m *recordingMetrics) ObserveLockout(_ context.Context, channel domain.Channel) {
	m.add("lockout", channel.String())
}

// ObserveMFAVerify 实现 app.Metrics。
func (m *recordingMetrics) ObserveMFAVerify(_ context.Context, result string) {
	m.add("mfa_verify", result)
}

// ObserveMFAVerifyDuration 实现 app.Metrics。
func (m *recordingMetrics) ObserveMFAVerifyDuration(_ context.Context, result string, _ time.Duration) {
	m.add("mfa_verify_duration", result)
}

// ObserveRefreshDuration 实现 app.Metrics。
func (m *recordingMetrics) ObserveRefreshDuration(_ context.Context, result string, _ time.Duration) {
	m.add("refresh_duration", result)
}

// ObserveStepUpDuration 实现 app.Metrics。
func (m *recordingMetrics) ObserveStepUpDuration(_ context.Context, result string, _ time.Duration) {
	m.add("step_up_duration", result)
}

// ObserveOAuthCallbackDuration 实现 app.Metrics。
func (m *recordingMetrics) ObserveOAuthCallbackDuration(_ context.Context, provider, result string, _ time.Duration) {
	m.add("oauth_callback_duration", provider, result)
}

// ObserveMFARecoveryUsed 实现 app.Metrics。
func (m *recordingMetrics) ObserveMFARecoveryUsed(_ context.Context, remaining int) {
	m.add("mfa_recovery_used", strconv.Itoa(remaining))
}

// ObserveRefresh 实现 app.Metrics。
func (m *recordingMetrics) ObserveRefresh(_ context.Context, result string) {
	m.add("refresh", result)
}

// ObserveTokenReuse 实现 app.Metrics。
func (m *recordingMetrics) ObserveTokenReuse(_ context.Context) {
	m.add("token_reuse")
}

// ObserveSessionRevoke 实现 app.Metrics。
func (m *recordingMetrics) ObserveSessionRevoke(_ context.Context, reason, result string) {
	m.add("session_revoke", reason, result)
}

// ObserveSMSSend 实现 app.Metrics。
func (m *recordingMetrics) ObserveSMSSend(_ context.Context, result string) {
	m.add("sms_send", result)
}

// ObserveOAuthCallback 实现 app.Metrics。
func (m *recordingMetrics) ObserveOAuthCallback(_ context.Context, provider, result, reason string) {
	m.add("oauth_callback", provider, result, reason)
}

// ObserveJWKSRefresh 实现 app.Metrics。
func (m *recordingMetrics) ObserveJWKSRefresh(_ context.Context, provider, result string) {
	m.add("jwks_refresh", provider, result)
}

// TestRefreshReplayFailsClosedWhenRevokeFails 验证撤销失败时事务失败关闭，不产生「审计成功但未撤销」。
func TestRefreshReplayFailsClosedWhenRevokeFails(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	metrics := &recordingMetrics{}
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.Sessions = &failingRevokeSessions{SessionRepository: cfg.Sessions, fail: true}
		cfg.Metrics = metrics
	}, withCapture(capture))
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	login := f.login(t, "alice", "Correct#Passw0rd")
	oldToken := login.RefreshToken
	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: oldToken}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// 旧令牌再次出现即重放；撤销失败必须返回 50300，而不是记录审计后放行。
	_, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: oldToken})
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("replay err = %v, want 50300", err)
	}
	if events := capture.byAction(audit.ActionAuthTokenReused); len(events) != 0 {
		t.Fatalf("replay audit must not commit when revoke fails, got %d", len(events))
	}
	if got := metrics.count("token_reuse"); got != 0 {
		t.Fatalf("token_reuse metric = %d, want 0 on rollback", got)
	}
	if got := metrics.count("refresh"); got == 0 {
		t.Fatalf("refresh failure metric missing")
	}
}

// TestRefreshGraceAvoidsChainRevocation 验证短窗口并发刷新不撤销整链，超窗仍按重放（REL-06）。
func TestRefreshGraceAvoidsChainRevocation(t *testing.T) {
	ctx := context.Background()
	capture := &captureEmitter{}
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.RefreshGrace = 10 * time.Second
	}, withCapture(capture))
	f.seedAccount(t, "acct-grace", "grace", "Correct#Passw0rd")
	login := f.login(t, "grace", "Correct#Passw0rd")

	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken, TraceID: "trace-refresh-1"}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	// 宽限窗口内重复提交旧令牌：返回 40100，但设备链保持可用。
	_, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken, TraceID: "trace-grace"})
	requireAppCode(t, err, apperr.CodeUnauthorized)
	family, err := f.sessions.FindFamily(ctx, login.SessionID)
	if err != nil {
		t.Fatalf("load family: %v", err)
	}
	if family.Status != domain.SessionStatusActive {
		t.Fatalf("family must stay active within grace, got %s", family.Status)
	}
	if events := capture.byAction(audit.ActionAuthTokenReused); len(events) != 0 {
		t.Fatalf("grace retry must not emit token.reused, got %d", len(events))
	}

	// 超出宽限窗口：按真实重放撤销整链并写审计。
	f.clock.Advance(20 * time.Second)
	_, err = f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken, TraceID: "trace-replay"})
	requireAppCode(t, err, apperr.CodeUnauthorized)
	family, err = f.sessions.FindFamily(ctx, login.SessionID)
	if err != nil {
		t.Fatalf("load family: %v", err)
	}
	if family.Status != domain.SessionStatusRevoked {
		t.Fatalf("family must be revoked after grace, got %s", family.Status)
	}
	if events := capture.byAction(audit.ActionAuthTokenReused); len(events) != 1 {
		t.Fatalf("replay audit = %d, want 1", len(events))
	}
}

// TestMetricsRecordLoginRefreshAndRevoke 验证登录、刷新与设备撤销的关键路径有指标。
func TestMetricsRecordLoginRefreshAndRevoke(t *testing.T) {
	ctx := context.Background()
	metrics := &recordingMetrics{}
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.Metrics = metrics
	})
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	login := f.login(t, "alice", "Correct#Passw0rd")
	if _, err := f.svc.Login(ctx, app.LoginInput{
		Subject: "alice", Password: "wrong-password", TraceID: "trace-metrics",
	}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("wrong password err = %v, want 401", err)
	}
	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken}); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if err := f.svc.RevokeSession(ctx, app.SessionActor{
		AccountID: login.AccountID,
		SessionID: login.SessionID,
	}, login.SessionID); err != nil {
		t.Fatalf("revoke session: %v", err)
	}

	if got := metrics.count("login"); got != 2 {
		t.Fatalf("login metric = %d, want 2", got)
	}
	if got := metrics.count("refresh"); got != 1 {
		t.Fatalf("refresh metric = %d, want 1", got)
	}
	if got := metrics.count("session_revoke"); got != 1 {
		t.Fatalf("session_revoke metric = %d, want 1", got)
	}
}

// countingAccounts 统计回源数据库次数，用于断言快照命中时不查库。
type countingAccounts struct {
	domain.AccountRepository
	findByID int
}

// FindByID 统计调用次数后委托给被包装仓储。
func (c *countingAccounts) FindByID(ctx context.Context, id string) (*domain.Account, error) {
	c.findByID++
	return c.AccountRepository.FindByID(ctx, id)
}

// recordingObserver 记录撤销快照写入失败的 op 序列。
type recordingObserver struct {
	ops []string
}

// OnAccountStateWriteFailure 追加失败操作名。
func (o *recordingObserver) OnAccountStateWriteFailure(_ context.Context, op string, _ error) {
	o.ops = append(o.ops, op)
}

// OnRevocationStoreFailure 追加读失败操作名。
func (o *recordingObserver) OnRevocationStoreFailure(_ context.Context, op string, _ error) {
	o.ops = append(o.ops, op)
}

// flakyRevocationStore 可分别模拟 SET / DEL 失败。
type flakyRevocationStore struct {
	*cache.MemoryRevocationStore
	failSave        bool
	failDelete      bool
	failSessionSave bool
}

// SaveAccountState 按 failSave 决定是否失败。
func (s *flakyRevocationStore) SaveAccountState(ctx context.Context, accountID string, state app.AccountState, ttl time.Duration) error {
	if s.failSave {
		return errStoreDown
	}
	return s.MemoryRevocationStore.SaveAccountState(ctx, accountID, state, ttl)
}

// DeleteAccountState 按 failDelete 决定是否失败。
func (s *flakyRevocationStore) DeleteAccountState(ctx context.Context, accountID string) error {
	if s.failDelete {
		return errStoreDown
	}
	return s.MemoryRevocationStore.DeleteAccountState(ctx, accountID)
}

// SaveSessionState 按 failSessionSave 决定是否失败。
func (s *flakyRevocationStore) SaveSessionState(ctx context.Context, sessionID string, state app.SessionState, ttl time.Duration) error {
	if s.failSessionSave {
		return errStoreDown
	}
	return s.MemoryRevocationStore.SaveSessionState(ctx, sessionID, state, ttl)
}

// TestRevocationInvalidatorFallsBackToDelete 验证 SET 失败时回退 DEL 并上报指标。
func TestRevocationInvalidatorFallsBackToDelete(t *testing.T) {
	ctx := context.Background()
	base := cache.NewMemoryRevocationStore()
	observer := &recordingObserver{}
	store := &flakyRevocationStore{MemoryRevocationStore: base, failSave: true}
	invalidator, err := app.NewRevocationInvalidator(store, time.Minute, observer)
	if err != nil {
		t.Fatalf("new invalidator: %v", err)
	}
	state := app.AccountState{Version: 2, Status: domain.AccountStatusActive}
	if err := invalidator.Invalidate(ctx, "acct-1", state); err != nil {
		t.Fatalf("delete fallback must succeed: %v", err)
	}
	if len(observer.ops) != 1 || observer.ops[0] != "set" {
		t.Fatalf("observer ops = %v, want [set]", observer.ops)
	}
	if _, hit, loadErr := base.LoadAccountState(ctx, "acct-1"); loadErr != nil || hit {
		t.Fatalf("snapshot must be deleted for next refetch, hit=%v err=%v", hit, loadErr)
	}
}

// TestRevocationInvalidatorReportsBothFailures 验证 SET 与 DEL 都失败时返回错误。
func TestRevocationInvalidatorReportsBothFailures(t *testing.T) {
	ctx := context.Background()
	observer := &recordingObserver{}
	store := &flakyRevocationStore{
		MemoryRevocationStore: cache.NewMemoryRevocationStore(),
		failSave:              true,
		failDelete:            true,
	}
	invalidator, err := app.NewRevocationInvalidator(store, time.Minute, observer)
	if err != nil {
		t.Fatalf("new invalidator: %v", err)
	}
	if err := invalidator.Invalidate(ctx, "acct-1", app.AccountState{Version: 2}); err == nil {
		t.Fatal("both failures must return error")
	}
	if len(observer.ops) != 2 || observer.ops[0] != "set" || observer.ops[1] != "delete" {
		t.Fatalf("observer ops = %v, want [set delete]", observer.ops)
	}
}

// TestLogoutAllToleratesRevocationCacheFailure 验证缓存整体不可用不改变登出全部的业务结果。
func TestLogoutAllToleratesRevocationCacheFailure(t *testing.T) {
	ctx := context.Background()
	observer := &recordingObserver{}
	store := &flakyRevocationStore{
		MemoryRevocationStore: cache.NewMemoryRevocationStore(),
		failSave:              true,
		failDelete:            true,
	}
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.Revocation = store
		cfg.RevocationObserver = observer
	})
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	login := f.login(t, "alice", "Correct#Passw0rd")
	actor := app.SessionActor{AccountID: login.AccountID, SessionID: login.SessionID}
	if err := f.svc.LogoutAll(ctx, actor); err != nil {
		t.Fatalf("logout-all must succeed even when cache write fails: %v", err)
	}
	account, err := f.accounts.FindByID(ctx, "acct-1")
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.TokenVersion != 2 {
		t.Fatalf("token version = %d, want 2", account.TokenVersion)
	}
	if len(observer.ops) != 2 {
		t.Fatalf("observer ops = %v, want set and delete failures", observer.ops)
	}
}

// brokenRevocationStore 模拟 Redis 不可用。
type brokenRevocationStore struct{}

// errStoreDown 表示撤销依赖故障。
var errStoreDown = errors.New("revocation store down")

func (brokenRevocationStore) LoadAccountState(context.Context, string) (app.AccountState, bool, error) {
	return app.AccountState{}, false, errStoreDown
}

func (brokenRevocationStore) SaveAccountState(context.Context, string, app.AccountState, time.Duration) error {
	return errStoreDown
}

func (brokenRevocationStore) DeleteAccountState(context.Context, string) error {
	return errStoreDown
}

func (brokenRevocationStore) LoadSessionState(context.Context, string) (app.SessionState, bool, error) {
	return app.SessionState{}, false, errStoreDown
}

func (brokenRevocationStore) SaveSessionState(context.Context, string, app.SessionState, time.Duration) error {
	return errStoreDown
}

func (brokenRevocationStore) DeleteSessionState(context.Context, string) error {
	return errStoreDown
}

func (brokenRevocationStore) RevokeToken(context.Context, string, time.Duration) error {
	return errStoreDown
}

func (brokenRevocationStore) IsTokenRevoked(context.Context, string) (bool, error) {
	return false, errStoreDown
}

// toggleRevocationStore 在测试中途把 Redis 从健康切换为故障（8.4 R-8.4-01）。
type toggleRevocationStore struct {
	inner *cache.MemoryRevocationStore
	down  bool
}

func (s *toggleRevocationStore) fail() error {
	if s.down {
		return errStoreDown
	}
	return nil
}

func (s *toggleRevocationStore) LoadAccountState(ctx context.Context, id string) (app.AccountState, bool, error) {
	if err := s.fail(); err != nil {
		return app.AccountState{}, false, err
	}
	return s.inner.LoadAccountState(ctx, id)
}

func (s *toggleRevocationStore) SaveAccountState(ctx context.Context, id string, state app.AccountState, ttl time.Duration) error {
	if err := s.fail(); err != nil {
		return err
	}
	return s.inner.SaveAccountState(ctx, id, state, ttl)
}

func (s *toggleRevocationStore) DeleteAccountState(ctx context.Context, id string) error {
	if err := s.fail(); err != nil {
		return err
	}
	return s.inner.DeleteAccountState(ctx, id)
}

func (s *toggleRevocationStore) LoadSessionState(ctx context.Context, id string) (app.SessionState, bool, error) {
	if err := s.fail(); err != nil {
		return app.SessionState{}, false, err
	}
	return s.inner.LoadSessionState(ctx, id)
}

func (s *toggleRevocationStore) SaveSessionState(ctx context.Context, id string, state app.SessionState, ttl time.Duration) error {
	if err := s.fail(); err != nil {
		return err
	}
	return s.inner.SaveSessionState(ctx, id, state, ttl)
}

func (s *toggleRevocationStore) DeleteSessionState(ctx context.Context, id string) error {
	if err := s.fail(); err != nil {
		return err
	}
	return s.inner.DeleteSessionState(ctx, id)
}

func (s *toggleRevocationStore) RevokeToken(ctx context.Context, jti string, ttl time.Duration) error {
	if err := s.fail(); err != nil {
		return err
	}
	return s.inner.RevokeToken(ctx, jti, ttl)
}

func (s *toggleRevocationStore) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	if err := s.fail(); err != nil {
		return false, err
	}
	return s.inner.IsTokenRevoked(ctx, jti)
}

func TestRevocationChecker(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

	t.Run("version_and_status", func(t *testing.T) {
		accounts := persistence.NewMemoryAccountRepository()
		store := cache.NewMemoryRevocationStore()
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := accounts.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		checker, err := app.NewRevocationChecker(accounts, store, app.RevocationFailClosed, time.Hour)
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}

		claims := jwt.Claims{Subject: "acct-1", ID: "jti-1", Extra: map[string]any{"ver": int64(1)}}
		if err := checker.Check(ctx, claims); err != nil {
			t.Fatalf("current version must pass: %v", err)
		}
		stale := jwt.Claims{Subject: "acct-1", ID: "jti-2", Extra: map[string]any{"ver": int64(0)}}
		requireAppCode(t, checker.Check(ctx, stale), apperr.CodeUnauthorized)

		missing := jwt.Claims{Subject: "acct-1", ID: "jti-3"}
		requireAppCode(t, checker.Check(ctx, missing), apperr.CodeUnauthorized)

		if err := store.RevokeToken(ctx, "jti-1", time.Hour); err != nil {
			t.Fatalf("revoke token: %v", err)
		}
		requireAppCode(t, checker.Check(ctx, claims), apperr.CodeUnauthorized)

		version := account.Version
		if err := account.Disable(now); err != nil {
			t.Fatalf("disable account: %v", err)
		}
		if err := accounts.Save(ctx, account, version); err != nil {
			t.Fatalf("save disabled account: %v", err)
		}
		// 撤销快照仍缓存着旧状态时会被误放行，因此这里换一个新存储验证 DB 回源。
		freshStore := cache.NewMemoryRevocationStore()
		freshChecker, err := app.NewRevocationChecker(accounts, freshStore, app.RevocationFailClosed, time.Hour)
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		active := jwt.Claims{Subject: "acct-1", ID: "jti-4", Extra: map[string]any{"ver": int64(account.TokenVersion)}}
		requireAppCode(t, freshChecker.Check(ctx, active), apperr.CodeUnauthorized)
	})

	t.Run("fail_closed_returns_50300_when_jti_blacklist_unavailable", func(t *testing.T) {
		accounts := persistence.NewMemoryAccountRepository()
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := accounts.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		checker, err := app.NewRevocationChecker(accounts, brokenRevocationStore{}, app.RevocationFailClosed, time.Hour)
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		err = checker.Check(ctx, jwt.Claims{Subject: "acct-1", ID: "jti-1", Extra: map[string]any{"ver": int64(1)}})
		requireAppCode(t, err, apperr.CodeServiceUnavailable)
		if !errors.Is(err, apperr.ErrUnavailable) {
			t.Fatalf("error must wrap ErrUnavailable: %v", err)
		}
	})

	t.Run("jti_grace_allows_recently_verified_token_during_outage", func(t *testing.T) {
		accounts := persistence.NewMemoryAccountRepository()
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := accounts.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		store := &toggleRevocationStore{inner: cache.NewMemoryRevocationStore()}
		clock := &fixedClock{now: now}
		checker, err := app.NewRevocationChecker(accounts, store, app.RevocationFailClosed, time.Minute,
			app.WithRevocationClock(clock), app.WithRevocationTokenGrace(30*time.Second))
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		claims := jwt.Claims{
			Subject: "acct-1", ID: "jti-grace",
			ExpiresAt: now.Add(time.Hour), Extra: map[string]any{"ver": int64(1)},
		}
		// 健康期完成一次校验，本地记录「未撤销」。
		if err := checker.Check(ctx, claims); err != nil {
			t.Fatalf("warm up check: %v", err)
		}
		store.down = true
		// 故障期在宽限内仍放行，避免全量 503。
		if err := checker.Check(ctx, claims); err != nil {
			t.Fatalf("grace must allow recently verified token: %v", err)
		}
		// 超出宽限必须失败关闭。
		clock.Advance(31 * time.Second)
		requireAppCode(t, checker.Check(ctx, claims), apperr.CodeServiceUnavailable)
		// 未验证过的 jti 在故障期不得借宽限放行。
		fresh := jwt.Claims{
			Subject: "acct-1", ID: "jti-new",
			ExpiresAt: now.Add(2 * time.Hour), Extra: map[string]any{"ver": int64(1)},
		}
		requireAppCode(t, checker.Check(ctx, fresh), apperr.CodeServiceUnavailable)
	})

	t.Run("jti_grace_capped_by_token_expiry", func(t *testing.T) {
		accounts := persistence.NewMemoryAccountRepository()
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := accounts.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		store := &toggleRevocationStore{inner: cache.NewMemoryRevocationStore()}
		clock := &fixedClock{now: now}
		checker, err := app.NewRevocationChecker(accounts, store, app.RevocationFailClosed, time.Minute,
			app.WithRevocationClock(clock), app.WithRevocationTokenGrace(30*time.Second))
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		// 令牌剩余寿命短于宽限：本地记录必须以令牌到期时间为上限。
		claims := jwt.Claims{
			Subject: "acct-1", ID: "jti-short",
			ExpiresAt: now.Add(10 * time.Second), Extra: map[string]any{"ver": int64(1)},
		}
		if err := checker.Check(ctx, claims); err != nil {
			t.Fatalf("warm up check: %v", err)
		}
		store.down = true
		if err := checker.Check(ctx, claims); err != nil {
			t.Fatalf("grace must cover unexpired token: %v", err)
		}
		clock.Advance(11 * time.Second)
		requireAppCode(t, checker.Check(ctx, claims), apperr.CodeServiceUnavailable)
	})

	t.Run("jti_grace_capped_by_config_max", func(t *testing.T) {
		accounts := persistence.NewMemoryAccountRepository()
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := accounts.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		store := &toggleRevocationStore{inner: cache.NewMemoryRevocationStore()}
		clock := &fixedClock{now: now}
		// 传入超过上限的宽限必须被夹到 60s。
		checker, err := app.NewRevocationChecker(accounts, store, app.RevocationFailClosed, 2*time.Minute,
			app.WithRevocationClock(clock), app.WithRevocationTokenGrace(90*time.Second))
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		claims := jwt.Claims{
			Subject: "acct-1", ID: "jti-clamp",
			ExpiresAt: now.Add(2 * time.Hour), Extra: map[string]any{"ver": int64(1)},
		}
		if err := checker.Check(ctx, claims); err != nil {
			t.Fatalf("warm up check: %v", err)
		}
		store.down = true
		clock.Advance(61 * time.Second)
		requireAppCode(t, checker.Check(ctx, claims), apperr.CodeServiceUnavailable)
	})

	t.Run("jti_grace_sweeps_expired_entries", func(t *testing.T) {
		accounts := persistence.NewMemoryAccountRepository()
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := accounts.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		store := &toggleRevocationStore{inner: cache.NewMemoryRevocationStore()}
		clock := &fixedClock{now: now}
		checker, err := app.NewRevocationChecker(accounts, store, app.RevocationFailClosed, time.Minute,
			app.WithRevocationClock(clock), app.WithRevocationTokenGrace(time.Second))
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		// 写入足够多的 jti 以触发一次过期清理，避免本地记录无限增长。
		for i := 0; i < 4200; i++ {
			claims := jwt.Claims{
				Subject: "acct-1", ID: "jti-sweep-" + strconv.Itoa(i),
				ExpiresAt: now.Add(time.Hour), Extra: map[string]any{"ver": int64(1)},
			}
			if err := checker.Check(ctx, claims); err != nil {
				t.Fatalf("check %d: %v", i, err)
			}
		}
	})

	t.Run("jti_grace_disabled_keeps_fail_closed", func(t *testing.T) {
		accounts := persistence.NewMemoryAccountRepository()
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := accounts.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		store := &toggleRevocationStore{inner: cache.NewMemoryRevocationStore()}
		checker, err := app.NewRevocationChecker(accounts, store, app.RevocationFailClosed, time.Minute,
			app.WithRevocationClock(&fixedClock{now: now}), app.WithRevocationTokenGrace(-time.Second))
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		claims := jwt.Claims{
			Subject: "acct-1", ID: "jti-grace-off",
			ExpiresAt: now.Add(time.Hour), Extra: map[string]any{"ver": int64(1)},
		}
		if err := checker.Check(ctx, claims); err != nil {
			t.Fatalf("warm up check: %v", err)
		}
		store.down = true
		requireAppCode(t, checker.Check(ctx, claims), apperr.CodeServiceUnavailable)
	})

	t.Run("account_snapshot_falls_back_to_db_when_store_down", func(t *testing.T) {
		accounts := persistence.NewMemoryAccountRepository()
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := accounts.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		checker, err := app.NewRevocationChecker(accounts, brokenRevocationStore{}, app.RevocationFailClosed, time.Hour)
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		// 不带 jti：只校验账号快照，Redis 不可用时必须回退 DB 并放行。
		if err := checker.Check(ctx, jwt.Claims{Subject: "acct-1", Extra: map[string]any{"ver": int64(1)}}); err != nil {
			t.Fatalf("account snapshot must fall back to db: %v", err)
		}
	})

	t.Run("fail_open_allows_but_missing_account_is_denied", func(t *testing.T) {
		accounts := persistence.NewMemoryAccountRepository()
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := accounts.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		checker, err := app.NewRevocationChecker(accounts, brokenRevocationStore{}, app.RevocationFailOpen, time.Hour)
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		if err := checker.Check(ctx, jwt.Claims{Subject: "acct-1", ID: "jti-1", Extra: map[string]any{"ver": int64(1)}}); err != nil {
			t.Fatalf("fail_open must allow when store is down: %v", err)
		}

		// 账号不存在属于确定性拒绝，fail_open 不得放行。
		missingChecker, err := app.NewRevocationChecker(
			persistence.NewMemoryAccountRepository(), cache.NewMemoryRevocationStore(), app.RevocationFailOpen, time.Hour)
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		requireAppCode(t, missingChecker.Check(ctx, jwt.Claims{Subject: "ghost", Extra: map[string]any{"ver": int64(1)}}),
			apperr.CodeUnauthorized)
	})

	t.Run("stale_snapshot_refetches_database", func(t *testing.T) {
		accounts := persistence.NewMemoryAccountRepository()
		store := cache.NewMemoryRevocationStore()
		clock := &fixedClock{now: now}
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := accounts.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		checker, err := app.NewRevocationChecker(accounts, store, app.RevocationFailClosed, 30*time.Second,
			app.WithRevocationClock(clock))
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		// 预热一份已超过陈旧度的 active 快照，模拟数据库被直接改动而缓存未刷新。
		stale := app.AccountState{
			Version:   account.TokenVersion,
			Status:    domain.AccountStatusActive,
			FetchedAt: now.Add(-time.Minute),
		}
		if err := store.SaveAccountState(ctx, "acct-1", stale, time.Hour); err != nil {
			t.Fatalf("prime snapshot: %v", err)
		}
		version := account.Version
		if err := account.Disable(now); err != nil {
			t.Fatalf("disable account: %v", err)
		}
		if err := accounts.Save(ctx, account, version); err != nil {
			t.Fatalf("save disabled account: %v", err)
		}

		claims := jwt.Claims{Subject: "acct-1", ID: "jti-1", Extra: map[string]any{"ver": int64(stale.Version)}}
		requireAppCode(t, checker.Check(ctx, claims), apperr.CodeUnauthorized)
	})

	t.Run("fresh_snapshot_avoids_database", func(t *testing.T) {
		base := persistence.NewMemoryAccountRepository()
		account, err := domain.NewAccount(domain.NewAccountParams{ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now})
		if err != nil {
			t.Fatalf("new account: %v", err)
		}
		if err := base.Save(ctx, account, 0); err != nil {
			t.Fatalf("save account: %v", err)
		}
		counted := &countingAccounts{AccountRepository: base}
		store := cache.NewMemoryRevocationStore()
		clock := &fixedClock{now: now}
		fresh := app.AccountState{
			Version:   account.TokenVersion,
			Status:    domain.AccountStatusActive,
			FetchedAt: now,
		}
		if err := store.SaveAccountState(ctx, "acct-1", fresh, time.Hour); err != nil {
			t.Fatalf("prime snapshot: %v", err)
		}
		checker, err := app.NewRevocationChecker(counted, store, app.RevocationFailClosed, 30*time.Second,
			app.WithRevocationClock(clock))
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		claims := jwt.Claims{Subject: "acct-1", ID: "jti-1", Extra: map[string]any{"ver": int64(fresh.Version)}}
		if err := checker.Check(ctx, claims); err != nil {
			t.Fatalf("fresh snapshot must pass: %v", err)
		}
		if counted.findByID != 0 {
			t.Fatalf("fresh snapshot must not hit database, calls=%d", counted.findByID)
		}
	})

	t.Run("claims_without_subject_are_rejected", func(t *testing.T) {
		checker, err := app.NewRevocationChecker(
			persistence.NewMemoryAccountRepository(), cache.NewMemoryRevocationStore(), app.RevocationFailClosed, time.Hour)
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		requireAppCode(t, checker.Check(ctx, jwt.Claims{ID: "jti-1"}), apperr.CodeUnauthorized)
	})
}

// TestRevokeSessionToleratesSessionStateWriteFailure 验证会话快照写失败不改变已提交的撤销结果。
func TestRevokeSessionToleratesSessionStateWriteFailure(t *testing.T) {
	ctx := context.Background()
	observer := &recordingObserver{}
	store := &flakyRevocationStore{
		MemoryRevocationStore: cache.NewMemoryRevocationStore(),
		failSessionSave:       true,
	}
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.Revocation = store
		cfg.RevocationObserver = observer
	})
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	first := f.login(t, "alice", "Correct#Passw0rd")
	second := f.login(t, "alice", "Correct#Passw0rd")

	actor := app.SessionActor{AccountID: first.AccountID, SessionID: first.SessionID}
	if err := f.svc.RevokeSession(ctx, actor, second.SessionID); err != nil {
		t.Fatalf("revoke must succeed even when session snapshot write fails: %v", err)
	}
	if session := f.sessionOf(t, second.SessionID); session.Status != domain.SessionStatusRevoked {
		t.Fatalf("session status = %s, want revoked", session.Status)
	}
	if len(observer.ops) != 3 {
		t.Fatalf("observer ops = %v, want two warm writes and one revoke write", observer.ops)
	}
	for _, op := range observer.ops {
		if op != "session_set" {
			t.Fatalf("unexpected op %q", op)
		}
	}

	// 缓存缺失时 Checker 必须回源数据库，仍然拒绝被撤销会话。
	checker, err := app.NewRevocationChecker(f.accounts, store, app.RevocationFailClosed, 0,
		app.WithRevocationSessions(f.sessions), app.WithRevocationClock(f.clock))
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	requireAppCode(t, checker.Check(ctx, sessionClaims(second.AccountID, second.SessionID, "jti-second", 1)),
		apperr.CodeUnauthorized)
}

// sessionClaims 构造带 sid 的访问令牌声明，用于会话级撤销断言。
func sessionClaims(accountID, sessionID, jti string, version int64) jwt.Claims {
	return jwt.Claims{
		Subject: accountID,
		ID:      jti,
		Extra:   map[string]any{"ver": version, "sid": sessionID},
	}
}

// TestRevokeSessionBlocksAccessToken 覆盖撤销非当前会话后，该设备 Access Token 立即被拒绝（IDN-P1-02）。
func TestRevokeSessionBlocksAccessToken(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	first := f.login(t, "alice", "Correct#Passw0rd")
	second := f.login(t, "alice", "Correct#Passw0rd")

	checker, err := app.NewRevocationChecker(f.accounts, f.store, app.RevocationFailClosed, 0,
		app.WithRevocationSessions(f.sessions), app.WithRevocationClock(f.clock))
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	firstClaims := sessionClaims(first.AccountID, first.SessionID, "jti-first", 1)
	secondClaims := sessionClaims(second.AccountID, second.SessionID, "jti-second", 1)
	if checkErr := checker.Check(ctx, firstClaims); checkErr != nil {
		t.Fatalf("first session must pass: %v", checkErr)
	}
	if checkErr := checker.Check(ctx, secondClaims); checkErr != nil {
		t.Fatalf("second session must pass: %v", checkErr)
	}

	actor := app.SessionActor{
		AccountID:      first.AccountID,
		SessionID:      first.SessionID,
		TokenID:        "jti-first",
		TokenExpiresAt: f.clock.Now().Add(time.Minute),
	}
	if revokeErr := f.svc.RevokeSession(ctx, actor, second.SessionID); revokeErr != nil {
		t.Fatalf("revoke session: %v", revokeErr)
	}

	requireAppCode(t, checker.Check(ctx, secondClaims), apperr.CodeUnauthorized)
	if checkErr := checker.Check(ctx, firstClaims); checkErr != nil {
		t.Fatalf("current session must still pass: %v", checkErr)
	}
	if session := f.sessionOf(t, second.SessionID); session.Status != domain.SessionStatusRevoked {
		t.Fatalf("session status = %s, want revoked", session.Status)
	}
}

// TestRefreshReplayRevokesFamilyAccessTokens 覆盖重放撤销整链后，family 内 Access Token 全部被拒绝。
func TestRefreshReplayRevokesFamilyAccessTokens(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	login := f.login(t, "alice", "Correct#Passw0rd")

	refreshed, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	next, err := f.sessions.FindByRefreshHash(ctx, refreshed.RefreshHash)
	if err != nil {
		t.Fatalf("load refreshed session: %v", err)
	}

	checker, err := app.NewRevocationChecker(f.accounts, f.store, app.RevocationFailClosed, 0,
		app.WithRevocationSessions(f.sessions), app.WithRevocationClock(f.clock))
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	oldClaims := sessionClaims(login.AccountID, login.SessionID, "jti-old", 1)
	newClaims := sessionClaims(login.AccountID, next.ID, "jti-next", 1)
	if checkErr := checker.Check(ctx, oldClaims); checkErr != nil {
		t.Fatalf("rotated session token must pass before replay: %v", checkErr)
	}
	if checkErr := checker.Check(ctx, newClaims); checkErr != nil {
		t.Fatalf("new session token must pass: %v", checkErr)
	}

	// 旧刷新令牌再次出现即重放：整条 family 被撤销，已签发的 Access Token 同步失效。
	_, replayErr := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken})
	requireAppCode(t, replayErr, apperr.CodeUnauthorized)
	requireAppCode(t, checker.Check(ctx, oldClaims), apperr.CodeUnauthorized)
	requireAppCode(t, checker.Check(ctx, newClaims), apperr.CodeUnauthorized)
}

// TestRevocationCheckerSessionSnapshotStaleness 覆盖缓存预热后 DB 直接撤销，超过陈旧度必须回源拒绝。
func TestRevocationCheckerSessionSnapshotStaleness(t *testing.T) {
	ctx := context.Background()
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	login := f.login(t, "alice", "Correct#Passw0rd")

	checker, err := app.NewRevocationChecker(f.accounts, f.store, app.RevocationFailClosed, 5*time.Second,
		app.WithRevocationSessions(f.sessions), app.WithRevocationClock(f.clock))
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	claims := sessionClaims(login.AccountID, login.SessionID, "jti-1", 1)
	if checkErr := checker.Check(ctx, claims); checkErr != nil {
		t.Fatalf("active session must pass: %v", checkErr)
	}

	// 直接改库：模拟其它副本或运维撤销会话，不刷新缓存。
	session := f.sessionOf(t, login.SessionID)
	expected := session.Version
	if revokeErr := session.Revoke(domain.RevokeReasonAdmin, f.clock.Now()); revokeErr != nil {
		t.Fatalf("revoke session: %v", revokeErr)
	}
	if saveErr := f.sessions.Save(ctx, session, expected); saveErr != nil {
		t.Fatalf("save revoked session: %v", saveErr)
	}

	// 快照仍新鲜：允许命中（≤state_ttl 的最终一致窗口）。
	if checkErr := checker.Check(ctx, claims); checkErr != nil {
		t.Fatalf("fresh snapshot window must still pass: %v", checkErr)
	}
	f.clock.Advance(6 * time.Second)
	requireAppCode(t, checker.Check(ctx, claims), apperr.CodeUnauthorized)
}

// TestSessionCleanupExpiresAndDeletes 覆盖保留期清理：先标记过期，超过保留期后删除。
func TestSessionCleanupExpiresAndDeletes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	sessions := persistence.NewMemorySessionRepository()
	attempts := persistence.NewMemoryLoginAttemptRepository()

	family, err := domain.NewSessionFamily(domain.NewSessionFamilyParams{
		ID:        "fam-1",
		AccountID: "acct-1",
		ExpiresAt: now.Add(time.Hour),
		Now:       now,
	})
	if err != nil {
		t.Fatalf("new family: %v", err)
	}
	if err := sessions.SaveFamily(ctx, family, 0); err != nil {
		t.Fatalf("save family: %v", err)
	}
	session, err := domain.NewSession(domain.NewSessionParams{
		ID:           "fam-1",
		AccountID:    "acct-1",
		FamilyID:     "fam-1",
		RefreshHash:  "hash-1",
		TokenVersion: 1,
		ExpiresAt:    now.Add(time.Hour),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := sessions.Save(ctx, session, 0); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if err := attempts.Append(ctx, &domain.LoginAttempt{
		Channel:    domain.ChannelAdmin,
		AccountRef: "ref-1",
		Outcome:    domain.LoginOutcomeFailure,
		OccurredAt: now.Add(-200 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("append attempt: %v", err)
	}

	cleanup, err := app.NewSessionCleanup(sessions, attempts, app.RetentionPolicy{
		SessionRetention:      30 * 24 * time.Hour,
		LoginAttemptRetention: 180 * 24 * time.Hour,
	}, nil)
	if err != nil {
		t.Fatalf("new cleanup: %v", err)
	}

	first, err := cleanup.Run(ctx, now.Add(31*24*time.Hour))
	if err != nil {
		t.Fatalf("cleanup first run: %v", err)
	}
	if first.ExpiredSessions != 1 || first.ExpiredFamilies != 1 {
		t.Fatalf("expire stats = %+v, want 1 session and 1 family", first)
	}
	if first.DeletedLoginAttempts != 1 {
		t.Fatalf("login attempts deleted = %d, want 1", first.DeletedLoginAttempts)
	}
	if first.DeletedSessions != 0 || first.DeletedFamilies != 0 {
		t.Fatalf("expired rows must be kept within retention: %+v", first)
	}
	count, err := attempts.CountFailuresSince(ctx, domain.ChannelAdmin, "ref-1", now.Add(-300*24*time.Hour))
	if err != nil || count != 0 {
		t.Fatalf("old attempts must be deleted, count=%d err=%v", count, err)
	}

	second, err := cleanup.Run(ctx, now.Add(62*24*time.Hour))
	if err != nil {
		t.Fatalf("cleanup second run: %v", err)
	}
	// 默认路径为归档 + 在线删除；第二轮的过期行应计入 Archived*，在线查询不再可见。
	if second.ArchivedSessions != 1 || second.ArchivedFamilies != 1 {
		t.Fatalf("archive stats = %+v, want 1 session and 1 family", second)
	}
	if second.DeletedSessions != 0 || second.DeletedFamilies != 0 {
		t.Fatalf("archive path must not report deleted stats: %+v", second)
	}
	if _, err := sessions.FindFamily(ctx, "fam-1"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("expired family must be deleted, got %v", err)
	}
}

// TestLoginDummyVerifyAlignment 断言账号不存在 / 状态拒绝时执行占位校验（IDN-P2-04）。
func TestLoginDummyVerifyAlignment(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown account", func(t *testing.T) {
		hasher := &countingHasher{}
		passwordSvc, err := app.NewPasswordService(hasher, app.DefaultPasswordPolicy())
		if err != nil {
			t.Fatalf("new password service: %v", err)
		}
		f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
			cfg.Password = passwordSvc
		})
		_, err = f.svc.Login(ctx, app.LoginInput{
			Subject: "missing@example.com", Password: "Whatever#123", TraceID: "trace-timing-missing",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
		if got := hasher.count(); got != 1 {
			t.Fatalf("dummy verify count = %d, want 1", got)
		}
	})

	t.Run("locked account", func(t *testing.T) {
		hasher := &countingHasher{}
		passwordSvc, err := app.NewPasswordService(hasher, app.DefaultPasswordPolicy())
		if err != nil {
			t.Fatalf("new password service: %v", err)
		}
		f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
			cfg.Password = passwordSvc
		})
		f.seedAccount(t, "acct-timing-locked", "locked@example.com", "Correct#Passw0rd")
		account, loadErr := f.accounts.FindByID(ctx, "acct-timing-locked")
		if loadErr != nil {
			t.Fatalf("load account: %v", loadErr)
		}
		expected := account.Version
		if lockErr := account.Lock(f.clock.Now().Add(time.Hour), f.clock.Now()); lockErr != nil {
			t.Fatalf("lock account: %v", lockErr)
		}
		if saveErr := f.accounts.Save(ctx, account, expected); saveErr != nil {
			t.Fatalf("save locked account: %v", saveErr)
		}

		_, err = f.svc.Login(ctx, app.LoginInput{
			Subject: "locked@example.com", Password: "Correct#Passw0rd", TraceID: "trace-timing-locked",
		})
		requireAppCode(t, err, apperr.CodeUnauthorized)
		if got := hasher.count(); got != 1 {
			t.Fatalf("dummy verify count = %d, want 1", got)
		}
	})
}

// 确保测试助手未被剪枝：刷新令牌摘要与仓储哈希一致。
func TestFixtureRefreshHashMatchesRepository(t *testing.T) {
	f := newAuthFixture(t, domain.ChannelAdmin)
	f.seedAccount(t, "acct-1", "alice", "Correct#Passw0rd")
	login := f.login(t, "alice", "Correct#Passw0rd")
	if !strings.HasPrefix(login.RefreshToken, app.RefreshPrefixAdmin) {
		t.Fatalf("unexpected refresh prefix: %q", login.RefreshToken)
	}
	hash, err := app.HashRefreshToken(login.RefreshToken)
	if err != nil {
		t.Fatalf("hash refresh token: %v", err)
	}
	if hash != login.RefreshHash {
		t.Fatal("returned refresh hash must be the repository digest")
	}
}
