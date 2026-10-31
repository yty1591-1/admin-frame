package app_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// missStore 表示撤销缓存始终未命中且无错误，迫使请求回源数据库。
type missStore struct{}

func (missStore) LoadAccountState(context.Context, string) (app.AccountState, bool, error) {
	return app.AccountState{}, false, nil
}
func (missStore) SaveAccountState(context.Context, string, app.AccountState, time.Duration) error {
	return nil
}
func (missStore) DeleteAccountState(context.Context, string) error { return nil }
func (missStore) LoadSessionState(context.Context, string) (app.SessionState, bool, error) {
	return app.SessionState{}, false, nil
}
func (missStore) SaveSessionState(context.Context, string, app.SessionState, time.Duration) error {
	return nil
}
func (missStore) DeleteSessionState(context.Context, string) error { return nil }
func (missStore) RevokeToken(context.Context, string, time.Duration) error {
	return nil
}
func (missStore) IsTokenRevoked(context.Context, string) (bool, error) { return false, nil }

// blockingAccountRepo 在 FindByID 上阻塞，用于模拟首个请求取消时仍在回源。
type blockingAccountRepo struct {
	domain.AccountRepository
	started chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func (r *blockingAccountRepo) FindByID(ctx context.Context, id string) (*domain.Account, error) {
	r.calls.Add(1)
	select {
	case r.started <- struct{}{}:
	default:
	}
	select {
	case <-r.release:
		return r.AccountRepository.FindByID(ctx, id)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestRevocationRefetchDecoupledFromCallerCancel 验证首个调用取消不会让同 key 等待者失败（REL-03）。
func TestRevocationRefetchDecoupledFromCallerCancel(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	inner := persistence.NewMemoryAccountRepository()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: "acct-rel03", Channel: domain.ChannelAdmin, Subject: "alice", Now: now,
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := inner.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	repo := &blockingAccountRepo{AccountRepository: inner, started: make(chan struct{}, 1), release: make(chan struct{})}
	checker, err := app.NewRevocationChecker(repo, missStore{}, app.RevocationFailClosed, time.Hour,
		app.WithRevocationClock(&fixedClock{now: now}))
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	claims := jwt.Claims{Subject: "acct-rel03", Extra: map[string]any{"ver": int64(1)}}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var firstErr, secondErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		firstErr = checker.Check(firstCtx, claims)
	}()
	select {
	case <-repo.started:
	case <-time.After(2 * time.Second):
		t.Fatal("refetch did not start")
	}
	cancelFirst()
	// 第二个请求加入同一 singleflight；首请求取消不应让它一起失败。
	go func() {
		defer wg.Done()
		secondErr = checker.Check(context.Background(), claims)
	}()
	time.Sleep(20 * time.Millisecond)
	close(repo.release)
	wg.Wait()

	if secondErr != nil {
		t.Fatalf("second caller must succeed after first cancel, got %v", secondErr)
	}
	if firstErr == nil {
		t.Fatal("first caller must observe its own cancellation")
	}
	if got := repo.calls.Load(); got != 1 {
		t.Fatalf("refetch calls = %d, want 1 (singleflight)", got)
	}
}
