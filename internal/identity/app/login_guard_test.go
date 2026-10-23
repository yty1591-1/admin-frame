package app_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// guardBaseTime 是登录用例的固定时间基准。
var guardBaseTime = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// seqIDs 是确定性 ID 生成器。
type seqIDs struct {
	mu sync.Mutex
	n  int64
}

func (s *seqIDs) NextString() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return strconv.FormatInt(s.n, 10), nil
}

// stubLimiter 是可控的登录限流器。
type stubLimiter struct {
	allowed    bool
	retryAfter time.Duration
	lastKey    string
}

func (l *stubLimiter) Allow(key string) (bool, time.Duration) {
	l.lastKey = key
	return l.allowed, l.retryAfter
}

// guardFixture 装配登录守卫与内存仓储。
type guardFixture struct {
	accounts *persistence.MemoryAccountRepository
	attempts *persistence.MemoryLoginAttemptRepository
	guard    *app.LoginGuard
}

func newGuardFixture(t *testing.T, policy app.LockoutPolicy, opts ...app.LoginGuardOption) *guardFixture {
	t.Helper()
	accounts := persistence.NewMemoryAccountRepository()
	attempts := persistence.NewMemoryLoginAttemptRepository()
	guard, err := app.NewLoginGuard(accounts, attempts, &seqIDs{}, policy, opts...)
	if err != nil {
		t.Fatalf("NewLoginGuard() error = %v", err)
	}
	return &guardFixture{accounts: accounts, attempts: attempts, guard: guard}
}

// newActiveAccount 创建并落库一个已激活账号。
func (f *guardFixture) newActiveAccount(t *testing.T) *domain.Account {
	t.Helper()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      "1001",
		Channel: domain.ChannelAdmin,
		Subject: "Alice",
		Now:     guardBaseTime,
	})
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	if err := account.Activate(guardBaseTime); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if err := f.accounts.Save(context.Background(), account, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return account
}

func TestNewLoginGuardValidation(t *testing.T) {
	accounts := persistence.NewMemoryAccountRepository()
	attempts := persistence.NewMemoryLoginAttemptRepository()
	ids := &seqIDs{}

	tests := []struct {
		name     string
		accounts domain.AccountRepository
		attempts domain.LoginAttemptRepository
		ids      app.IDGenerator
		policy   app.LockoutPolicy
		wantErr  bool
	}{
		{name: "valid", accounts: accounts, attempts: attempts, ids: ids, policy: app.DefaultLockoutPolicy()},
		{name: "missing accounts", attempts: attempts, ids: ids, policy: app.DefaultLockoutPolicy(), wantErr: true},
		{name: "missing attempts", accounts: accounts, ids: ids, policy: app.DefaultLockoutPolicy(), wantErr: true},
		{name: "missing ids", accounts: accounts, attempts: attempts, policy: app.DefaultLockoutPolicy(), wantErr: true},
		{
			name:     "invalid policy",
			accounts: accounts,
			attempts: attempts,
			ids:      ids,
			policy:   app.LockoutPolicy{MaxFailures: 0, Window: time.Minute, Duration: time.Minute},
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := app.NewLoginGuard(tt.accounts, tt.attempts, tt.ids, tt.policy)
			if tt.wantErr && err == nil {
				t.Fatal("NewLoginGuard() error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("NewLoginGuard() error = %v, want nil", err)
			}
		})
	}
}

func TestLoginGuardAuthorize(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*domain.Account)
		nilArg  bool
		wantErr error
	}{
		{name: "active allowed"},
		{name: "pending allowed", setup: func(a *domain.Account) { a.Status = domain.AccountStatusPending }},
		{
			name: "locked rejected",
			setup: func(a *domain.Account) {
				a.Status = domain.AccountStatusLocked
				until := guardBaseTime.Add(time.Minute)
				a.LockedUntil = &until
			},
			wantErr: domain.ErrAccountLocked,
		},
		{
			name: "expired lock allowed",
			setup: func(a *domain.Account) {
				a.Status = domain.AccountStatusLocked
				until := guardBaseTime.Add(-time.Minute)
				a.LockedUntil = &until
			},
		},
		{name: "disabled rejected", setup: func(a *domain.Account) { a.Status = domain.AccountStatusDisabled }, wantErr: domain.ErrAccountDisabled},
		{
			name: "deleted rejected",
			setup: func(a *domain.Account) {
				deleted := guardBaseTime
				a.DeletedAt = &deleted
			},
			wantErr: domain.ErrAccountDisabled,
		},
		{name: "nil account", nilArg: true, wantErr: domain.ErrInvalidCredentials},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newGuardFixture(t, app.DefaultLockoutPolicy())
			account := fixture.newActiveAccount(t)
			if tt.setup != nil {
				tt.setup(account)
			}
			if tt.nilArg {
				account = nil
			}
			err := fixture.guard.Authorize(account, guardBaseTime)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Authorize() error = %v, want sentinel %v", err, tt.wantErr)
				}
				guardCode(t, err, apperr.CodeUnauthorized)
				return
			}
			if err != nil {
				t.Fatalf("Authorize() error = %v, want nil", err)
			}
		})
	}
}

