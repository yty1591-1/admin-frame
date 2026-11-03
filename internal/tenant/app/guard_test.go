package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// countingRepo 记录 Get 次数，并按预设返回。
type countingRepo struct {
	mu      sync.Mutex
	calls   int
	tenants map[string]domain.Tenant
	err     error
}

func (r *countingRepo) Create(context.Context, domain.Tenant) error { return nil }
func (r *countingRepo) List(context.Context, domain.Filter) ([]domain.Tenant, int, error) {
	return nil, 0, nil
}
func (r *countingRepo) UpdateStatus(context.Context, domain.Tenant) error            { return nil }
func (r *countingRepo) UpdateProfile(context.Context, domain.Tenant) error           { return nil }
func (r *countingRepo) CountByStatus(context.Context) (map[domain.Status]int, error) { return nil, nil }

func (r *countingRepo) Get(_ context.Context, id string) (domain.Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return domain.Tenant{}, r.err
	}
	tenant, ok := r.tenants[id]
	if !ok {
		return domain.Tenant{}, domain.ErrNotFound
	}
	return tenant, nil
}

func (r *countingRepo) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// fakeClock 是可推进的时钟。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newGuard 构造带假时钟的守卫。
func newGuard(t *testing.T, repo domain.Repository, ttl, notFoundTTL time.Duration) (*app.Guard, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	guard, err := app.NewGuard(repo, app.GuardOptions{TTL: ttl, NotFoundTTL: notFoundTTL, Clock: clock.Now})
	if err != nil {
		t.Fatalf("NewGuard() error = %v", err)
	}
	return guard, clock
}

// activeTenant 构造 active 租户。
func activeTenant(status domain.Status) domain.Tenant {
	return domain.Tenant{ID: "m1", Name: "acme", Status: status, Version: 1}
}

