package cache

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// countingRevocationStore 包装 MemoryRevocationStore 并统计 inner 调用次数，
// 用于验证 L1 是否真正减少了 Redis（此处以 inner 调用代表）往返。
type countingRevocationStore struct {
	inner       app.RevocationStore
	accountLoad atomic.Int64
	sessionLoad atomic.Int64
	tokenLoad   atomic.Int64
}

func newCountingStore() *countingRevocationStore {
	return &countingRevocationStore{inner: NewMemoryRevocationStore()}
}

func (s *countingRevocationStore) LoadAccountState(ctx context.Context, accountID string) (app.AccountState, bool, error) {
	s.accountLoad.Add(1)
	return s.inner.LoadAccountState(ctx, accountID)
}

func (s *countingRevocationStore) SaveAccountState(ctx context.Context, accountID string, state app.AccountState, ttl time.Duration) error {
	return s.inner.SaveAccountState(ctx, accountID, state, ttl)
}

func (s *countingRevocationStore) DeleteAccountState(ctx context.Context, accountID string) error {
	return s.inner.DeleteAccountState(ctx, accountID)
}

func (s *countingRevocationStore) LoadSessionState(ctx context.Context, sessionID string) (app.SessionState, bool, error) {
	s.sessionLoad.Add(1)
	return s.inner.LoadSessionState(ctx, sessionID)
}

func (s *countingRevocationStore) SaveSessionState(ctx context.Context, sessionID string, state app.SessionState, ttl time.Duration) error {
	return s.inner.SaveSessionState(ctx, sessionID, state, ttl)
}

func (s *countingRevocationStore) DeleteSessionState(ctx context.Context, sessionID string) error {
	return s.inner.DeleteSessionState(ctx, sessionID)
}

func (s *countingRevocationStore) RevokeToken(ctx context.Context, jti string, ttl time.Duration) error {
	return s.inner.RevokeToken(ctx, jti, ttl)
}

func (s *countingRevocationStore) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	s.tokenLoad.Add(1)
	return s.inner.IsTokenRevoked(ctx, jti)
}

// cacheObserver 记录指标调用，便于断言命中 / Redis / 回源计数。
type cacheObserver struct {
	hits    atomic.Int64
	redis   atomic.Int64
	refetch atomic.Int64
}

func (o *cacheObserver) OnRevocationCacheHit(context.Context, string)  { o.hits.Add(1) }
func (o *cacheObserver) OnRevocationRedisCall(context.Context, string) { o.redis.Add(1) }
func (o *cacheObserver) OnRevocationDBRefetch(context.Context, string) { o.refetch.Add(1) }

// newCachedForTest 构造带可控时钟的装饰器，避免依赖真实 sleep。
func newCachedForTest(inner app.RevocationStore, ttl time.Duration, capacity int, observer app.RevocationCacheObserver, now *time.Time) app.RevocationStore {
	store := &cachedRevocationStore{
		inner:    inner,
		ttl:      ttl,
		capacity: capacity,
		observer: observer,
		l1:       newRevocationL1(ttl, capacity),
	}
	store.l1.clock = func() time.Time { return *now }
	return store
}

// TestCachedRevocationAccountHitAvoidsInner 验证账号快照第二次读取命中 L1，不再调用 Redis。
func TestCachedRevocationAccountHitAvoidsInner(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	inner := newCountingStore()
	observer := &cacheObserver{}
	store := newCachedForTest(inner, time.Second, 8, observer, &now)

	if err := store.SaveAccountState(ctx, "acc-1", app.AccountState{Version: 7, Status: domain.AccountStatusActive}, time.Minute); err != nil {
		t.Fatalf("save account state: %v", err)
	}
	state, found, err := store.LoadAccountState(ctx, "acc-1")
	if err != nil || !found || state.Version != 7 {
		t.Fatalf("first load = %+v, found=%v, err=%v", state, found, err)
	}
	if got := inner.accountLoad.Load(); got != 0 {
		t.Fatalf("first load after save should be served from L1, inner calls=%d", got)
	}

	state, found, err = store.LoadAccountState(ctx, "acc-1")
	if err != nil || !found || state.Version != 7 {
		t.Fatalf("second load = %+v, found=%v, err=%v", state, found, err)
	}
	if got := observer.hits.Load(); got != 2 {
		t.Fatalf("l1 hits = %d, want 2", got)
	}
	if got := inner.accountLoad.Load(); got != 0 {
		t.Fatalf("inner calls = %d, want 0", got)
	}
}

