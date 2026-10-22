package app_test

import (
	"context"
	stderrors "errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
	pkgcache "github.com/admin-frame/admin-frame/pkg/cache"
)

// fakeRepository 是可编程的快照仓储，记录调用次数。
type fakeRepository struct {
	calls atomic.Int32
	load  func(ctx context.Context, query domain.SubjectQuery) (*domain.SubjectSnapshot, error)
}

// Load 实现 domain.SnapshotRepository。
func (r *fakeRepository) Load(ctx context.Context, query domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
	r.calls.Add(1)
	return r.load(ctx, query)
}

// bizSnapshot 构造含重复项与通配符的仓储快照。
func bizSnapshot() *domain.SubjectSnapshot {
	return &domain.SubjectSnapshot{
		Channel:     domain.ChannelBiz,
		SubjectType: domain.SubjectMerchantStaff,
		SubjectID:   "u-1",
		MerchantID:  "m-1",
		Roles:       []string{"merchant_ops", "merchant_ops"},
		Permissions: []iam.PermissionCode{"iam:role:write", "iam:role:read", "*", "iam:role:read"},
		Scopes: []domain.DataScope{
			{Resource: "iam", Type: domain.DataScopeMerchant},
		},
		Version:     9,
		GeneratedAt: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC),
	}
}

// bizPrincipal 构造 B 端 principal。
func bizPrincipal() auth.Principal {
	return auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-1", MerchantID: "m-1"}
}

// newCache 构造内存两级缓存。
func newCache(tb testing.TB, opts ...func(*iampcache.Config)) *iampcache.SnapshotCache {
	tb.Helper()
	cfg := iampcache.Config{
		L1:          pkgcache.NewMemory(pkgcache.MemoryConfig{MaxEntries: 64}),
		L2:          pkgcache.NewMemory(pkgcache.MemoryConfig{MaxEntries: 64}),
		L1TTL:       3 * time.Second,
		L2TTL:       5 * time.Minute,
		NegativeTTL: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	cache, err := iampcache.NewSnapshotCache(cfg)
	if err != nil {
		tb.Fatalf("new cache: %v", err)
	}
	return cache
}

// newService 构造被测服务，测试默认允许 5 个角色 / 100 个权限。
func newService(tb testing.TB, repo domain.SnapshotRepository, opts ...func(*app.AuthorizeConfig)) *app.AuthorizeService {
	tb.Helper()
	cfg := app.AuthorizeConfig{
		Repository:     repo,
		Cache:          newCache(tb),
		Env:            "test",
		LoadTimeout:    200 * time.Millisecond,
		MaxRoles:       5,
		MaxPermissions: 100,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	service, err := app.NewAuthorizeService(cfg)
	if err != nil {
		tb.Fatalf("new service: %v", err)
	}
	return service
}

// TestAuthorizeServiceCachesAndCleans 验证缓存命中、清洗、去重排序与数据范围映射。
func TestAuthorizeServiceCachesAndCleans(t *testing.T) {
	repo := &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return bizSnapshot(), nil
	}}
	observer := &recordingObserver{}
	service := newService(t, repo, func(cfg *app.AuthorizeConfig) { cfg.Observer = observer })

	snapshot, err := service.LoadSnapshot(context.Background(), bizPrincipal())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snapshot.Roles) != 1 || snapshot.Roles[0] != "merchant_ops" {
		t.Fatalf("roles=%v", snapshot.Roles)
	}
	if len(snapshot.Permissions) != 2 || snapshot.Permissions[0] != "iam:role:read" || snapshot.Permissions[1] != "iam:role:write" {
		t.Fatalf("permissions=%v", snapshot.Permissions)
	}
	if snapshot.Scopes["iam"].Type != string(domain.DataScopeMerchant) {
		t.Fatalf("scopes=%v", snapshot.Scopes)
	}

	// 第二次必须命中 L1，不再回源。
	if _, err := service.LoadSnapshot(context.Background(), bizPrincipal()); err != nil {
		t.Fatalf("second load: %v", err)
	}
	if calls := repo.calls.Load(); calls != 1 {
		t.Fatalf("repository calls=%d want 1", calls)
	}
	if !observer.hasSource(iampcache.LayerDB) || !observer.hasSource(iampcache.LayerL1) {
		t.Fatalf("observer sources=%v", observer.sources)
	}
	if observer.lastPermissions != 2 {
		t.Fatalf("observer snapshot permissions=%d want 2", observer.lastPermissions)
	}
}

