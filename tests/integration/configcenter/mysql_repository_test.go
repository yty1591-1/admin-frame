// Package configcenter_test 是配置中心 M1（数据模型与仓储）的 MySQL 集成测试。
//
// 覆盖 docs/config-center-plan.md 附录 A 冻结契约的持久化语义：
// 唯一键冲突、乐观锁 CAS、发布快照头 + 条目原子写入、发布状态迁移、
// 审批记录与生效指针 CAS（并发切换只有一个赢家）。
// 未设置 CONFIGCENTER_TEST_MYSQL_DSN 时显式跳过（不允许静默通过）。
package configcenter_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/testdb"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// envDSN 是配置中心集成测试的连接串开关。
const envDSN = "CONFIGCENTER_TEST_MYSQL_DSN"

var (
	setupOnce sync.Once
	setupDB   *sql.DB
	setupErr  error
)

var testNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

// testDB 返回已完成迁移的测试数据库；未配置 DSN 时跳过当前用例。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(envDSN))
	if dsn == "" {
		t.Skipf("%s is not set: configcenter integration test skipped (CI provides mysql:8 service)", envDSN)
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("%s must enable parseTime=true so DATETIME(3) scans into time.Time", envDSN)
	}
	setupOnce.Do(func() { setupDB, setupErr = openAndMigrate(dsn) })
	if setupErr != nil {
		t.Fatalf("prepare integration database: %v", setupErr)
	}
	reset(t, setupDB)
	return setupDB
}

// openAndMigrate 打开连接并应用 migrations/ 下的全部脚本。
func openAndMigrate(rawDSN string) (*sql.DB, error) {
	dsn, err := db.ForceMySQLCollation("mysql", rawDSN, db.MySQLDefaultCollation)
	if err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(8)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := testdb.Migrate(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return sqlDB, nil
}

// reset 按依赖逆序清空配置中心表族。
func reset(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	tables := []string{
		"config_release_review",
		"config_release_pointer",
		"config_release_item",
		"config_release",
		"config_item_version",
		"config_item",
	}
	for _, table := range tables {
		//nolint:gosec // G202: table 来自白名单常量切片，非用户输入。
		if _, err := sqlDB.Exec("DELETE FROM " + table); err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}
}

func mustRepos(t *testing.T, sqlDB *sql.DB) *persistence.Repositories {
	t.Helper()
	repos, err := persistence.New(sqlDB)
	if err != nil {
		t.Fatalf("new repositories: %v", err)
	}
	return repos
}

// sameJSON 比较两段 JSON 文本的语义是否相等。
//
// MySQL JSON 列会重排键序并规范化空白（写入 {"min":"1s","max":"60s"} 读回为
// {"max": "60s", "min": "1s"}），因此不能按字节比较；语义相等才是契约要求。
func sameJSON(t *testing.T, want, got string) bool {
	t.Helper()
	var wantValue, gotValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want json %q: %v", want, err)
	}
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("decode got json %q: %v", got, err)
	}
	return reflect.DeepEqual(wantValue, gotValue)
}

func newItem(id int64, key string) domain.Item {
	return domain.Item{
		ID:          id,
		Env:         domain.EnvTest,
		Namespace:   "platform",
		Key:         key,
		ValueType:   domain.ValueTypeString,
		Description: "集成测试配置",
		Owner:       "team-platform",
		Status:      domain.ItemStatusDraft,
		Version:     1,
		CreatedBy:   "admin-1",
		UpdatedBy:   "admin-1",
		CreatedAt:   testNow,
		UpdatedAt:   testNow,
	}
}

func newRelease(id, releaseNo int64) domain.Release {
	return domain.Release{
		ID:           id,
		Env:          domain.EnvTest,
		Namespace:    "platform",
		ReleaseNo:    releaseNo,
		Name:         "集成测试发布",
		Status:       domain.ReleaseStatusDraft,
		Strategy:     domain.StrategyAll,
		SnapshotHash: strings.Repeat("b", 64),
		CreatedBy:    "admin-1",
		CreatedAt:    testNow,
		UpdatedAt:    testNow,
	}
}