// TestCachedRevocationExpiresByTTL 验证 L1 超过 TTL 后重新读取 inner。
func TestCachedRevocationExpiresByTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	inner := newCountingStore()
	store := newCachedForTest(inner, time.Second, 8, nil, &now)

	if err := inner.SaveAccountState(ctx, "acc-2", app.AccountState{Version: 3, Status: domain.AccountStatusActive}, time.Minute); err != nil {
		t.Fatalf("inner save: %v", err)
	}
	if _, _, err := store.LoadAccountState(ctx, "acc-2"); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	if got := inner.accountLoad.Load(); got != 1 {
		t.Fatalf("inner calls after prime = %d, want 1", got)
	}

	now = now.Add(2 * time.Second)
	if _, _, err := store.LoadAccountState(ctx, "acc-2"); err != nil {
		t.Fatalf("load after ttl: %v", err)
	}
	if got := inner.accountLoad.Load(); got != 2 {
		t.Fatalf("inner calls after ttl = %d, want 2", got)
	}
}

// TestCachedRevocationInvalidatedBySave 验证本进程写入新快照后 L1 立即更新。
func TestCachedRevocationInvalidatedBySave(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	inner := newCountingStore()
	store := newCachedForTest(inner, time.Minute, 8, nil, &now)

	if err := inner.SaveAccountState(ctx, "acc-3", app.AccountState{Version: 1, Status: domain.AccountStatusActive}, time.Minute); err != nil {
		t.Fatalf("inner save: %v", err)
	}
	primed, _, err := store.LoadAccountState(ctx, "acc-3")
	if err != nil || primed.Version != 1 {
		t.Fatalf("prime = %+v, err=%v", primed, err)
	}

	// 模拟停用：写路径通过同一个装饰器写入更高版本。
	if err := store.SaveAccountState(ctx, "acc-3", app.AccountState{Version: 2, Status: domain.AccountStatusDisabled}, time.Minute); err != nil {
		t.Fatalf("overwrite state: %v", err)
	}
	state, _, err := store.LoadAccountState(ctx, "acc-3")
	if err != nil {
		t.Fatalf("load after save: %v", err)
	}
	if state.Version != 2 || state.Status != domain.AccountStatusDisabled {
		t.Fatalf("state after save = %+v, want version=2 disabled", state)
	}
}

// TestCachedRevocationDeleteClearsL1 验证删除快照后本副本下一次读取直接落空。
func TestCachedRevocationDeleteClearsL1(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	inner := newCountingStore()
	store := newCachedForTest(inner, time.Minute, 8, nil, &now)

	if err := store.SaveAccountState(ctx, "acc-4", app.AccountState{Version: 1, Status: domain.AccountStatusActive}, time.Minute); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, found, err := store.LoadAccountState(ctx, "acc-4"); err != nil || !found {
		t.Fatalf("load before delete: found=%v err=%v", found, err)
	}
	if err := store.DeleteAccountState(ctx, "acc-4"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, err := store.LoadAccountState(ctx, "acc-4"); err != nil || found {
		t.Fatalf("load after delete: found=%v err=%v, want not found", found, err)
	}
}

// TestCachedRevocationNeverCachesJTIAbsence 验证 jti 黑名单不缓存「未撤销」结果。
func TestCachedRevocationNeverCachesJTIAbsence(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	inner := newCountingStore()
	store := newCachedForTest(inner, time.Minute, 8, nil, &now)

	for i := 0; i < 2; i++ {
		revoked, err := store.IsTokenRevoked(ctx, "jti-1")
		if err != nil || revoked {
			t.Fatalf("check %d: revoked=%v err=%v", i, revoked, err)
		}
	}
	if got := inner.tokenLoad.Load(); got != 2 {
		t.Fatalf("token inner calls = %d, want 2 (jti must not be cached)", got)
	}

	if err := store.RevokeToken(ctx, "jti-1", time.Minute); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	revoked, err := store.IsTokenRevoked(ctx, "jti-1")
	if err != nil || !revoked {
		t.Fatalf("after revoke: revoked=%v err=%v", revoked, err)
	}
}

// TestCachedRevocationEvictsLRU 验证容量上限触发 LRU 淘汰，避免内存无界增长。
func TestCachedRevocationEvictsLRU(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	inner := newCountingStore()
	store := newCachedForTest(inner, time.Minute, 2, nil, &now)

	for _, id := range []string{"acc-a", "acc-b", "acc-c"} {
		if err := store.SaveAccountState(ctx, id, app.AccountState{Version: 1, Status: domain.AccountStatusActive}, time.Minute); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	// 容量 2：最早写入的 acc-a 应被淘汰；读取会回落到 inner（accountLoad 加 1）。
	if _, found, err := store.LoadAccountState(ctx, "acc-a"); err != nil || !found {
		t.Fatalf("evicted entry should fall back to inner: found=%v err=%v", found, err)
	}
	if got := inner.accountLoad.Load(); got != 1 {
		t.Fatalf("inner calls after evicted load = %d, want 1", got)
	}
	// 仍在 L1 的 acc-c 不应触发 inner。
	if _, _, err := store.LoadAccountState(ctx, "acc-c"); err != nil {
		t.Fatalf("load acc-c: %v", err)
	}
	if got := inner.accountLoad.Load(); got != 1 {
		t.Fatalf("inner calls after cached load = %d, want 1", got)
	}
}
