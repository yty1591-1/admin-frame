package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	pkgcache "github.com/admin-frame/admin-frame/pkg/cache"
)

// 本文件是 M5「故障演练」的可执行版本：把 runbook 里承诺的降级行为固化成测试，
// 避免后续重构悄悄改成「故障即放行」或「故障即写入失败」。
//
// 覆盖场景（docs/rbac-iam.md §15 / docs/runbooks/iam.md）：
//   - L2（Redis）不可用：鉴权降级回源 DB，不返回 5xx；
//   - 缓存失效失败：管理写入仍然成功，由 TTL 兜底；
//   - Outbox 出口瞬时失败：积压可观测，退避后自动追平。

// unavailableStore 模拟 Redis 整体不可用（读 / 写 / 删全部报错）。
func unavailableStore() pkgcache.Store {
	errDown := errors.New("redis: connection refused")
	return pkgcache.FuncStore{
		GetFunc:    func(context.Context, string) ([]byte, error) { return nil, errDown },
		SetFunc:    func(context.Context, string, []byte, time.Duration) error { return errDown },
		DeleteFunc: func(context.Context, ...string) error { return errDown },
	}
}

// TestAuthorizeServiceDegradesWhenL2Unavailable 验证 Redis 故障时鉴权仍可用。
//
// 语义：L2 读错误视为未命中并回源 DB；L2 写失败只记录 cache_write，绝不让请求失败。
// 这也保证「Redis 抖动导致全站 503」不会发生。
func TestAuthorizeServiceDegradesWhenL2Unavailable(t *testing.T) {
	repo := &fakeRepository{load: func(context.Context, domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return bizSnapshot(), nil
	}}
	observer := &recordingObserver{}
	cache := newCache(t, func(cfg *iampcache.Config) { cfg.L2 = unavailableStore() })
	service := newService(t, repo, func(cfg *app.AuthorizeConfig) {
		cfg.Cache = cache
		cfg.Observer = observer
	})

	snapshot, err := service.LoadSnapshot(context.Background(), bizPrincipal())
	if err != nil {
		t.Fatalf("L2 outage must degrade to DB, got error: %v", err)
	}
	if snapshot.SubjectID != "u-1" || len(snapshot.Permissions) == 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if repo.calls.Load() != 1 {
		t.Fatalf("repository calls = %d, want 1 (DB fallback)", repo.calls.Load())
	}
	if sources := observer.sources; len(sources) != 1 || sources[0] != iampcache.LayerDB {
		t.Fatalf("sources = %v, want [db]", sources)
	}
	if stages := observer.stages; len(stages) != 1 || stages[0] != "cache_write" {
		t.Fatalf("error stages = %v, want [cache_write]", stages)
	}

	// 第二次请求由 L1 命中，不再回源：Redis 持续不可用也不放大 DB 压力。
	if _, err := service.LoadSnapshot(context.Background(), bizPrincipal()); err != nil {
		t.Fatalf("second load: %v", err)
	}
	if repo.calls.Load() != 1 {
		t.Fatalf("repository calls = %d, want 1 after L1 hit", repo.calls.Load())
	}
	if sources := observer.sources; len(sources) != 2 || sources[1] != iampcache.LayerL1 {
		t.Fatalf("sources = %v, want [db l1]", sources)
	}
}