// TestItemRepositoryCRUDAndCAS 覆盖配置键元数据与只追加版本。
func TestItemRepositoryCRUDAndCAS(t *testing.T) {
	sqlDB := testDB(t)
	repos := mustRepos(t, sqlDB)
	ctx := context.Background()

	item := newItem(9001, "http.timeout")
	item.ValidationJSON = `{"min":"1s","max":"60s"}`
	expiresAt := testNow.Add(24 * time.Hour)
	item.ExpiresAt = &expiresAt
	if err := repos.Items.Create(ctx, item); err != nil {
		t.Fatalf("create item: %v", err)
	}

	got, err := repos.Items.GetByKey(ctx, item.Env, item.Namespace, item.Key)
	if err != nil {
		t.Fatalf("get by key: %v", err)
	}
	if got.ValueType != item.ValueType || !sameJSON(t, item.ValidationJSON, got.ValidationJSON) ||
		got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}

	// 三元组唯一键。
	if err := repos.Items.Create(ctx, newItem(9002, item.Key)); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("duplicate triple must be ErrDuplicateKey, got %v", err)
	}
	if _, err := repos.Items.GetByID(ctx, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing item must be ErrNotFound, got %v", err)
	}

	// 乐观锁：陈旧版本更新被拒，递增后成功。
	next := got
	next.Description = "更新后的描述"
	next.Version = got.Version + 1
	next.UpdatedBy = "admin-2"
	if err := repos.Items.UpdateCAS(ctx, next, got.Version); err != nil {
		t.Fatalf("cas update: %v", err)
	}
	stale := got
	stale.Description = "陈旧"
	if err := repos.Items.UpdateCAS(ctx, stale, got.Version); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale update must be ErrVersionConflict, got %v", err)
	}

	// 只追加版本。
	version := domain.ItemVersion{
		ID:            9101,
		ItemID:        item.ID,
		Version:       1,
		ValueText:     "10s",
		ValueHash:     domain.HashValue("10s"),
		ChangeSummary: "初始版本",
		CreatedBy:     "admin-1",
		CreatedAt:     testNow,
	}
	if err := repos.Items.AppendVersion(ctx, version); err != nil {
		t.Fatalf("append version: %v", err)
	}
	if err := repos.Items.AppendVersion(ctx, version); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("duplicate version must be ErrDuplicateKey, got %v", err)
	}
	loaded, err := repos.Items.GetVersion(ctx, item.ID, 1)
	if err != nil || loaded.ValueText != "10s" || loaded.ValueHash != version.ValueHash {
		t.Fatalf("get version: %v %+v", err, loaded)
	}

	// 分页与状态过滤。
	for i, key := range []string{"a.first", "b.second"} {
		extra := newItem(int64(9100+i), key)
		if err := repos.Items.Create(ctx, extra); err != nil {
			t.Fatalf("create extra item: %v", err)
		}
	}
	page, total, err := repos.Items.List(ctx, item.Env, item.Namespace, "", 2, 0)
	if err != nil || total != 3 || len(page) != 2 || page[0].Key != "a.first" {
		t.Fatalf("list page mismatch: %v total=%d page=%+v", err, total, page)
	}
	active, total, err := repos.Items.List(ctx, item.Env, item.Namespace, domain.ItemStatusActive, 20, 0)
	if err != nil || total != 0 || len(active) != 0 {
		t.Fatalf("status filter mismatch: %v total=%d items=%+v", err, total, active)
	}
}

