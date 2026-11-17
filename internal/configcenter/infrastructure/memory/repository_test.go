package memory_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
)

var testNow = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func newItem(id int64, key string) domain.Item {
	return domain.Item{
		ID:          id,
		Env:         domain.EnvDev,
		Namespace:   "platform",
		Key:         key,
		ValueType:   domain.ValueTypeString,
		Description: "测试配置",
		Owner:       "team-platform",
		Status:      domain.ItemStatusDraft,
		Version:     1,
		CreatedBy:   "admin",
		UpdatedBy:   "admin",
		CreatedAt:   testNow,
		UpdatedAt:   testNow,
	}
}

func newRelease(id, releaseNo int64) domain.Release {
	return domain.Release{
		ID:           id,
		Env:          domain.EnvDev,
		Namespace:    "platform",
		ReleaseNo:    releaseNo,
		Name:         "release-" + strconv.FormatInt(releaseNo, 10),
		Status:       domain.ReleaseStatusDraft,
		Strategy:     domain.StrategyAll,
		SnapshotHash: strings.Repeat("a", 64),
		CreatedBy:    "admin",
		CreatedAt:    testNow,
		UpdatedAt:    testNow,
	}
}

func TestItemRepositoryCreateAndGet(t *testing.T) {
	repos := memory.New()
	ctx := context.Background()

	item := newItem(1, "http.timeout")
	if err := repos.Items.Create(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}

	byID, err := repos.Items.GetByID(ctx, item.ID)
	if err != nil || byID.Key != item.Key {
		t.Fatalf("get by id: %v %+v", err, byID)
	}
	byKey, err := repos.Items.GetByKey(ctx, item.Env, item.Namespace, item.Key)
	if err != nil || byKey.ID != item.ID {
		t.Fatalf("get by key: %v %+v", err, byKey)
	}

	// 同三元组不同 ID 必须撞唯一键。
	if err := repos.Items.Create(ctx, newItem(2, item.Key)); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("duplicate triple must be ErrDuplicateKey, got %v", err)
	}
	if _, err := repos.Items.GetByID(ctx, 404); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing item must be ErrNotFound, got %v", err)
	}
	if _, err := repos.Items.GetByKey(ctx, item.Env, "other", item.Key); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing key must be ErrNotFound, got %v", err)
	}
}

func TestItemRepositoryUpdateCAS(t *testing.T) {
	repos := memory.New()
	ctx := context.Background()

	item := newItem(1, "http.timeout")
	if err := repos.Items.Create(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}

	next := item
	next.Version = item.Version + 1
	next.Description = "更新后"
	if err := repos.Items.UpdateCAS(ctx, next, item.Version); err != nil {
		t.Fatalf("cas update: %v", err)
	}
	stored, err := repos.Items.GetByID(ctx, item.ID)
	if err != nil || stored.Description != "更新后" || stored.Version != 2 {
		t.Fatalf("stored mismatch: %v %+v", err, stored)
	}

	// 陈旧版本必须冲突。
	stale := item
	stale.Description = "陈旧"
	if err := repos.Items.UpdateCAS(ctx, stale, item.Version); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale version must be ErrVersionConflict, got %v", err)
	}
	if err := repos.Items.UpdateCAS(ctx, newItem(404, "http.timeout"), 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing item update must be ErrNotFound, got %v", err)
	}
}

func TestItemRepositoryVersions(t *testing.T) {
	repos := memory.New()
	ctx := context.Background()

	item := newItem(1, "http.timeout")
	if err := repos.Items.Create(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}

	for v := int64(1); v <= 3; v++ {
		version := domain.ItemVersion{
			ID:        100 + v,
			ItemID:    item.ID,
			Version:   v,
			ValueText: "10s",
			ValueHash: domain.HashValue("10s"),
			CreatedBy: "admin",
			CreatedAt: testNow.Add(time.Duration(v) * time.Second),
		}
		if err := repos.Items.AppendVersion(ctx, version); err != nil {
			t.Fatalf("append version %d: %v", v, err)
		}
	}

	dup := domain.ItemVersion{ID: 999, ItemID: item.ID, Version: 2, CreatedAt: testNow}
	if err := repos.Items.AppendVersion(ctx, dup); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("duplicate version must be ErrDuplicateKey, got %v", err)
	}
	orphan := domain.ItemVersion{ID: 998, ItemID: 404, Version: 1, CreatedAt: testNow}
	if err := repos.Items.AppendVersion(ctx, orphan); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("orphan version must be ErrNotFound, got %v", err)
	}

	got, err := repos.Items.GetVersion(ctx, item.ID, 2)
	if err != nil || got.Version != 2 {
		t.Fatalf("get version: %v %+v", err, got)
	}
	if _, err := repos.Items.GetVersion(ctx, item.ID, 9); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing version must be ErrNotFound, got %v", err)
	}

	page, total, err := repos.Items.ListVersions(ctx, item.ID, 2, 1)
	if err != nil || total != 3 || len(page) != 2 || page[0].Version != 2 || page[1].Version != 3 {
		t.Fatalf("list versions mismatch: %v total=%d page=%+v", err, total, page)
	}
}