// TestRoleServiceWriteSurvivesInvalidationFailure 验证缓存删除失败不阻断已提交的授权变更。
//
// 语义：失效失败只告警，由 L1 3s / L2 5m TTL 兜底；管理接口必须返回成功，
// 否则运维会反复重试并产生重复审计。
func TestRoleServiceWriteSurvivesInvalidationFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	role, err := f.roleSvc.CreateRole(ctx, app.CreateRoleCommand{
		Channel:     domain.ChannelAdmin,
		Scope:       domain.RoleScopePlatform,
		Code:        "ops_lead",
		Name:        "运营主管",
		Permissions: []iam.PermissionCode{iam.PermRoleRead},
		DataScopes:  []domain.DataScope{scope(t, domain.DataScopeMerchantSet)},
		Actor:       "1001",
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := f.bindingSvc.Bind(ctx, app.BindCommand{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "2001",
		RoleID:      role.ID,
		Actor:       "1001",
		Reason:      "排班调整",
	}); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// 模拟 Redis 删除失败：写入路径必须继续成功。
	f.invalidator.err = errors.New("redis: connection refused")
	updated, change, err := f.roleSvc.ReplacePermissions(ctx, app.ReplacePermissionsCommand{
		ID:          role.ID,
		Permissions: []iam.PermissionCode{iam.PermRoleRead, iam.PermRoleWrite},
		Version:     role.Version,
		Actor:       "1001",
		Reason:      "补授写入权限",
	})
	if err != nil {
		t.Fatalf("ReplacePermissions must not fail on invalidation error: %v", err)
	}
	if len(change.Added) != 1 || change.Added[0] != iam.PermRoleWrite || len(change.Removed) != 0 {
		t.Fatalf("change = %+v", change)
	}
	if updated.Version != role.Version+1 {
		t.Fatalf("version = %d, want %d", updated.Version, role.Version+1)
	}
	// 失效尝试失败但写入落库：仓储是最终事实，不能出现「返回成功但没写进去」。
	stored, err := f.roles.FindByID(ctx, role.ID)
	if err != nil {
		t.Fatalf("reload role: %v", err)
	}
	if len(stored.PermissionCodes()) != 2 {
		t.Fatalf("stored permissions = %v, want 2", stored.PermissionCodes())
	}
}

// TestOutboxPublisherDrainsBacklogAfterTransientFailure 验证出口抖动后积压能自动追平。
//
// 演练目标：失败一轮后 iam_outbox_pending 仍为 1（可告警），退避到期后下一轮
// 投递成功并把 pending 归零，避免「告警但永不恢复」的假活。
func TestOutboxPublisherDrainsBacklogAfterTransientFailure(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	if err := (app.OutboxRecorder{Events: repo}).Record(ctx, testAuditEvent(t)); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var (
		mu     sync.Mutex
		calls  int
		clock  = now
		failed = errors.New("audit sink timeout")
	)
	observer := &outboxObserver{}
	publisher, err := app.NewOutboxPublisher(app.OutboxPublisherOptions{
		Events: repo,
		Deliver: func(context.Context, *domain.OutboxEvent) error {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 1 {
				return failed
			}
			return nil
		},
		Policy:   domain.OutboxRetryPolicy{MaxAttempts: 3},
		Observer: observer,
		Now:      func() time.Time { mu.Lock(); defer mu.Unlock(); return clock },
	})
	if err != nil {
		t.Fatalf("NewOutboxPublisher: %v", err)
	}

	delivered, drainErr := publisher.Drain(ctx)
	if delivered != 0 || drainErr == nil {
		t.Fatalf("first drain delivered=%d err=%v, want 0 + error", delivered, drainErr)
	}
	stats, err := repo.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 1 {
		t.Fatalf("pending = %d, want 1 (backlog must stay visible)", stats.Pending)
	}

	// 时钟推进到退避之后：同一个事件必须被重新认领并投递成功。
	mu.Lock()
	clock = now.Add(2 * time.Second)
	mu.Unlock()
	delivered, drainErr = publisher.Drain(ctx)
	if delivered != 1 || drainErr != nil {
		t.Fatalf("second drain delivered=%d err=%v, want 1 + nil", delivered, drainErr)
	}
	stats, err = repo.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 0 {
		t.Fatalf("pending = %d, want 0 after recovery", stats.Pending)
	}
	if results := observer.results(); len(results) != 2 || results[0] != "retry" || results[1] != "success" {
		t.Fatalf("results = %v, want [retry success]", results)
	}
}
