// ENT-15 数据处置执行器测试：五步证据链（扫描 → 证据校验 → 边界删除 →
// 审计 → 报告）与 fail-safe 语义（无导出证据绝不裸删）。
package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// fakeBoundary 是可控的假边界（记录删除动作）。
type fakeBoundary struct {
	name  string
	calls int
	rows  int64
	err   error
}

func (b *fakeBoundary) Name() string { return b.name }

func (b *fakeBoundary) Purge(context.Context, string, int) (int64, error) {
	b.calls++
	if b.err != nil {
		return 0, b.err
	}
	return b.rows, nil
}

// purgeObserverSpy 记录观察事件。
type purgeObserverSpy struct {
	skipped []string
	purged  []string
}

func (s *purgeObserverSpy) TenantSkipped(id string)                { s.skipped = append(s.skipped, id) }
func (s *purgeObserverSpy) TenantPurged(id string, _ int, _ int64) { s.purged = append(s.purged, id) }

// purgeFixture 是处置任务的测试夹具（内存仓储 + 可控边界）。
type purgeFixture struct {
	repo       *persistence.MemoryRepository
	manifests  *persistence.MemoryExportManifestRepository
	emitter    *recordEmitter
	svc        *app.PurgeService
	boundaries []*fakeBoundary
	observed   *purgeObserverSpy
	now        time.Time
}

