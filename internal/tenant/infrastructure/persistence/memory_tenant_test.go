package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// fixedNow 固定时间，保证排序断言稳定。
var fixedNow = time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)

// newTenant 构造测试租户。
func newTenant(t *testing.T, id, name string) domain.Tenant {
	t.Helper()
	tenant, err := domain.NewTenant(id, name, fixedNow)
	if err != nil {
		t.Fatalf("NewTenant(%s) error = %v", id, err)
	}
	return tenant
}

// TestMemoryRepositoryCreateRejectsDuplicate 验证主键冲突映射为 ErrAlreadyExists。
func TestMemoryRepositoryCreateRejectsDuplicate(t *testing.T) {
	repo := persistence.NewMemoryRepository()
	ctx := context.Background()
	tenant := newTenant(t, "t1", "acme")
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := repo.Create(ctx, tenant); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("重复创建 error = %v, want ErrAlreadyExists", err)
	}
}

// TestMemoryRepositoryUpdateUsesVersionCAS 验证版本不一致时拒绝写入。
func TestMemoryRepositoryUpdateUsesVersionCAS(t *testing.T) {
	repo := persistence.NewMemoryRepository()
	ctx := context.Background()
	tenant := newTenant(t, "t1", "acme")
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	next, err := tenant.Transition(domain.StatusActive, "", fixedNow)
	if err != nil {
		t.Fatalf("Transition() error = %v", err)
	}
	if err := repo.UpdateStatus(ctx, next); err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}

	// 用过期的版本再写一次（模拟两个运营同时操作）。
	stale, err := tenant.Transition(domain.StatusTerminated, "退租", fixedNow)
	if err != nil {
		t.Fatalf("Transition() error = %v", err)
	}
	if err := repo.UpdateStatus(ctx, stale); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("过期版本写入 error = %v, want ErrVersionConflict", err)
	}

	reloaded, err := repo.Get(ctx, "t1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if reloaded.Status != domain.StatusActive || reloaded.Version != 2 {
		t.Fatalf("Status=%s Version=%d, want active/2", reloaded.Status, reloaded.Version)
	}
}

// TestMemoryRepositoryListFiltersAndPages 验证过滤、排序与分页边界。
func TestMemoryRepositoryListFiltersAndPages(t *testing.T) {
	repo := persistence.NewMemoryRepository()
	ctx := context.Background()
	base := fixedNow
	for i, name := range []string{"acme", "acme-cn", "beta"} {
		tenant, err := domain.NewTenant("t"+string(rune('1'+i)), name, base.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatalf("NewTenant() error = %v", err)
		}
		if err := repo.Create(ctx, tenant); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	items, total, err := repo.List(ctx, domain.Filter{Keyword: "acme"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("total=%d items=%d, want 2/2", total, len(items))
	}
	// 按创建时间倒序：acme-cn 晚于 acme。
	if items[0].Name != "acme-cn" {
		t.Fatalf("首条 = %s, want acme-cn", items[0].Name)
	}

	page, total, err := repo.List(ctx, domain.Filter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("List(page) error = %v", err)
	}
	if total != 3 || len(page) != 1 {
		t.Fatalf("total=%d len=%d, want 3/1", total, len(page))
	}

	beyond, total, err := repo.List(ctx, domain.Filter{Limit: 10, Offset: 99})
	if err != nil {
		t.Fatalf("List(beyond) error = %v", err)
	}
	if total != 3 || len(beyond) != 0 {
		t.Fatalf("越界 total=%d len=%d, want 3/0", total, len(beyond))
	}
}

// TestMemoryRepositoryGetMissing 验证不存在返回 ErrNotFound。
func TestMemoryRepositoryGetMissing(t *testing.T) {
	repo := persistence.NewMemoryRepository()
	if _, err := repo.Get(context.Background(), "absent"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
	if err := repo.UpdateStatus(context.Background(), newTenant(t, "absent", "x")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("UpdateStatus() error = %v, want ErrNotFound", err)
	}
}
