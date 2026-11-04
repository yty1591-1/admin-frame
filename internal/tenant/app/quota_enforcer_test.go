package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// isQuotaExhausted 判定是否为 42900（配额耗尽统一语义）。
func isQuotaExhausted(err error) bool {
	var appErr *apperr.AppError
	return errors.As(err, &appErr) && appErr.Code == apperr.CodeTooManyRequests
}

// enforcerFixture 建立 user=2 / api.rps=3 两行配额的执行器。
func enforcerFixture(t *testing.T) (*app.QuotaEnforcer, *persistence.MemoryRepository) {
	t.Helper()
	repo := persistence.NewMemoryRepository()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, q := range []domain.Quota{
		{TenantID: "t-1", Resource: "user", Limit: 2, UpdatedAt: now},
		{TenantID: "t-1", Resource: "api.rps", Limit: 3, UpdatedAt: now},
	} {
		if err := repo.UpsertQuota(context.Background(), q); err != nil {
			t.Fatalf("seed quota: %v", err)
		}
	}
	enforcer, err := app.NewQuotaEnforcer(repo, app.QuotaEnforcerOptions{
		Defaults: map[string]int64{"user": 5},
	})
	if err != nil {
		t.Fatalf("NewQuotaEnforcer: %v", err)
	}
	return enforcer, repo
}

// TestQuotaEnforcerRPSLimit 限流型资源：limit=3 的令牌桶第 4 次扣减必须 42900。
func TestQuotaEnforcerRPSLimit(t *testing.T) {
	enforcer, _ := enforcerFixture(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := enforcer.Allow(ctx, "t-1", "api.rps", "", 1); err != nil {
			t.Fatalf("request %d must pass: %v", i+1, err)
		}
	}
	if _, err := enforcer.Allow(ctx, "t-1", "api.rps", "", 1); !isQuotaExhausted(err) {
		t.Fatalf("4th request must be 42900, got %v", err)
	}
}

// TestQuotaEnforcerUnlimited 与缺行默认：-1 不限；未配置资源且无默认
// 视为不限；登记了默认的资源缺行用默认值。
func TestQuotaEnforcerUnlimited(t *testing.T) {
	enforcer, _ := enforcerFixture(t)
	ctx := context.Background()

	// 显式 -1（未 seed 该行，走缺行不限分支验证口径由 resolve 覆盖；
	// user 有行=2）。
	for i := 0; i < 10; i++ {
		if _, err := enforcer.Allow(ctx, "t-2", "api.rps", "", 1); err != nil {
			t.Fatalf("unconfigured tenant resource must be unlimited: %v", err)
		}
	}
	// 缺行但登记了默认值：Check 用默认 5。
	if err := enforcer.Check(ctx, "t-9", "user", 5); err != nil {
		t.Fatalf("projected within default must pass: %v", err)
	}
	if err := enforcer.Check(ctx, "t-9", "user", 6); !isQuotaExhausted(err) {
		t.Fatalf("projected beyond default must be 42900, got %v", err)
	}
}

// TestQuotaEnforcerCheck 计数型静态校验：超限 42900，不限放行。
func TestQuotaEnforcerCheck(t *testing.T) {
	enforcer, _ := enforcerFixture(t)
	ctx := context.Background()

	if err := enforcer.Check(ctx, "t-1", "user", 2); err != nil {
		t.Fatalf("within limit must pass: %v", err)
	}
	if err := enforcer.Check(ctx, "t-1", "user", 3); !isQuotaExhausted(err) {
		t.Fatalf("beyond limit must be 42900, got %v", err)
	}
}

// TestQuotaEnforcerFailOpen 仓储故障 fail-open：限流路径不因配额读失败拒绝业务。
func TestQuotaEnforcerFailOpen(t *testing.T) {
	repo := failingQuotaRepo{}
	enforcer, err := app.NewQuotaEnforcer(repo, app.QuotaEnforcerOptions{})
	if err != nil {
		t.Fatalf("NewQuotaEnforcer: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := enforcer.Allow(context.Background(), "t-1", "api.rps", "", 1); err != nil {
			t.Fatalf("backend failure must fail-open, got %v", err)
		}
	}
}

// failingQuotaRepo 模拟配额仓储故障（嵌入接口补齐其余方法）。
type failingQuotaRepo struct {
	domain.QuotaRepository
}

func (failingQuotaRepo) ListQuotas(context.Context, string) ([]domain.Quota, error) {
	return nil, context.DeadlineExceeded
}

// TestQuotaEnforcerDimensionKeying dimension 参与分键：不同维度互不挤占。
func TestQuotaEnforcerDimensionKeying(t *testing.T) {
	enforcer, _ := enforcerFixture(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := enforcer.Allow(ctx, "t-1", "api.rps", "user-a", 1); err != nil {
			t.Fatalf("dimension a request %d must pass: %v", i+1, err)
		}
	}
	// user-a 已耗尽，但独立分键的 user-b 不受影响。
	if _, err := enforcer.Allow(ctx, "t-1", "api.rps", "user-b", 1); err != nil {
		t.Fatalf("independent dimension must not be affected: %v", err)
	}
	if _, err := enforcer.Allow(ctx, "t-1", "api.rps", "user-a", 1); !isQuotaExhausted(err) {
		t.Fatalf("exhausted dimension must still be 42900, got %v", err)
	}
}