// newPurgeFixture 构造：hold=90 export=30，now 固定；默认边界 tenant+iam。
func newPurgeFixture(t *testing.T, boundaries ...*fakeBoundary) *purgeFixture {
	t.Helper()
	repo := persistence.NewMemoryRepository()
	seedPlan(t, repo)
	emitter := &recordEmitter{}
	manifests := persistence.NewMemoryExportManifestRepository()
	evidence, err := app.NewManifestEvidence(manifests)
	if err != nil {
		t.Fatalf("manifest evidence: %v", err)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if len(boundaries) == 0 {
		boundaries = []*fakeBoundary{{name: "tenant", rows: 5}, {name: "iam", rows: 3}}
	}
	observed := &purgeObserverSpy{}
	iface := make([]app.Boundary, len(boundaries))
	for i, b := range boundaries {
		iface[i] = b
	}
	svc, err := app.NewPurgeService(repo, manifests, evidence, iface, emitter, observed, app.PurgeOptions{
		Service: "worker", HoldDays: 90, ExportDays: 30, BatchSize: 500,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewPurgeService: %v", err)
	}
	return &purgeFixture{repo: repo, manifests: manifests, emitter: emitter, svc: svc, boundaries: boundaries, observed: observed, now: now}
}

// terminate 直接经仓储落 terminated 租户（处置扫描面向存量数据，
// 绕过状态机构造；terminatedAt 为 RFC3339）。
func (f *purgeFixture) terminate(t *testing.T, id, terminatedAt string) {
	t.Helper()
	term, err := time.Parse(time.RFC3339, terminatedAt)
	if err != nil {
		t.Fatalf("parse terminatedAt: %v", err)
	}
	tenant := domain.Tenant{
		ID: id, Code: "c-" + id, Name: "租户" + id, Status: domain.StatusTerminated,
		StatusReason: "contract ended", TerminatedAt: &term,
		Version: 2, CreatedAt: term.AddDate(0, 0, -30), UpdatedAt: term,
	}
	if err := f.repo.Create(context.Background(), tenant); err != nil {
		t.Fatalf("seed terminated tenant %s: %v", id, err)
	}
}

// recordManifest 登记一条已校验 manifest。
func (f *purgeFixture) recordManifest(t *testing.T, tenantID, sha string) {
	t.Helper()
	if err := f.manifests.Record(context.Background(), domain.ExportManifest{
		TenantID: tenantID, ManifestSHA256: sha,
		FileCount: 3, TotalRows: 42, CreatedBy: "admin-1", CreatedAt: f.now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("record manifest: %v", err)
	}
}

// TestPurgeDeletesOnlyWithExportEvidence 核心铁律：有导出证据才删；
// 未到期租户不进入处置（扫描口径过滤）。
func TestPurgeDeletesOnlyWithExportEvidence(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()

	f.terminate(t, "t-due", "2026-05-01T00:00:00Z")    // 到期（> 120d）
	f.terminate(t, "t-recent", "2026-10-01T00:00:00Z") // 保留期内
	f.recordManifest(t, "t-due", strings.Repeat("a", 64))

	report, err := f.svc.RunDue(ctx)
	if err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if report.Scanned != 1 {
		t.Fatalf("scanned = %d, want 1（未到期租户不进入扫描）", report.Scanned)
	}
	if report.Purged != 1 || report.Skipped != 0 {
		t.Fatalf("purged=%d skipped=%d, want 1/0", report.Purged, report.Skipped)
	}
	if len(f.observed.purged) != 1 || f.observed.purged[0] != "t-due" {
		t.Fatalf("observer purged = %v", f.observed.purged)
	}
	if report.Entries[0].RowsDeleted != 8 {
		t.Fatalf("rows deleted = %d, want 8 (tenant 5 + iam 3)", report.Entries[0].RowsDeleted)
	}
	if report.Entries[0].ManifestSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("manifest sha mismatch: %s", report.Entries[0].ManifestSHA256)
	}
	for _, b := range f.boundaries {
		if b.calls != 1 {
			t.Fatalf("boundary %s calls = %d, want 1", b.name, b.calls)
		}
	}
}

// TestPurgeSkipsWithoutManifest 无导出证据 → 跳过 + 告警 + 零删除。
func TestPurgeSkipsWithoutManifest(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	f.terminate(t, "t-noexport", "2026-05-01T00:00:00Z")

	report, err := f.svc.RunDue(ctx)
	if err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if report.Scanned != 1 || report.Skipped != 1 || report.Purged != 0 || report.RowsDeleted != 0 {
		t.Fatalf("must skip without manifest: %+v", report)
	}
	if len(f.observed.skipped) != 1 || f.observed.skipped[0] != "t-noexport" {
		t.Fatalf("observer skipped = %v", f.observed.skipped)
	}
	for _, b := range f.boundaries {
		if b.calls != 0 {
			t.Fatalf("boundary %s must not be called", b.name)
		}
	}
}

// TestPurgeWritesAuditAfterDelete 审计在删除后写入且字段完整（证据链第 4 步）。
func TestPurgeWritesAuditAfterDelete(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	f.terminate(t, "t-audit", "2026-05-01T00:00:00Z")
	f.recordManifest(t, "t-audit", strings.ToUpper(strings.Repeat("b", 64)))

	if _, err := f.svc.RunDue(ctx); err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	found := false
	for _, e := range f.emitter.events {
		if e.Action != "tenant.data_purge" {
			continue
		}
		found = true
		if e.ResourceID != "t-audit" || e.Outcome != audit.OutcomeSuccess {
			t.Fatalf("audit event mismatch: %+v", e)
		}
		if e.Attrs["rows_deleted"] != "8" ||
			!strings.Contains(e.Attrs["tables"], "tenant") ||
			!strings.Contains(e.Attrs["tables"], "iam") ||
			e.Attrs["export_manifest_sha256"] != strings.Repeat("b", 64) {
			t.Fatalf("audit attrs incomplete: %v", e.Attrs)
		}
	}
	if !found {
		t.Fatal("tenant.data_purge audit event missing")
	}
}

// TestPurgeAbortsOnBoundaryFailure 任一边界失败即中止本轮（幂等重试安全）。
func TestPurgeAbortsOnBoundaryFailure(t *testing.T) {
	f := newPurgeFixture(t,
		&fakeBoundary{name: "tenant", rows: 2},
		&fakeBoundary{name: "iam", err: errors.New("iam purge exploded")},
	)
	ctx := context.Background()
	f.terminate(t, "t-fail", "2026-05-01T00:00:00Z")
	f.terminate(t, "t-second", "2026-04-01T00:00:00Z")
	f.recordManifest(t, "t-fail", strings.Repeat("c", 64))
	f.recordManifest(t, "t-second", strings.Repeat("d", 64))

	report, err := f.svc.RunDue(ctx)
	if err == nil {
		t.Fatal("boundary failure must abort the round")
	}
	if report.Purged != 0 {
		t.Fatalf("no tenant may be reported purged on failure, got %d", report.Purged)
	}
	if _, err := f.repo.Get(ctx, "t-fail"); err != nil {
		t.Fatalf("failed tenant must remain for retry: %v", err)
	}
}

// stubEvidence 是固定返回的证据端口（模拟证据损坏）。
type stubEvidence struct{ sha string }

func (e *stubEvidence) VerifiedManifest(context.Context, string) (string, error) {
	return e.sha, nil
}

// TestPurgeManifestSHAConsistencyCheck 证据端口与 manifest 不一致 → 按无证据跳过。
func TestPurgeManifestSHAConsistencyCheck(t *testing.T) {
	repo := persistence.NewMemoryRepository()
	seedPlan(t, repo)
	manifests := persistence.NewMemoryExportManifestRepository()
	emitter := &recordEmitter{}
	boundary := &fakeBoundary{name: "tenant", rows: 5}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	mismatch := &stubEvidence{sha: strings.Repeat("f", 64)}
	svc, err := app.NewPurgeService(repo, manifests, mismatch, []app.Boundary{boundary}, emitter,
		nil, app.PurgeOptions{Service: "worker", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewPurgeService: %v", err)
	}
	term := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := repo.Create(context.Background(), domain.Tenant{
		ID: "t-corrupt", Code: "c-tc", Name: "坏证据", Status: domain.StatusTerminated,
		StatusReason: "ended", TerminatedAt: &term, Version: 2,
		CreatedAt: term, UpdatedAt: term,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := manifests.Record(context.Background(), domain.ExportManifest{
		TenantID: "t-corrupt", ManifestSHA256: strings.Repeat("e", 64),
		CreatedBy: "worker", CreatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	report, err := svc.RunDue(context.Background())
	if err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if report.Purged != 0 || report.Skipped != 1 || boundary.calls != 0 {
		t.Fatalf("corrupt evidence must skip: %+v", report)
	}
}