// TestReleaseRepositorySnapshotAndTransition 覆盖发布快照原子写入与状态迁移。
func TestReleaseRepositorySnapshotAndTransition(t *testing.T) {
	sqlDB := testDB(t)
	repos := mustRepos(t, sqlDB)
	ctx := context.Background()

	release := newRelease(9201, 1)
	release.Strategy = domain.StrategyTenantList
	release.TargetRuleJSON = `{"tenant_ids":["t-1","t-2"]}`
	items := []domain.ReleaseItem{
		{ReleaseID: release.ID, ItemID: 9001, ItemVersion: 1, ConfigKey: "http.timeout", ValueHash: domain.HashValue("10s")},
		{ReleaseID: release.ID, ItemID: 9002, ItemVersion: 2, ConfigKey: "http.retries", ValueHash: domain.HashValue("3")},
	}
	if err := repos.Releases.Create(ctx, release, items); err != nil {
		t.Fatalf("create release: %v", err)
	}

	loaded, err := repos.Releases.GetByNo(ctx, release.Env, release.Namespace, release.ReleaseNo)
	if err != nil {
		t.Fatalf("get by no: %v", err)
	}
	if loaded.Strategy != domain.StrategyTenantList || !sameJSON(t, release.TargetRuleJSON, loaded.TargetRuleJSON) {
		t.Fatalf("release roundtrip mismatch: %+v", loaded)
	}

	storedItems, err := repos.Releases.ListItems(ctx, release.ID)
	if err != nil || len(storedItems) != 2 || storedItems[0].ItemID != 9001 || storedItems[1].ConfigKey != "http.retries" {
		t.Fatalf("release items mismatch: %v %+v", err, storedItems)
	}

	no, err := repos.Releases.NextReleaseNo(ctx, release.Env, release.Namespace)
	if err != nil || no != 2 {
		t.Fatalf("next release no: %v %d", err, no)
	}

	// 头写入成功但条目失败时必须整体回滚（原子性）。
	brokenItems := []domain.ReleaseItem{{ReleaseID: 9999, ItemID: 9001, ItemVersion: 1, ConfigKey: "http.timeout", ValueHash: domain.HashValue("1")}}
	if err := repos.Releases.Create(ctx, newRelease(9202, 2), brokenItems); !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("mismatched release item must be ErrStateConflict, got %v", err)
	}
	if _, err := repos.Releases.Get(ctx, 9202); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("failed release create must roll back header, got %v", err)
	}

	// 状态机：draft → pending_review → approved。
	if err := repos.Releases.Transition(ctx, release.ID, domain.ReleaseStatusDraft, domain.ReleaseStatusActive, "admin-1", testNow); !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("illegal transition must be ErrStateConflict, got %v", err)
	}
	if err := repos.Releases.Transition(ctx, release.ID, domain.ReleaseStatusDraft, domain.ReleaseStatusPendingReview, "submitter", testNow); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := repos.Releases.Transition(ctx, release.ID, domain.ReleaseStatusDraft, domain.ReleaseStatusPendingReview, "submitter", testNow); !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("stale transition must be ErrStateConflict, got %v", err)
	}
	if err := repos.Releases.Transition(ctx, release.ID, domain.ReleaseStatusPendingReview, domain.ReleaseStatusApproved, "reviewer", testNow.Add(time.Minute)); err != nil {
		t.Fatalf("approve: %v", err)
	}
	approved, err := repos.Releases.Get(ctx, release.ID)
	if err != nil || approved.Status != domain.ReleaseStatusApproved ||
		approved.SubmittedBy != "submitter" || approved.ApprovedBy != "reviewer" || approved.ApprovedAt == nil {
		t.Fatalf("approved mismatch: %v %+v", err, approved)
	}

	// 审批记录追加与排序。
	reviews := []domain.ReleaseReview{
		{ID: 9302, ReleaseID: release.ID, Reviewer: "b", Decision: domain.DecisionApproved, Comment: "ok", CreatedAt: testNow.Add(2 * time.Minute)},
		{ID: 9301, ReleaseID: release.ID, Reviewer: "a", Decision: domain.DecisionApproved, Comment: "lgtm", CreatedAt: testNow.Add(time.Minute)},
	}
	for _, review := range reviews {
		if err := repos.Releases.AppendReview(ctx, review); err != nil {
			t.Fatalf("append review: %v", err)
		}
	}
	if err := repos.Releases.AppendReview(ctx, reviews[0]); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("duplicate review must be ErrDuplicateKey, got %v", err)
	}
	listed, err := repos.Releases.ListReviews(ctx, release.ID)
	if err != nil || len(listed) != 2 || listed[0].Reviewer != "a" || listed[1].Reviewer != "b" {
		t.Fatalf("reviews order mismatch: %v %+v", err, listed)
	}
}

