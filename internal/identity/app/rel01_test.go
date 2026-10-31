package app_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// countingBrokenStore 统计撤销存储调用次数，并始终返回依赖故障。
type countingBrokenStore struct {
	calls atomic.Int64
}

func (s *countingBrokenStore) LoadAccountState(context.Context, string) (app.AccountState, bool, error) {
	s.calls.Add(1)
	return app.AccountState{}, false, errStoreDown
}

func (s *countingBrokenStore) SaveAccountState(context.Context, string, app.AccountState, time.Duration) error {
	s.calls.Add(1)
	return errStoreDown
}

func (s *countingBrokenStore) DeleteAccountState(context.Context, string) error {
	s.calls.Add(1)
	return errStoreDown
}

func (s *countingBrokenStore) LoadSessionState(context.Context, string) (app.SessionState, bool, error) {
	s.calls.Add(1)
	return app.SessionState{}, false, errStoreDown
}

func (s *countingBrokenStore) SaveSessionState(context.Context, string, app.SessionState, time.Duration) error {
	s.calls.Add(1)
	return errStoreDown
}

func (s *countingBrokenStore) DeleteSessionState(context.Context, string) error {
	s.calls.Add(1)
	return errStoreDown
}

func (s *countingBrokenStore) RevokeToken(context.Context, string, time.Duration) error {
	s.calls.Add(1)
	return errStoreDown
}

func (s *countingBrokenStore) IsTokenRevoked(context.Context, string) (bool, error) {
	s.calls.Add(1)
	return false, errStoreDown
}

// TestRevocationCheckerBreakerFailsFastWithoutStoreCalls 验证熔断打开后 jti 校验不再触网（REL-01）。
func TestRevocationCheckerBreakerFailsFastWithoutStoreCalls(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	accounts := persistence.NewMemoryAccountRepository()
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: "acct-1", Channel: domain.ChannelAdmin, Subject: "alice", Now: now,
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	store := &countingBrokenStore{}
	checker, err := app.NewRevocationChecker(accounts, store, app.RevocationFailClosed, time.Hour,
		app.WithRevocationClock(&fixedClock{now: now}))
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	claims := jwt.Claims{Subject: "acct-1", ID: "jti-1", Extra: map[string]any{"ver": int64(1)}}

	// 前两次请求把熔断计数推过阈值；第二次起回填与 jti 均跳过 Redis。
	requireAppCode(t, checker.Check(ctx, claims), apperr.CodeServiceUnavailable)
	requireAppCode(t, checker.Check(ctx, claims), apperr.CodeServiceUnavailable)
	if got := store.calls.Load(); got != 5 {
		t.Fatalf("store calls after breaker open = %d, want 5", got)
	}
	// 冷却期内必须快速失败且完全不触网，避免每个请求等待 Redis 读超时。
	before := store.calls.Load()
	requireAppCode(t, checker.Check(ctx, claims), apperr.CodeServiceUnavailable)
	if after := store.calls.Load(); after != before {
		t.Fatalf("breaker must skip store calls: before=%d after=%d", before, after)
	}
}