func TestItemRepositoryListPaginationAndFilter(t *testing.T) {
	repos := memory.New()
	ctx := context.Background()

	for i, key := range []string{"a.one", "b.two", "c.three"} {
		item := newItem(int64(i+1), key)
		if i == 2 {
			item.Status = domain.ItemStatusActive
		}
		if err := repos.Items.Create(ctx, item); err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
	}

	all, total, err := repos.Items.List(ctx, domain.EnvDev, "platform", "", 2, 0)
	if err != nil || total != 3 || len(all) != 2 || all[0].Key != "a.one" || all[1].Key != "b.two" {
		t.Fatalf("first page mismatch: %v total=%d items=%+v", err, total, all)
	}
	second, _, err := repos.Items.List(ctx, domain.EnvDev, "platform", "", 2, 2)
	if err != nil || len(second) != 1 || second[0].Key != "c.three" {
		t.Fatalf("second page mismatch: %v %+v", err, second)
	}
	active, total, err := repos.Items.List(ctx, domain.EnvDev, "platform", domain.ItemStatusActive, 20, 0)
	if err != nil || total != 1 || len(active) != 1 || active[0].Key != "c.three" {
		t.Fatalf("status filter mismatch: %v total=%d items=%+v", err, total, active)
	}
}

func TestReleaseRepositoryCreateAndQuery(t *testing.T) {
	repos := memory.New()
	ctx := context.Background()

	release := newRelease(1, 1)
	items := []domain.ReleaseItem{{
		ReleaseID:   release.ID,
		ItemID:      1,
		ItemVersion: 1,
		ConfigKey:   "http.timeout",
		ValueHash:   domain.HashValue("10s"),
	}}
	if err := repos.Releases.Create(ctx, release, items); err != nil {
		t.Fatalf("create release: %v", err)
	}

	byID, err := repos.Releases.Get(ctx, release.ID)
	if err != nil || byID.ReleaseNo != 1 {
		t.Fatalf("get release: %v %+v", err, byID)
	}
	byNo, err := repos.Releases.GetByNo(ctx, release.Env, release.Namespace, 1)
	if err != nil || byNo.ID != release.ID {
		t.Fatalf("get by no: %v %+v", err, byNo)
	}

	no, err := repos.Releases.NextReleaseNo(ctx, release.Env, release.Namespace)
	if err != nil || no != 2 {
		t.Fatalf("next release no: %v %d", err, no)
	}

	// release_no 唯一键冲突。
	if err := repos.Releases.Create(ctx, newRelease(2, 1), nil); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("duplicate release no must be ErrDuplicateKey, got %v", err)
	}
	// 条目归属错误必须在写入前拒绝。
	badItems := []domain.ReleaseItem{{ReleaseID: 99, ItemID: 1, ItemVersion: 1, ConfigKey: "http.timeout", ValueHash: domain.HashValue("1")}}
	if err := repos.Releases.Create(ctx, newRelease(3, 2), badItems); !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("mismatched release item must be ErrStateConflict, got %v", err)
	}

	listed, total, err := repos.Releases.List(ctx, domain.ReleaseListFilter{
		Env: release.Env, Namespace: release.Namespace,
	}, 20, 0)
	if err != nil || total != 1 || len(listed) != 1 {
		t.Fatalf("list releases: %v total=%d %+v", err, total, listed)
	}
	storedItems, err := repos.Releases.ListItems(ctx, release.ID)
	if err != nil || len(storedItems) != 1 || storedItems[0].ConfigKey != "http.timeout" {
		t.Fatalf("list items: %v %+v", err, storedItems)
	}
}

