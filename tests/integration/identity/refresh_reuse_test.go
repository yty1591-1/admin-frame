package identity_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	identitycache "github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/jwt"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/password"
)

// seqIDs 是并发安全的自增主键生成器（会话 / 事件 ID）。
type seqIDs struct {
	mu sync.Mutex
	n  int64
}

// NextString 返回下一个十进制序号。
func (s *seqIDs) NextString() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return strconv.FormatInt(s.n, 10), nil
}

// sqlAuthFixture 聚合使用真实 MySQL 仓储与事务边界的认证服务。
type sqlAuthFixture struct {
	svc      *app.AuthService
	accounts *persistence.SQLAccountRepository
	sessions *persistence.SQLSessionRepository
	password *app.PasswordService
	sqlDB    *sql.DB
	channel  domain.Channel
}

// newSQLAuthFixture 按生产装配方式构造认证服务：
// SQL 六仓储 + SQL UnitOfWork + Outbox 审计出口（允许断言 async_outbox）。
func newSQLAuthFixture(t *testing.T, sqlDB *sql.DB, channel domain.Channel) *sqlAuthFixture {
	t.Helper()
	ids := &seqIDs{}
	accounts := persistence.NewSQLAccountRepository(sqlDB, persistence.WithSubjectProtector(newIntegrationProtector(t)))
	credentials := persistence.NewSQLCredentialRepository(sqlDB)
	sessions := persistence.NewSQLSessionRepository(sqlDB)
	attempts := persistence.NewSQLLoginAttemptRepository(sqlDB)

	hasher, err := password.New(password.Config{})
	if err != nil {
		t.Fatalf("new password hasher: %v", err)
	}
	passwordSvc, err := app.NewPasswordService(hasher, app.DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("new password service: %v", err)
	}
	guard, err := app.NewLoginGuard(accounts, attempts, ids, app.DefaultLockoutPolicy())
	if err != nil {
		t.Fatalf("new login guard: %v", err)
	}
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "https://identity.test",
		Audience: []string{string(channel)},
		Secret:   []byte("01234567890123456789012345678901"),
		TTL:      15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	tokens, err := app.NewTokenService(signer, channel, 15*time.Minute, 168*time.Hour, ids,
		app.WithTokenParser(signer))
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	svc, err := app.NewAuthService(app.AuthServiceConfig{
		Accounts:    accounts,
		Credentials: credentials,
		Sessions:    sessions,
		Guard:       guard,
		Password:    passwordSvc,
		Tokens:      tokens,
		Revocation:  identitycache.NewMemoryRevocationStore(),
		Audit:       app.OutboxEmitter{Events: outbox.NewMySQLStore(sqlDB)},
		IDs:         ids,
		Channel:     channel,
		UoW:         persistence.NewSQLUnitOfWork(sqlDB),
		IdleTimeout: 72 * time.Hour,
		Service:     "identity-integration",
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	return &sqlAuthFixture{
		svc:      svc,
		accounts: accounts,
		sessions: sessions,
		password: passwordSvc,
		sqlDB:    sqlDB,
		channel:  channel,
	}
}

// seedPasswordAccount 落库账号与真实 Argon2id 密码凭据。
func (f *sqlAuthFixture) seedPasswordAccount(t *testing.T, accountID, subject, plainPassword string) {
	t.Helper()
	now := time.Now().UTC()
	seedAccount(t, f.accounts, f.channel, accountID, subject, now)

	encoded, algorithm, err := f.password.Hash(plainPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	credential, err := domain.NewCredential(domain.NewCredentialParams{
		ID:        nextID(),
		AccountID: accountID,
		Type:      domain.CredentialTypePassword,
		Hash:      encoded,
		Algorithm: algorithm,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("new credential: %v", err)
	}
	if err := persistence.NewSQLCredentialRepository(f.sqlDB).Save(context.Background(), credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}
}

// login 执行一次成功登录并返回结果。
func (f *sqlAuthFixture) login(t *testing.T, subject, plainPassword string) app.LoginResult {
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

// reloadSessions 读取账号全部会话，供家庭链撤销断言。
func (f *sqlAuthFixture) reloadSessions(t *testing.T, accountID string) []*domain.Session {
	t.Helper()
	sessions, err := f.sessions.ListByAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return sessions
}

// TestRefreshReuseRevokesFamily 覆盖刷新令牌重放：整条家庭链撤销 + 审计入库。
func TestRefreshReuseRevokesFamily(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	f := newSQLAuthFixture(t, sqlDB, domain.ChannelC)

	accountID := nextID()
	subject := "+86" + accountID[len(accountID)-11:]
	const plainPassword = "Correct#Passw0rd"
	f.seedPasswordAccount(t, accountID, subject, plainPassword)

	login := f.login(t, subject, plainPassword)
	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken, TraceID: "trace-refresh"}); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if _, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken, TraceID: "trace-replay"}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("refresh replay must return 401, got %v", err)
	}

	sessions := f.reloadSessions(t, accountID)
	if len(sessions) != 2 {
		t.Fatalf("expected login + refreshed session, got %d", len(sessions))
	}
	for _, session := range sessions {
		if session.Status != domain.SessionStatusRevoked {
			t.Fatalf("session %s must be revoked after replay, status=%s", session.ID, session.Status)
		}
		if session.RevokedReason != domain.RevokeReasonReplay {
			t.Fatalf("session %s revoked reason=%s, want replay", session.ID, session.RevokedReason)
		}
	}

	var reused int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM async_outbox
		 WHERE topic = ? AND event_type = ?
		   AND JSON_UNQUOTE(JSON_EXTRACT(payload, '$.action')) = ?`,
		app.TopicAudit, app.EventTypeAudit, audit.ActionAuthTokenReused,
	).Scan(&reused); err != nil {
		t.Fatalf("count reused audit events: %v", err)
	}
	if reused != 1 {
		t.Fatalf("expected one auth.token.reused outbox event, got %d", reused)
	}
}

// TestConcurrentRefreshOnlyOneSucceeds 覆盖并发刷新：乐观锁保证仅一个请求成功。
func TestConcurrentRefreshOnlyOneSucceeds(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	f := newSQLAuthFixture(t, sqlDB, domain.ChannelC)

	accountID := nextID()
	subject := "+86" + accountID[len(accountID)-11:]
	const plainPassword = "Correct#Passw0rd"
	f.seedPasswordAccount(t, accountID, subject, plainPassword)
	login := f.login(t, subject, plainPassword)

	const goroutines = 10
	var (
		wg           sync.WaitGroup
		success      int32
		unauthorized int32
		errMu        sync.Mutex
		otherErr     error
	)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.svc.Refresh(ctx, app.RefreshInput{
				RefreshToken: login.RefreshToken,
				TraceID:      "trace-concurrent",
			})
			switch {
			case err == nil:
				atomic.AddInt32(&success, 1)
			case errors.Is(err, apperr.ErrUnauthorized):
				atomic.AddInt32(&unauthorized, 1)
			default:
				errMu.Lock()
				if otherErr == nil {
					otherErr = err
				}
				errMu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if otherErr != nil {
		t.Fatalf("unexpected refresh error: %v", otherErr)
	}
	if success != 1 {
		t.Fatalf("expected exactly one successful refresh, got %d (401: %d)", success, unauthorized)
	}
	if unauthorized != goroutines-1 {
		t.Fatalf("expected %d unauthorized responses, got %d", goroutines-1, unauthorized)
	}

	sessions := f.reloadSessions(t, accountID)
	if len(sessions) < 2 {
		t.Fatalf("expected a rotated session and its successor, got %d", len(sessions))
	}
	for _, session := range sessions {
		if session.Status != domain.SessionStatusRevoked {
			t.Fatalf("session %s must be revoked after concurrent replay, status=%s", session.ID, session.Status)
		}
	}
}