// TestPointerCompareAndSwap 覆盖指针初始化、CAS 失败与并发切换唯一赢家。
func TestPointerCompareAndSwap(t *testing.T) {
	sqlDB := testDB(t)
	repos := mustRepos(t, sqlDB)
	ctx := context.Background()

	if _, err := repos.Pointers.Get(ctx, domain.EnvTest, "platform"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing pointer must be ErrNotFound, got %v", err)
	}

	pointer := domain.Pointer{Env: domain.EnvTest, Namespace: "platform", ActiveReleaseID: 1, Version: 1, UpdatedAt: testNow}
	if err := repos.Pointers.Create(ctx, pointer); err != nil {
		t.Fatalf("create pointer: %v", err)
	}
	if err := repos.Pointers.Create(ctx, pointer); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("duplicate pointer must be ErrDuplicateKey, got %v", err)
	}

	if _, err := repos.Pointers.CompareAndSwap(ctx, pointer.Env, pointer.Namespace, 99, 2, testNow); !errors.Is(err, domain.ErrPointerConflict) {
		t.Fatalf("stale cas must be ErrPointerConflict, got %v", err)
	}

	// 并发从 version=1 切换：只有一个成功，另一个必须 ErrPointerConflict。
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		successes int
		conflicts int
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(releaseID int64) {
			defer wg.Done()
			_, casErr := repos.Pointers.CompareAndSwap(ctx, pointer.Env, pointer.Namespace, 1, releaseID, testNow.Add(time.Minute))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case casErr == nil:
				successes++
			case errors.Is(casErr, domain.ErrPointerConflict):
				conflicts++
			default:
				t.Errorf("unexpected cas error: %v", casErr)
			}
		}(int64(10 + i))
	}
	wg.Wait()
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent cas must have exactly one winner: success=%d conflict=%d", successes, conflicts)
	}

	stored, err := repos.Pointers.Get(ctx, pointer.Env, pointer.Namespace)
	if err != nil || stored.Version != 2 || stored.ActiveReleaseID < 10 {
		t.Fatalf("stored pointer mismatch: %v %+v", err, stored)
	}
}

// TestItemVersionListAndPagination 覆盖版本历史的分页、总数与不存在键的空列表语义。
func TestItemVersionListAndPagination(t *testing.T) {
	sqlDB := testDB(t)
	repos := mustRepos(t, sqlDB)
	ctx := context.Background()

	item := newItem(9401, "notify.retry_times")
	if err := repos.Items.Create(ctx, item); err != nil {
		t.Fatalf("create item: %v", err)
	}
	loaded, err := repos.Items.GetByID(ctx, item.ID)
	if err != nil || loaded.Key != item.Key || loaded.Sensitive {
		t.Fatalf("get by id mismatch: %v %+v", err, loaded)
	}

	for version := int64(1); version <= 3; version++ {
		value := strconv.FormatInt(version, 10)
		record := domain.ItemVersion{
			ID:            9400 + version,
			ItemID:        item.ID,
			Version:       version,
			ValueText:     value,
			ValueHash:     domain.HashValue(value),
			ChangeSummary: "批量写入",
			CreatedBy:     "admin-1",
			CreatedAt:     testNow.Add(time.Duration(version) * time.Minute),
		}
		if err := repos.Items.AppendVersion(ctx, record); err != nil {
			t.Fatalf("append version %d: %v", version, err)
		}
	}

	firstPage, total, err := repos.Items.ListVersions(ctx, item.ID, 2, 0)
	if err != nil || total != 3 || len(firstPage) != 2 ||
		firstPage[0].Version != 1 || firstPage[1].Version != 2 {
		t.Fatalf("first page mismatch: %v total=%d page=%+v", err, total, firstPage)
	}
	secondPage, total, err := repos.Items.ListVersions(ctx, item.ID, 2, 2)
	if err != nil || total != 3 || len(secondPage) != 1 || secondPage[0].Version != 3 {
		t.Fatalf("second page mismatch: %v total=%d page=%+v", err, total, secondPage)
	}

	// 不存在的键返回空列表而不是错误，便于管理面直接渲染空态。
	empty, total, err := repos.Items.ListVersions(ctx, 999999, 10, 0)
	if err != nil || total != 0 || len(empty) != 0 {
		t.Fatalf("missing item versions must be empty: %v total=%d page=%+v", err, total, empty)
	}
}