// TestAuthorizeServiceAllowWildcard 验证显式允许时保留通配符（仅破玻璃场景）。
func TestAuthorizeServiceAllowWildcard(t *testing.T) {
	repo := &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return bizSnapshot(), nil
	}}
	service := newService(t, repo, func(cfg *app.AuthorizeConfig) { cfg.AllowWildcard = true })
	snapshot, err := service.LoadSnapshot(context.Background(), bizPrincipal())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !snapshot.HasPermission("*") {
		t.Fatalf("wildcard must be preserved when allowed: %v", snapshot.Permissions)
	}
}

// TestAuthorizeServiceNegativeCache 验证空快照进入缓存，避免异常主体打穿 DB。
func TestAuthorizeServiceNegativeCache(t *testing.T) {
	repo := &fakeRepository{load: func(_ context.Context, query domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return &domain.SubjectSnapshot{
			Channel: query.Channel, SubjectType: query.SubjectType,
			SubjectID: query.SubjectID, MerchantID: query.MerchantID,
		}, nil
	}}
	service := newService(t, repo)
	first, err := service.LoadSnapshot(context.Background(), bizPrincipal())
	if err != nil || !first.Empty() {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := service.LoadSnapshot(context.Background(), bizPrincipal())
	if err != nil || !second.Empty() {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if calls := repo.calls.Load(); calls != 1 {
		t.Fatalf("repository calls=%d want 1 (negative cache)", calls)
	}
}

// TestAuthorizeServiceInvalidPrincipal 验证非法主体返回 401，而不是 503。
func TestAuthorizeServiceInvalidPrincipal(t *testing.T) {
	service := newService(t, &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return bizSnapshot(), nil
	}})
	cases := []struct {
		name      string
		principal auth.Principal
	}{
		{name: "unknown channel", principal: auth.Principal{Channel: auth.ChannelC, SubjectID: "u-1"}},
		{name: "missing subject", principal: auth.Principal{Channel: auth.ChannelAdmin}},
		{name: "biz missing merchant", principal: auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-1"}},
		{name: "admin with merchant", principal: auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1", MerchantID: "m-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.LoadSnapshot(context.Background(), tc.principal)
			var appErr *apperr.AppError
			if !stderrors.As(err, &appErr) || appErr.Code != apperr.CodeUnauthorized {
				t.Fatalf("err=%v want code %d", err, apperr.CodeUnauthorized)
			}
		})
	}
}

// TestAuthorizeServiceRepositoryFailureIsClosed 验证回源失败返回 50300。
func TestAuthorizeServiceRepositoryFailureIsClosed(t *testing.T) {
	repo := &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return nil, stderrors.New("mysql: connection refused")
	}}
	service := newService(t, repo)
	if _, err := service.LoadSnapshot(context.Background(), bizPrincipal()); err == nil {
		t.Fatal("repository failure must be surfaced")
	} else {
		var appErr *apperr.AppError
		if !stderrors.As(err, &appErr) || appErr.Code != apperr.CodeServiceUnavailable {
			t.Fatalf("err=%v want 50300", err)
		}
	}
	// 失败结果不得写入缓存：下一次仍应回源。
	if _, err := service.LoadSnapshot(context.Background(), bizPrincipal()); err == nil {
		t.Fatal("second load must also fail")
	}
	if calls := repo.calls.Load(); calls != 2 {
		t.Fatalf("repository calls=%d want 2", calls)
	}
}

