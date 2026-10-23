package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
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

// Algorithm 返回测试算法标识。
func (fakeHasher) Algorithm() password.Algorithm { return password.AlgorithmArgon2id }

// authFixture 聚合认证用例所需的真实内存实现。
type authFixture struct {
	svc         *app.AuthService
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
		Audit:       audit.SlogEmitter{Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
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
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      id,
		Channel: f.channel,
		Subject: subject,
		Now:     f.clock.Now(),
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
	result, err := f.svc.Login(context.Background(), app.LoginInput{
		Subject:  subject,
		Password: plainPassword,
		TraceID:  "trace-login",
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

			result, err := f.svc.Login(ctx, app.LoginInput{Subject: "  Alice@Example.com  ", Password: "Correct#Passw0rd"})
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
	views, err := f.svc.ListSessions(ctx, actor)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
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

func (brokenRevocationStore) RevokeToken(context.Context, string, time.Duration) error {
	return errStoreDown
}

func (brokenRevocationStore) IsTokenRevoked(context.Context, string) (bool, error) {
	return false, errStoreDown
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

	t.Run("fail_closed_returns_50300", func(t *testing.T) {
		checker, err := app.NewRevocationChecker(persistence.NewMemoryAccountRepository(), brokenRevocationStore{}, app.RevocationFailClosed, time.Hour)
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		err = checker.Check(ctx, jwt.Claims{Subject: "acct-1", ID: "jti-1", Extra: map[string]any{"ver": int64(1)}})
		requireAppCode(t, err, apperr.CodeServiceUnavailable)
		if !errors.Is(err, apperr.ErrUnavailable) {
			t.Fatalf("error must wrap ErrUnavailable: %v", err)
		}
	})

	t.Run("fail_open_allows_but_missing_account_is_denied", func(t *testing.T) {
		checker, err := app.NewRevocationChecker(persistence.NewMemoryAccountRepository(), brokenRevocationStore{}, app.RevocationFailOpen, time.Hour)
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

	t.Run("claims_without_subject_are_rejected", func(t *testing.T) {
		checker, err := app.NewRevocationChecker(
			persistence.NewMemoryAccountRepository(), cache.NewMemoryRevocationStore(), app.RevocationFailClosed, time.Hour)
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}
		requireAppCode(t, checker.Check(ctx, jwt.Claims{ID: "jti-1"}), apperr.CodeUnauthorized)
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