// TestReleaseListSignatureAndActivation 覆盖发布列表过滤、快照签名幂等写入与生效时间落库。
func TestReleaseListSignatureAndActivation(t *testing.T) {
	sqlDB := testDB(t)
	repos := mustRepos(t, sqlDB)
	ctx := context.Background()

	draft := newRelease(9501, 1)
	pending := newRelease(9502, 2)
	for _, release := range []domain.Release{draft, pending} {
		if err := repos.Releases.Create(ctx, release, nil); err != nil {
			t.Fatalf("create release %d: %v", release.ID, err)
		}
	}
	if err := repos.Releases.Transition(ctx, pending.ID, domain.ReleaseStatusDraft,
		domain.ReleaseStatusPendingReview, "submitter", testNow); err != nil {
		t.Fatalf("submit pending release: %v", err)
	}

	// 列表按 release_no 倒序，并支持状态过滤。
	all, total, err := repos.Releases.List(ctx, draft.Env, draft.Namespace, "", 10, 0)
	if err != nil || total != 2 || len(all) != 2 || all[0].ReleaseNo != 2 {
		t.Fatalf("list all mismatch: %v total=%d releases=%+v", err, total, all)
	}
	drafts, total, err := repos.Releases.List(ctx, draft.Env, draft.Namespace, domain.ReleaseStatusDraft, 10, 0)
	if err != nil || total != 1 || len(drafts) != 1 || drafts[0].ID != draft.ID {
		t.Fatalf("status filter mismatch: %v total=%d releases=%+v", err, total, drafts)
	}
	page, total, err := repos.Releases.List(ctx, draft.Env, draft.Namespace, "", 1, 1)
	if err != nil || total != 2 || len(page) != 1 || page[0].ReleaseNo != 1 {
		t.Fatalf("pagination mismatch: %v total=%d releases=%+v", err, total, page)
	}

	// ADR-0016：UpdateSnapshotSignature 仅校验发布存在，不再落库签名列。
	signedAt := testNow.Add(3 * time.Minute)
	before, err := repos.Releases.Get(ctx, pending.ID)
	if err != nil {
		t.Fatalf("get before signature noop: %v", err)
	}
	if err := repos.Releases.UpdateSnapshotSignature(ctx, pending.ID, "sig-b64", "kid-1", signedAt); err != nil {
		t.Fatalf("update signature: %v", err)
	}
	signed, err := repos.Releases.Get(ctx, pending.ID)
	if err != nil || signed.SnapshotSignature != "" || signed.SnapshotKeyID != "" {
		t.Fatalf("signature must stay empty after noop: %v %+v", err, signed)
	}
	if !signed.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("noop signature must not bump updated_at: before=%v after=%v", before.UpdatedAt, signed.UpdatedAt)
	}
	if err := repos.Releases.UpdateSnapshotSignature(ctx, 999999, "sig", "kid", signedAt); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing release signature must be ErrNotFound, got %v", err)
	}

	// approved → active 必须写入 published_at（回滚审计依赖该时间点）。
	if err := repos.Releases.Transition(ctx, pending.ID, domain.ReleaseStatusPendingReview,
		domain.ReleaseStatusApproved, "reviewer", signedAt); err != nil {
		t.Fatalf("approve: %v", err)
	}
	publishedAt := signedAt.Add(time.Minute)
	if err := repos.Releases.Transition(ctx, pending.ID, domain.ReleaseStatusApproved,
		domain.ReleaseStatusActive, "publisher", publishedAt); err != nil {
		t.Fatalf("activate: %v", err)
	}
	active, err := repos.Releases.Get(ctx, pending.ID)
	if err != nil || active.Status != domain.ReleaseStatusActive ||
		active.PublishedAt == nil || !active.PublishedAt.Equal(publishedAt) {
		t.Fatalf("active release mismatch: %v %+v", err, active)
	}
}
