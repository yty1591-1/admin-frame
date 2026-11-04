// ENT-15 数据处置的 MySQL 集成测试：tenant_export_manifest 证据读写与
// 租户表硬删除边界（D-05 第三段的持久化验证）。
package tenant_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// TestTenantExportManifestEvidence manifest 证据读写与最新记录语义。
func TestTenantExportManifestEvidence(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLExportManifestRepository(sqlDB)
	if err != nil {
		t.Fatalf("new manifest repo: %v", err)
	}
	ctx := context.Background()

	// manifest 表不在本套件通用 reset 清单（证据表保留期独立），
	// 本用例自行清场。
	if _, err := sqlDB.Exec("DELETE FROM tenant_export_manifest"); err != nil {
		t.Fatalf("reset manifest: %v", err)
	}

	if _, err := repo.FindLatest(ctx, "t-it-manifest"); err != domain.ErrManifestNotFound {
		t.Fatalf("missing manifest must be ErrManifestNotFound, got %v", err)
	}

	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	first := domain.ExportManifest{
		TenantID: "t-it-manifest", ManifestSHA256: strings.Repeat("a", 64),
		FileCount: 2, TotalRows: 30, CreatedBy: "admin-1", CreatedAt: now.Add(-time.Hour),
	}
	if err := repo.Record(ctx, first); err != nil {
		t.Fatalf("record: %v", err)
	}
	second := first
	second.ManifestSHA256 = strings.Repeat("b", 64)
	second.CreatedAt = now
	if err := repo.Record(ctx, second); err != nil {
		t.Fatalf("record v2: %v", err)
	}

	got, err := repo.FindLatest(ctx, "t-it-manifest")
	if err != nil {
		t.Fatalf("find latest: %v", err)
	}
	if got.ManifestSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("latest manifest must win, got sha prefix %s", got.ManifestSHA256[:8])
	}

	// SHA 形态校验（非法值拒绝）。
	bad := first
	bad.ManifestSHA256 = "not-a-hash"
	if err := repo.Record(ctx, bad); err != domain.ErrManifestSHAInvalid {
		t.Fatalf("invalid sha must be ErrManifestSHAInvalid, got %v", err)
	}
}

// TestTenantTablesPurger 租户表硬删除：级联清空 + 幂等 + 非 terminated 拒绝；
// ListDueForPurge 扫描口径（terminated_at < before 才进入处置）。
func TestTenantTablesPurger(t *testing.T) {
	sqlDB := testDB(t)
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	purger, err := persistence.NewMySQLTenantPurger(sqlDB)
	if err != nil {
		t.Fatalf("new purger: %v", err)
	}
	ctx := context.Background()

	// 直落仓储构造 terminated 租户（处置扫描面向存量数据）。
	terminatedAt := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	tenant := domain.Tenant{
		ID: "t-purge-it", Code: "code-purge-it", Name: "处置集成租户",
		Status: domain.StatusTerminated, StatusReason: "contract ended",
		TerminatedAt: &terminatedAt, Version: 2,
		CreatedAt: terminatedAt.AddDate(0, 0, -30), UpdatedAt: terminatedAt,
	}
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 扫描口径：截止时点在 terminated_at 之后扫到；之前扫不到。
	due, err := repo.ListDueForPurge(ctx, terminatedAt.Add(time.Minute), 100)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	found := false
	for _, d := range due {
		if d.ID == tenant.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("due tenant must be listed after cutoff")
	}
	if early, err := repo.ListDueForPurge(ctx, terminatedAt.Add(-time.Minute), 100); err != nil || len(early) != 0 {
		t.Fatalf("not-yet-due tenant must not appear: n=%d err=%v", len(early), err)
	}

	rows, err := purger.Purge(ctx, tenant.ID, 500)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if rows < 1 {
		t.Fatalf("purge must delete at least the tenant row, got %d", rows)
	}
	if _, err := repo.Get(ctx, tenant.ID); err != domain.ErrNotFound {
		t.Fatalf("purged tenant must be gone, got %v", err)
	}

	// 幂等：重跑返回 0 行且不报错。
	if rows2, err := purger.Purge(ctx, tenant.ID, 500); err != nil || rows2 != 0 {
		t.Fatalf("re-purge must be idempotent: rows=%d err=%v", rows2, err)
	}
}