// TestAuthorizeServiceTimeout 验证 DB 超时按失败关闭处理。
func TestAuthorizeServiceTimeout(t *testing.T) {
	repo := &fakeRepository{load: func(ctx context.Context, _ domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	service := newService(t, repo, func(cfg *app.AuthorizeConfig) { cfg.LoadTimeout = 20 * time.Millisecond })
	started := time.Now()
	if _, err := service.LoadSnapshot(context.Background(), bizPrincipal()); err == nil {
		t.Fatal("timeout must fail closed")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("load timeout not applied: %v", elapsed)
	}
}

// TestAuthorizeServiceSingleflight 验证并发未命中只回源一次。
func TestAuthorizeServiceSingleflight(t *testing.T) {
	repo := &fakeRepository{load: func(_ context.Context, _ domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		time.Sleep(20 * time.Millisecond)
		return bizSnapshot(), nil
	}}
	service := newService(t, repo)
	const concurrency = 8
	var wg sync.WaitGroup
	wg.Add(concurrency)
	errs := make(chan error, concurrency)
	start := make(chan struct{})
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			<-start
			if _, err := service.LoadSnapshot(context.Background(), bizPrincipal()); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent load: %v", err)
	}
	if calls := repo.calls.Load(); calls != 1 {
		t.Fatalf("repository calls=%d want 1 (singleflight)", calls)
	}
}

// TestAuthorizeServiceCacheWriteFailureDoesNotFailRequest 验证缓存写失败不阻断鉴权。
func TestAuthorizeServiceCacheWriteFailureDoesNotFailRequest(t *testing.T) {
	repo := &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return bizSnapshot(), nil
	}}
	observer := &recordingObserver{}
	service := newService(t, repo, func(cfg *app.AuthorizeConfig) {
		cfg.Cache = newCache(t, func(cfg *iampcache.Config) {
			cfg.L2 = pkgcache.FuncStore{
				GetFunc: func(context.Context, string) ([]byte, error) { return nil, pkgcache.ErrMiss },
				SetFunc: func(context.Context, string, []byte, time.Duration) error {
					return stderrors.New("redis: write timeout")
				},
				DeleteFunc: func(context.Context, ...string) error { return nil },
			}
		})
		cfg.Observer = observer
	})
	snapshot, err := service.LoadSnapshot(context.Background(), bizPrincipal())
	if err != nil {
		t.Fatalf("cache write failure must not fail request: %v", err)
	}
	if snapshot.SubjectID != "u-1" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if !observer.hasStage("cache_write") {
		t.Fatalf("cache write failure must be observed: %v", observer.stages)
	}
}

// TestAuthorizeServiceRejectsSubjectMismatch 验证仓储返回其他主体时失败关闭。
func TestAuthorizeServiceRejectsSubjectMismatch(t *testing.T) {
	repo := &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		snapshot := bizSnapshot()
		snapshot.SubjectID = "u-2"
		return snapshot, nil
	}}
	service := newService(t, repo)
	_, err := service.LoadSnapshot(context.Background(), bizPrincipal())
	var appErr *apperr.AppError
	if !stderrors.As(err, &appErr) || appErr.Code != apperr.CodeServiceUnavailable {
		t.Fatalf("err=%v want 50300", err)
	}
}

// TestAuthorizeServiceRejectsOversizedSnapshot 验证超出上限失败关闭而非截断。
func TestAuthorizeServiceRejectsOversizedSnapshot(t *testing.T) {
	repo := &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return bizSnapshot(), nil
	}}
	service := newService(t, repo, func(cfg *app.AuthorizeConfig) { cfg.MaxRoles = 0 })
	// MaxRoles=0 使用默认值 20，此处手工构造超限快照。
	snapshot := bizSnapshot()
	roles := make([]string, app.DefaultMaxRoles+1)
	for i := range roles {
		roles[i] = "role"
	}
	snapshot.Roles = roles
	repo.load = func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) { return snapshot, nil }
	if _, err := service.LoadSnapshot(context.Background(), bizPrincipal()); err == nil {
		t.Fatal("oversized snapshot must fail closed")
	}

	limits := newService(t, &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return bizSnapshot(), nil
	}}, func(cfg *app.AuthorizeConfig) { cfg.MaxPermissions = 1 })
	_, err := limits.LoadSnapshot(context.Background(), bizPrincipal())
	var appErr *apperr.AppError
	if !stderrors.As(err, &appErr) || appErr.Code != apperr.CodeServiceUnavailable {
		t.Fatalf("err=%v want 50300", err)
	}
}

