package app_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
)

// perf02TxTracker 标记当前是否处于数据库事务内。
type perf02TxTracker struct {
	inTx *atomic.Bool
}

func (t *perf02TxTracker) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	t.inTx.Store(true)
	defer t.inTx.Store(false)
	return fn(ctx)
}

// txAwareRevocationStore 只在事务内写会话快照时置位，用于断言写入已移出事务（PERF-02）。
type txAwareRevocationStore struct {
	*cache.MemoryRevocationStore
	inTx            *atomic.Bool
	sessionSaveInTx atomic.Bool
}

func (s *txAwareRevocationStore) SaveSessionState(
	ctx context.Context,
	sessionID string,
	state app.SessionState,
	ttl time.Duration,
) error {
	if s.inTx.Load() {
		s.sessionSaveInTx.Store(true)
	}
	return s.MemoryRevocationStore.SaveSessionState(ctx, sessionID, state, ttl)
}

// TestSessionSnapshotWrittenAfterCommit 验证登录与刷新的会话快照均在事务提交后写入（PERF-02）。
func TestSessionSnapshotWrittenAfterCommit(t *testing.T) {
	ctx := context.Background()
	base := cache.NewMemoryRevocationStore()
	inTx := &atomic.Bool{}
	store := &txAwareRevocationStore{MemoryRevocationStore: base, inTx: inTx}
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.Revocation = store
		cfg.UoW = &perf02TxTracker{inTx: inTx}
	})
	f.seedAccount(t, "acct-session", "alice", "Old#Password12")

	login := f.login(t, "alice", "Old#Password12")
	if store.sessionSaveInTx.Load() {
		t.Fatalf("login must not write session snapshot inside transaction")
	}
	if _, found, err := base.LoadSessionState(ctx, login.SessionID); err != nil || !found {
		t.Fatalf("login snapshot missing: found=%v err=%v", found, err)
	}

	refreshed, err := f.svc.Refresh(ctx, app.RefreshInput{RefreshToken: login.RefreshToken, TraceID: "trace-refresh"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if store.sessionSaveInTx.Load() {
		t.Fatalf("refresh must not write session snapshot inside transaction")
	}
	hash, err := app.HashRefreshToken(refreshed.RefreshToken)
	if err != nil {
		t.Fatalf("hash refresh token: %v", err)
	}
	session, err := f.sessions.FindByRefreshHash(ctx, hash)
	if err != nil {
		t.Fatalf("find refreshed session: %v", err)
	}
	if _, found, err := base.LoadSessionState(ctx, session.ID); err != nil || !found {
		t.Fatalf("refresh snapshot missing: found=%v err=%v", found, err)
	}
}