func TestLoginGuardRecordFailureLocksAtThreshold(t *testing.T) {
	policy := app.LockoutPolicy{MaxFailures: 3, Window: 15 * time.Minute, Duration: 15 * time.Minute}
	fixture := newGuardFixture(t, policy)
	account := fixture.newActiveAccount(t)
	ctx := context.Background()

	for i := 1; i <= policy.MaxFailures; i++ {
		now := guardBaseTime.Add(time.Duration(i) * time.Minute)
		if err := fixture.guard.RecordFailure(ctx, account, domain.LoginReasonBadCredential, "iphash", "uahash", now); err != nil {
			t.Fatalf("RecordFailure(#%d) error = %v", i, err)
		}
		wantLocked := i == policy.MaxFailures
		if wantLocked != (account.Status == domain.AccountStatusLocked) {
			t.Fatalf("after failure #%d status = %s, want locked = %v", i, account.Status, wantLocked)
		}
	}

	if account.LockedUntil == nil || !account.LockedUntil.Equal(guardBaseTime.Add(time.Duration(policy.MaxFailures)*time.Minute+policy.Duration)) {
		t.Fatalf("LockedUntil = %v, want %v", account.LockedUntil,
			guardBaseTime.Add(time.Duration(policy.MaxFailures)*time.Minute+policy.Duration))
	}

	persisted, err := fixture.accounts.FindByID(ctx, account.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if persisted.Status != domain.AccountStatusLocked {
		t.Fatalf("persisted status = %s, want locked", persisted.Status)
	}

	failures, err := fixture.attempts.CountFailuresSince(ctx, account.Channel,
		app.HashAccountRef(account.Channel, account.Subject), guardBaseTime.Add(-policy.Window))
	if err != nil {
		t.Fatalf("CountFailuresSince() error = %v", err)
	}
	if failures != policy.MaxFailures {
		t.Fatalf("failures = %d, want %d", failures, policy.MaxFailures)
	}
}

func TestLoginGuardRecordFailureIgnoresWindow(t *testing.T) {
	policy := app.LockoutPolicy{MaxFailures: 2, Window: 15 * time.Minute, Duration: 15 * time.Minute}
	fixture := newGuardFixture(t, policy)
	account := fixture.newActiveAccount(t)
	ctx := context.Background()

	if err := fixture.guard.RecordFailure(ctx, account, domain.LoginReasonBadCredential, "", "", guardBaseTime); err != nil {
		t.Fatalf("RecordFailure() error = %v", err)
	}
	// 第二次失败已越过 15 分钟窗口，首次失败不再计数。
	if err := fixture.guard.RecordFailure(ctx, account, domain.LoginReasonBadCredential, "", "", guardBaseTime.Add(16*time.Minute)); err != nil {
		t.Fatalf("RecordFailure() error = %v", err)
	}
	if account.Status != domain.AccountStatusActive {
		t.Fatalf("status = %s, want active", account.Status)
	}
}

func TestLoginGuardRecordSuccessResetsWindow(t *testing.T) {
	policy := app.LockoutPolicy{MaxFailures: 3, Window: 15 * time.Minute, Duration: 15 * time.Minute}
	fixture := newGuardFixture(t, policy)
	account := fixture.newActiveAccount(t)
	ctx := context.Background()

	if err := fixture.guard.RecordFailure(ctx, account, domain.LoginReasonBadCredential, "", "", guardBaseTime); err != nil {
		t.Fatalf("RecordFailure() error = %v", err)
	}
	if err := fixture.guard.RecordSuccess(ctx, account, "", "", guardBaseTime.Add(time.Minute)); err != nil {
		t.Fatalf("RecordSuccess() error = %v", err)
	}
	if err := fixture.guard.RecordFailure(ctx, account, domain.LoginReasonBadCredential, "", "", guardBaseTime.Add(2*time.Minute)); err != nil {
		t.Fatalf("RecordFailure() error = %v", err)
	}
	// 成功登录后窗口重置：成功之前的失败不再计数，当前仅 1 次失败。
	if account.Status != domain.AccountStatusActive {
		t.Fatalf("status = %s, want active", account.Status)
	}
}

func TestLoginGuardRecordSuccessUnlocksExpired(t *testing.T) {
	policy := app.LockoutPolicy{MaxFailures: 1, Window: 15 * time.Minute, Duration: 15 * time.Minute}
	fixture := newGuardFixture(t, policy)
	account := fixture.newActiveAccount(t)
	ctx := context.Background()

	if err := fixture.guard.RecordFailure(ctx, account, domain.LoginReasonBadCredential, "", "", guardBaseTime); err != nil {
		t.Fatalf("RecordFailure() error = %v", err)
	}
	if account.Status != domain.AccountStatusLocked {
		t.Fatalf("status = %s, want locked", account.Status)
	}

	// 锁定期已过，成功登录应自动恢复 active。
	if err := fixture.guard.RecordSuccess(ctx, account, "", "", guardBaseTime.Add(16*time.Minute)); err != nil {
		t.Fatalf("RecordSuccess() error = %v", err)
	}
	if account.Status != domain.AccountStatusActive || account.LockedUntil != nil {
		t.Fatalf("account = status %s, locked_until %v; want active / nil", account.Status, account.LockedUntil)
	}
	persisted, err := fixture.accounts.FindByID(ctx, account.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if persisted.Status != domain.AccountStatusActive {
		t.Fatalf("persisted status = %s, want active", persisted.Status)
	}

	latest, found, err := fixture.attempts.LastSuccessAt(ctx, account.Channel, app.HashAccountRef(account.Channel, account.Subject))
	if err != nil {
		t.Fatalf("LastSuccessAt() error = %v", err)
	}
	if !found || !latest.Equal(guardBaseTime.Add(16*time.Minute)) {
		t.Fatalf("LastSuccessAt() = %v / %v, want %v / true", latest, found, guardBaseTime.Add(16*time.Minute))
	}
}

func TestLoginGuardRecordNilAccount(t *testing.T) {
	fixture := newGuardFixture(t, app.DefaultLockoutPolicy())
	ctx := context.Background()
	if err := fixture.guard.RecordFailure(ctx, nil, domain.LoginReasonBadCredential, "", "", guardBaseTime); err == nil {
		t.Fatal("RecordFailure(nil) error = nil, want error")
	}
	if err := fixture.guard.RecordSuccess(ctx, nil, "", "", guardBaseTime); err == nil {
		t.Fatal("RecordSuccess(nil) error = nil, want error")
	}
}

func TestLoginGuardCheckRateLimit(t *testing.T) {
	empty := newGuardFixture(t, app.DefaultLockoutPolicy())
	if err := empty.guard.CheckRateLimit("alice"); err != nil {
		t.Fatalf("CheckRateLimit(no limiter) error = %v, want nil", err)
	}

	allowed := &stubLimiter{allowed: true}
	passing := newGuardFixture(t, app.DefaultLockoutPolicy(), app.WithLoginLimiter(allowed))
	if err := passing.guard.CheckRateLimit("alice"); err != nil {
		t.Fatalf("CheckRateLimit(allowed) error = %v, want nil", err)
	}
	if allowed.lastKey != "alice" {
		t.Fatalf("limiter key = %q, want alice", allowed.lastKey)
	}

	denied := &stubLimiter{allowed: false, retryAfter: 30 * time.Second}
	blocked := newGuardFixture(t, app.DefaultLockoutPolicy(), app.WithLoginLimiter(denied))
	err := blocked.guard.CheckRateLimit("alice")
	if err == nil {
		t.Fatal("CheckRateLimit(denied) error = nil, want error")
	}
	guardCode(t, err, apperr.CodeTooManyRequests)
}

func TestHashAccountRef(t *testing.T) {
	first := app.HashAccountRef(domain.ChannelAdmin, "  Alice@Example.COM ")
	second := app.HashAccountRef(domain.ChannelAdmin, "alice@example.com")
	if first != second {
		t.Fatalf("HashAccountRef() = %q / %q, want equal after normalization", first, second)
	}
	if strings.Contains(first, "alice") || strings.Contains(first, "@") {
		t.Fatalf("HashAccountRef() = %q, must not contain plaintext", first)
	}
	if len(first) != 64 {
		t.Fatalf("len(HashAccountRef()) = %d, want 64", len(first))
	}
	if other := app.HashAccountRef(domain.ChannelBiz, "alice@example.com"); other == first {
		t.Fatal("HashAccountRef() must differ across channels")
	}
}

// guardCode 断言错误携带的业务码。
func guardCode(t *testing.T, err error, want int) {
	t.Helper()
	var applicationError *apperr.AppError
	if !errors.As(err, &applicationError) {
		t.Fatalf("error = %v, want *apperr.AppError", err)
	}
	if applicationError.Code != want {
		t.Fatalf("code = %d, want %d", applicationError.Code, want)
	}
}