// TestGuardAllowsActiveOnly 验证只有 active 放行，其余状态一律拒绝。
func TestGuardAllowsActiveOnly(t *testing.T) {
	cases := map[domain.Status]bool{
		domain.StatusActive:     true,
		domain.StatusPending:    false,
		domain.StatusSuspended:  false,
		domain.StatusTerminated: false,
	}
	for status, allow := range cases {
		t.Run(string(status), func(t *testing.T) {
			repo := &countingRepo{tenants: map[string]domain.Tenant{"m1": activeTenant(status)}}
			guard, _ := newGuard(t, repo, time.Minute, time.Second)
			err := guard.Check(context.Background(), "m1")
			if allow {
				if err != nil {
					t.Fatalf("Check() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, app.ErrTenantNotActive) {
				t.Fatalf("Check() error = %v, want ErrTenantNotActive", err)
			}
		})
	}
}

// TestGuardUnknownMerchantIsDenied 验证未登记商户被拒绝（fail closed）。
func TestGuardUnknownMerchantIsDenied(t *testing.T) {
	repo := &countingRepo{tenants: map[string]domain.Tenant{}}
	guard, _ := newGuard(t, repo, time.Minute, time.Second)
	if err := guard.Check(context.Background(), "absent"); !errors.Is(err, app.ErrTenantUnknown) {
		t.Fatalf("Check() error = %v, want ErrTenantUnknown", err)
	}
}

// TestGuardEmptyMerchantSkips 验证平台级请求（无 merchant_id）不查库。
func TestGuardEmptyMerchantSkips(t *testing.T) {
	repo := &countingRepo{tenants: map[string]domain.Tenant{}}
	guard, _ := newGuard(t, repo, time.Minute, time.Second)
	for _, merchant := range []string{"", "   "} {
		if err := guard.Check(context.Background(), merchant); err != nil {
			t.Fatalf("Check(%q) error = %v, want nil", merchant, err)
		}
	}
	if got := repo.callCount(); got != 0 {
		t.Fatalf("平台级请求不应查库，calls = %d", got)
	}
}

// TestGuardCachesWithinTTL 验证 TTL 内不重复查库，过期后重新查。
func TestGuardCachesWithinTTL(t *testing.T) {
	repo := &countingRepo{tenants: map[string]domain.Tenant{"m1": activeTenant(domain.StatusActive)}}
	guard, clock := newGuard(t, repo, 30*time.Second, time.Second)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := guard.Check(ctx, "m1"); err != nil {
			t.Fatalf("Check() error = %v", err)
		}
	}
	if got := repo.callCount(); got != 1 {
		t.Fatalf("TTL 内 calls = %d, want 1", got)
	}

	clock.Advance(29 * time.Second)
	if err := guard.Check(ctx, "m1"); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got := repo.callCount(); got != 1 {
		t.Fatalf("未过期 calls = %d, want 1", got)
	}

	clock.Advance(2 * time.Second)
	if err := guard.Check(ctx, "m1"); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got := repo.callCount(); got != 2 {
		t.Fatalf("过期后 calls = %d, want 2", got)
	}
}

// TestGuardNegativeCacheIsShorter 验证未登记商户的负缓存短于正缓存：
// 开户后最多 NotFoundTTL 就能通过，不必等完整 TTL。
func TestGuardNegativeCacheIsShorter(t *testing.T) {
	repo := &countingRepo{tenants: map[string]domain.Tenant{}}
	guard, clock := newGuard(t, repo, 30*time.Second, 5*time.Second)
	ctx := context.Background()

	if err := guard.Check(ctx, "m9"); !errors.Is(err, app.ErrTenantUnknown) {
		t.Fatalf("Check() error = %v, want ErrTenantUnknown", err)
	}
	// 期间完成开户。
	repo.mu.Lock()
	repo.tenants["m9"] = activeTenant(domain.StatusActive)
	repo.mu.Unlock()

	clock.Advance(4 * time.Second)
	if err := guard.Check(ctx, "m9"); !errors.Is(err, app.ErrTenantUnknown) {
		t.Fatalf("负缓存未过期应仍拒绝，error = %v", err)
	}
	clock.Advance(2 * time.Second)
	if err := guard.Check(ctx, "m9"); err != nil {
		t.Fatalf("负缓存过期后应放行，error = %v", err)
	}
	if got := repo.callCount(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

// TestGuardRepositoryErrorIsNotCached 验证仓储故障返回错误、不缓存、不误判为停用。
func TestGuardRepositoryErrorIsNotCached(t *testing.T) {
	repo := &countingRepo{err: errors.New("db down")}
	guard, _ := newGuard(t, repo, time.Minute, time.Second)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		err := guard.Check(ctx, "m1")
		if err == nil || errors.Is(err, app.ErrTenantNotActive) || errors.Is(err, app.ErrTenantUnknown) {
			t.Fatalf("Check() error = %v, want generic lookup error", err)
		}
	}
	if got := repo.callCount(); got != 2 {
		t.Fatalf("故障不应缓存，calls = %d, want 2", got)
	}
}

// TestGuardInvalidateForcesRefetch 验证失效后立即按新状态判定（不等 TTL）。
func TestGuardInvalidateForcesRefetch(t *testing.T) {
	repo := &countingRepo{tenants: map[string]domain.Tenant{"m1": activeTenant(domain.StatusActive)}}
	guard, _ := newGuard(t, repo, 30*time.Second, time.Second)
	ctx := context.Background()

	if err := guard.Check(ctx, "m1"); err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	// 模拟本进程内停用：仓储状态变化 + 失效缓存。
	repo.mu.Lock()
	repo.tenants["m1"] = activeTenant(domain.StatusSuspended)
	repo.mu.Unlock()
	guard.Invalidate("m1")

	if err := guard.Check(ctx, "m1"); !errors.Is(err, app.ErrTenantNotActive) {
		t.Fatalf("失效后应拒绝，error = %v", err)
	}
	if got := repo.callCount(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

// TestGuardVerifySeparatesDeniedFromUndetermined 验证中间件契约：
// 「明确拒绝」返回 (false, nil)，「无法判定」返回 (false, err)。
func TestGuardVerifySeparatesDeniedFromUndetermined(t *testing.T) {
	active := &countingRepo{tenants: map[string]domain.Tenant{"m1": activeTenant(domain.StatusActive)}}
	guard, _ := newGuard(t, active, time.Minute, time.Second)
	allow, err := guard.Verify(context.Background(), "m1")
	if !allow || err != nil {
		t.Fatalf("Verify(active) = %v,%v want true,nil", allow, err)
	}

	suspended := &countingRepo{tenants: map[string]domain.Tenant{"m1": activeTenant(domain.StatusSuspended)}}
	guard, _ = newGuard(t, suspended, time.Minute, time.Second)
	allow, err = guard.Verify(context.Background(), "m1")
	if allow || err != nil {
		t.Fatalf("Verify(suspended) = %v,%v want false,nil", allow, err)
	}

	broken := &countingRepo{err: errors.New("db down")}
	guard, _ = newGuard(t, broken, time.Minute, time.Second)
	allow, err = guard.Verify(context.Background(), "m1")
	if allow || err == nil {
		t.Fatalf("Verify(broken) = %v,%v want false,err", allow, err)
	}
}

// WithinTx 在测试中直接执行 fn（无事务语义）。
func (r *countingRepo) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// recordObserver 是 Observer 的记录实现，用于断言埋点接线（T-16）。
type recordObserver struct {
	mu          sync.Mutex
	decisions   []string
	cacheHits   []bool
	transitions [][2]string
	emitFailed  int
}

func (o *recordObserver) GuardDecision(result string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.decisions = append(o.decisions, result)
}
func (o *recordObserver) GuardCacheHit(hit bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cacheHits = append(o.cacheHits, hit)
}
func (o *recordObserver) Transition(from, to string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.transitions = append(o.transitions, [2]string{from, to})
}
func (o *recordObserver) AuditEmitFailed() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.emitFailed++
}
func (o *recordObserver) snapshot() (decisions []string, hits []bool, transitions [][2]string, failed int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.decisions...), append([]bool(nil), o.cacheHits...),
		append([][2]string(nil), o.transitions...), o.emitFailed
}

// TestGuardObserverWiring 验证守卫判定 / 缓存命中信号接入 Observer（T-16）。
func TestGuardObserverWiring(t *testing.T) {
	repo := &countingRepo{tenants: map[string]domain.Tenant{"m1": activeTenant(domain.StatusActive)}}
	obs := &recordObserver{}
	guard, err := app.NewGuard(repo, app.GuardOptions{
		TTL: time.Minute, NotFoundTTL: time.Second, Clock: func() time.Time { return time.Unix(0, 0) },
		Observer: obs,
	})
	if err != nil {
		t.Fatalf("NewGuard() error = %v", err)
	}

	// 首次：回源 + allow。
	if err := guard.Check(context.Background(), "m1"); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	// 第二次：缓存命中 + allow。
	if err := guard.Check(context.Background(), "m1"); err != nil {
		t.Fatalf("Check(cached) error = %v", err)
	}
	// 未登记：回源 + deny。
	if err := guard.Check(context.Background(), "absent"); !errors.Is(err, app.ErrTenantUnknown) {
		t.Fatalf("Check(absent) error = %v", err)
	}
	// 数据库故障：error。
	repo.err = errors.New("db down")
	repo.calls = 0
	repo.tenants = map[string]domain.Tenant{} // absent 已缓存 deny，换个新商户强制回源
	if err := guard.Check(context.Background(), "m2"); err == nil {
		t.Fatal("Check(db down) 应报错")
	}

	decisions, hits, _, _ := obs.snapshot()
	wantDecisions := []string{"allow", "allow", "deny", "error"}
	if len(decisions) != len(wantDecisions) {
		t.Fatalf("decisions = %v, want %v", decisions, wantDecisions)
	}
	for i, want := range wantDecisions {
		if decisions[i] != want {
			t.Fatalf("decisions[%d] = %s, want %s", i, decisions[i], want)
		}
	}
	wantHits := []bool{false, true, false, false}
	if len(hits) != len(wantHits) {
		t.Fatalf("cacheHits = %v, want %v", hits, wantHits)
	}
	for i, want := range wantHits {
		if hits[i] != want {
			t.Fatalf("cacheHits[%d] = %t, want %t", i, hits[i], want)
		}
	}
}

// TestNilObserverIsNoop 验证 nil Observer 归一化：守卫零配置可用。
func TestNilObserverIsNoop(t *testing.T) {
	repo := &countingRepo{tenants: map[string]domain.Tenant{"m1": activeTenant(domain.StatusActive)}}
	guard, err := app.NewGuard(repo, app.GuardOptions{})
	if err != nil {
		t.Fatalf("NewGuard() error = %v", err)
	}
	if err := guard.Check(context.Background(), "m1"); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}