func TestReleaseRepositoryTransition(t *testing.T) {
	repos := memory.New()
	ctx := context.Background()

	release := newRelease(1, 1)
	if err := repos.Releases.Create(ctx, release, nil); err != nil {
		t.Fatalf("create release: %v", err)
	}

	if err := repos.Releases.Transition(ctx, release.ID, domain.ReleaseStatusDraft, domain.ReleaseStatusActive, "admin", testNow); !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("draft->active must be ErrStateConflict, got %v", err)
	}
	if err := repos.Releases.Transition(ctx, 404, domain.ReleaseStatusDraft, domain.ReleaseStatusPendingReview, "admin", testNow); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing release must be ErrNotFound, got %v", err)
	}

	if err := repos.Releases.Transition(ctx, release.ID, domain.ReleaseStatusDraft, domain.ReleaseStatusPendingReview, "submitter", testNow); err != nil {
		t.Fatalf("submit: %v", err)
	}
	submitted, err := repos.Releases.Get(ctx, release.ID)
	if err != nil || submitted.Status != domain.ReleaseStatusPendingReview || submitted.SubmittedBy != "submitter" {
		t.Fatalf("submitted mismatch: %v %+v", err, submitted)
	}

	// 前置状态已变化，重复提交必须冲突。
	if err := repos.Releases.Transition(ctx, release.ID, domain.ReleaseStatusDraft, domain.ReleaseStatusPendingReview, "submitter", testNow); !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("re-submit must be ErrStateConflict, got %v", err)
	}

	if err := repos.Releases.Transition(ctx, release.ID, domain.ReleaseStatusPendingReview, domain.ReleaseStatusApproved, "reviewer", testNow); err != nil {
		t.Fatalf("approve: %v", err)
	}
	approved, err := repos.Releases.Get(ctx, release.ID)
	if err != nil || approved.ApprovedBy != "reviewer" || approved.ApprovedAt == nil {
		t.Fatalf("approved mismatch: %v %+v", err, approved)
	}
}

func TestReleaseRepositoryReviews(t *testing.T) {
	repos := memory.New()
	ctx := context.Background()

	release := newRelease(1, 1)
	if err := repos.Releases.Create(ctx, release, nil); err != nil {
		t.Fatalf("create release: %v", err)
	}

	late := domain.ReleaseReview{ID: 2, ReleaseID: release.ID, Reviewer: "b", Decision: domain.DecisionApproved, CreatedAt: testNow.Add(time.Minute)}
	early := domain.ReleaseReview{ID: 1, ReleaseID: release.ID, Reviewer: "a", Decision: domain.DecisionApproved, CreatedAt: testNow}
	if err := repos.Releases.AppendReview(ctx, late); err != nil {
		t.Fatalf("append late review: %v", err)
	}
	if err := repos.Releases.AppendReview(ctx, early); err != nil {
		t.Fatalf("append early review: %v", err)
	}
	if err := repos.Releases.AppendReview(ctx, early); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("duplicate review must be ErrDuplicateKey, got %v", err)
	}

	reviews, err := repos.Releases.ListReviews(ctx, release.ID)
	if err != nil || len(reviews) != 2 || reviews[0].Reviewer != "a" || reviews[1].Reviewer != "b" {
		t.Fatalf("reviews order mismatch: %v %+v", err, reviews)
	}
}

func TestPointerRepository(t *testing.T) {
	repos := memory.New()
	ctx := context.Background()

	if _, err := repos.Pointers.Get(ctx, domain.EnvDev, "platform"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing pointer must be ErrNotFound, got %v", err)
	}

	pointer := domain.Pointer{Env: domain.EnvDev, Namespace: "platform", ActiveReleaseID: 1, Version: 1, UpdatedAt: testNow}
	if err := repos.Pointers.Create(ctx, pointer); err != nil {
		t.Fatalf("create pointer: %v", err)
	}
	if err := repos.Pointers.Create(ctx, pointer); !errors.Is(err, domain.ErrDuplicateKey) {
		t.Fatalf("duplicate pointer must be ErrDuplicateKey, got %v", err)
	}

	if _, err := repos.Pointers.CompareAndSwap(ctx, pointer.Env, pointer.Namespace, 99, 2, testNow); !errors.Is(err, domain.ErrPointerConflict) {
		t.Fatalf("stale cas must be ErrPointerConflict, got %v", err)
	}
	if _, err := repos.Pointers.CompareAndSwap(ctx, pointer.Env, "other", 1, 2, testNow); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing pointer cas must be ErrNotFound, got %v", err)
	}

	next, err := repos.Pointers.CompareAndSwap(ctx, pointer.Env, pointer.Namespace, 1, 2, testNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("cas: %v", err)
	}
	if next.ActiveReleaseID != 2 || next.Version != 2 || !next.UpdatedAt.Equal(testNow.Add(time.Minute)) {
		t.Fatalf("cas result mismatch: %+v", next)
	}

	stored, err := repos.Pointers.Get(ctx, pointer.Env, pointer.Namespace)
	if err != nil || stored.ActiveReleaseID != 2 || stored.Version != 2 {
		t.Fatalf("stored pointer mismatch: %v %+v", err, stored)
	}
}