// TestAuthorizeServiceReusesContextSnapshot 验证同一请求只加载一次。
func TestAuthorizeServiceReusesContextSnapshot(t *testing.T) {
	repo := &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return bizSnapshot(), nil
	}}
	service := newService(t, repo)
	cached := authz.Snapshot{
		Channel: "biz", SubjectType: "merchant_staff", SubjectID: "u-1", MerchantID: "m-1",
		Permissions: []string{"iam:role:read"},
	}
	ctx := authz.WithSnapshot(context.Background(), cached)
	got, err := service.LoadSnapshot(ctx, bizPrincipal())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Permissions) != 1 || got.Permissions[0] != "iam:role:read" {
		t.Fatalf("snapshot=%+v", got)
	}
	if calls := repo.calls.Load(); calls != 0 {
		t.Fatalf("repository calls=%d want 0", calls)
	}

	// 主体不一致时不得复用（防止缓存 / context 串租户）。
	other := authz.Snapshot{
		Channel: "biz", SubjectType: "merchant_staff", SubjectID: "u-2", MerchantID: "m-1",
	}
	got, err = service.LoadSnapshot(authz.WithSnapshot(context.Background(), other), bizPrincipal())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.SubjectID != "u-1" || repo.calls.Load() != 1 {
		t.Fatalf("got=%+v calls=%d", got, repo.calls.Load())
	}
}

// TestNewAuthorizeServiceValidation 验证装配期必填依赖与参数范围。
func TestNewAuthorizeServiceValidation(t *testing.T) {
	repo := &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return bizSnapshot(), nil
	}}
	cache := newCache(t)
	cases := []struct {
		name string
		cfg  app.AuthorizeConfig
	}{
		{name: "missing repository", cfg: app.AuthorizeConfig{Cache: cache}},
		{name: "missing cache", cfg: app.AuthorizeConfig{Repository: repo}},
		{name: "negative timeout", cfg: app.AuthorizeConfig{Repository: repo, Cache: cache, LoadTimeout: -time.Second}},
		{name: "negative limits", cfg: app.AuthorizeConfig{Repository: repo, Cache: cache, MaxRoles: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := app.NewAuthorizeService(tc.cfg); err == nil {
				t.Fatal("invalid config must be rejected")
			}
		})
	}
	// nil 服务必须失败关闭。
	var service *app.AuthorizeService
	if _, err := service.LoadSnapshot(context.Background(), bizPrincipal()); err == nil {
		t.Fatal("nil service must fail closed")
	}
}

// recordingObserver 记录加载来源、规模与错误阶段。
type recordingObserver struct {
	mu              sync.Mutex
	sources         []iampcache.Layer
	stages          []string
	lastPermissions int
}

func (o *recordingObserver) OnSnapshotLoad(_ context.Context, _ string, source iampcache.Layer, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sources = append(o.sources, source)
}

func (o *recordingObserver) OnSnapshot(_ context.Context, _ string, permissions int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastPermissions = permissions
}

func (o *recordingObserver) OnSnapshotError(_ context.Context, stage string, _ error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stages = append(o.stages, stage)
}

func (o *recordingObserver) hasSource(source iampcache.Layer) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, candidate := range o.sources {
		if candidate == source {
			return true
		}
	}
	return false
}

func (o *recordingObserver) hasStage(stage string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, candidate := range o.stages {
		if candidate == stage {
			return true
		}
	}
	return false
}
