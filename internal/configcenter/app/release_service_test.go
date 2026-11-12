package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

var reviewerActor = app.Actor{SubjectID: "admin-2", ActorType: "user", Channel: "admin", TraceID: "trace-2"}

// createDraftRelease 创建一个只含单个条目的草稿发布。
func createDraftRelease(t *testing.T, service *app.ReleaseService, itemID int64, name string) domain.Release {
	t.Helper()
	release, err := service.CreateRelease(context.Background(), app.CreateReleaseInput{
		Env: "dev", Namespace: "platform", Name: name, Strategy: domain.StrategyAll,
		Entries: []app.SnapshotEntry{{ItemID: itemID, ItemVersion: 1}},
		Actor:   testActor,
	})
	if err != nil {
		t.Fatalf("create release %s: %v", name, err)
	}
	return release
}

// approveRelease 送审并由第二个管理员审批通过。
func approveRelease(t *testing.T, service *app.ReleaseService, releaseID int64) domain.Release {
	t.Helper()
	ctx := context.Background()
	if _, err := service.Submit(ctx, releaseID, testActor); err != nil {
		t.Fatalf("submit release %d: %v", releaseID, err)
	}
	approved, err := service.Approve(ctx, app.ApproveInput{
		ReleaseID: releaseID, Decision: domain.DecisionApproved, Actor: reviewerActor,
	})
	if err != nil {
		t.Fatalf("approve release %d: %v", releaseID, err)
	}
	if approved.Status != domain.ReleaseStatusApproved {
		t.Fatalf("release %d status = %s, want approved", releaseID, approved.Status)
	}
	return approved
}

func TestReleaseLifecycle(t *testing.T) {
	service, repos, emitter := newReleaseService(t)
	ctx := context.Background()
	itemID := seedItem(t, repos, 1001, "http.timeout", "10s")

	release := createDraftRelease(t, service, itemID, "首次发布")
	if release.Status != domain.ReleaseStatusDraft || release.ReleaseNo != 1 || release.SnapshotSize != 1 {
		t.Fatalf("draft release mismatch: %+v", release)
	}
	if !domain.ValidSHA256(release.SnapshotHash) {
		t.Fatalf("snapshot hash is not sha256: %s", release.SnapshotHash)
	}

	approved := approveRelease(t, service, release.ID)
	if approved.ApprovedBy != reviewerActor.SubjectID || approved.ApprovedAt == nil {
		t.Fatalf("approval not recorded: %+v", approved)
	}

	published, err := service.Publish(ctx, release.ID, nil, testActor)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if published.Status != domain.ReleaseStatusActive || published.PublishedAt == nil {
		t.Fatalf("published release mismatch: %+v", published)
	}
	pointer, err := repos.Pointers.Get(ctx, "dev", "platform")
	if err != nil {
		t.Fatalf("get pointer: %v", err)
	}
	if pointer.ActiveReleaseID != release.ID || pointer.Version != 1 {
		t.Fatalf("pointer mismatch: %+v", pointer)
	}

	events := emitter.all()
	if len(events) != 4 {
		t.Fatalf("expected 4 audit events, got %d", len(events))
	}
	if events[0].Reason != "create_release" || events[0].ResourceType != app.ResourceTypeRelease {
		t.Fatalf("unexpected create audit: %+v", events[0])
	}
	if events[2].Action != audit.ActionConfigReleaseApprove {
		t.Fatalf("unexpected approve audit: %+v", events[2])
	}
	if events[3].Action != audit.ActionConfigReleasePublish {
		t.Fatalf("unexpected publish audit: %+v", events[3])
	}
	if _, ok := events[3].Attrs["pointer_version"]; !ok {
		t.Fatal("publish audit must record pointer version")
	}
}

func TestCreateReleaseRejectsInvalidEntries(t *testing.T) {
	service, repos, _ := newReleaseService(t)
	ctx := context.Background()
	first := seedItem(t, repos, 1001, "http.timeout", "10s")
	second := seedItem(t, repos, 1002, "http.retries", "3")

	base := app.CreateReleaseInput{
		Env: "dev", Namespace: "platform", Name: "非法发布", Strategy: domain.StrategyAll, Actor: testActor,
	}

	duplicated := base
	duplicated.Entries = []app.SnapshotEntry{
		{ItemID: first, ItemVersion: 1}, {ItemID: first, ItemVersion: 1},
	}
	if _, err := service.CreateRelease(ctx, duplicated); err == nil {
		t.Fatal("duplicate entries must be rejected")
	}

	wrongScope := base
	wrongScope.Namespace = "not-platform"
	wrongScope.Entries = []app.SnapshotEntry{{ItemID: first, ItemVersion: 1}}
	if _, err := service.CreateRelease(ctx, wrongScope); err == nil {
		t.Fatal("entry from another namespace must be rejected")
	}

	missingVersion := base
	missingVersion.Entries = []app.SnapshotEntry{{ItemID: second, ItemVersion: 2}}
	if _, err := service.CreateRelease(ctx, missingVersion); err == nil {
		t.Fatal("missing item version must be rejected")
	}

	unknownItem := base
	unknownItem.Entries = []app.SnapshotEntry{{ItemID: 999999, ItemVersion: 1}}
	if _, err := service.CreateRelease(ctx, unknownItem); err == nil {
		t.Fatal("unknown item must be rejected")
	}

	empty := base
	empty.Entries = nil
	if _, err := service.CreateRelease(ctx, empty); err == nil {
		t.Fatal("empty release must be rejected")
	}
}

func TestApproveRules(t *testing.T) {
	service, repos, _ := newReleaseService(t)
	ctx := context.Background()
	itemID := seedItem(t, repos, 1001, "http.timeout", "10s")
	release := createDraftRelease(t, service, itemID, "审批规则")

	if _, err := service.Approve(ctx, app.ApproveInput{
		ReleaseID: release.ID, Decision: domain.DecisionApproved, Actor: reviewerActor,
	}); err == nil {
		t.Fatal("draft release must not be reviewed before submit")
	}
	if _, err := service.Submit(ctx, release.ID, testActor); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := service.Submit(ctx, release.ID, testActor); err == nil {
		t.Fatal("second submit must be rejected")
	}

	if _, err := service.Approve(ctx, app.ApproveInput{
		ReleaseID: release.ID, Decision: domain.DecisionApproved, Actor: testActor,
	}); !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("self review must be forbidden, got %v", err)
	}

	if _, err := service.Approve(ctx, app.ApproveInput{
		ReleaseID: release.ID, Decision: domain.DecisionRejected, Actor: reviewerActor,
	}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("rejection without comment must be bad request, got %v", err)
	}

	rejected, err := service.Approve(ctx, app.ApproveInput{
		ReleaseID: release.ID, Decision: domain.DecisionRejected, Comment: "缺少变更说明",
		Actor: reviewerActor,
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != domain.ReleaseStatusRejected {
		t.Fatalf("rejected release status = %s", rejected.Status)
	}
	if _, err := service.Publish(ctx, release.ID, nil, testActor); err == nil {
		t.Fatal("rejected release must not be published")
	}

	reviews, err := service.ListReviews(ctx, release.ID)
	if err != nil || len(reviews) != 1 {
		t.Fatalf("reviews mismatch: %v %+v", err, reviews)
	}
	if reviews[0].Decision != domain.DecisionRejected || reviews[0].Comment != "缺少变更说明" {
		t.Fatalf("unexpected review: %+v", reviews[0])
	}
}

func TestPublishSupersedesAndPointerConflict(t *testing.T) {
	service, repos, _ := newReleaseService(t)
	ctx := context.Background()
	itemID := seedItem(t, repos, 1001, "http.timeout", "10s")

	first := createDraftRelease(t, service, itemID, "第一版")
	approveRelease(t, service, first.ID)
	if _, err := service.Publish(ctx, first.ID, nil, testActor); err != nil {
		t.Fatalf("publish first: %v", err)
	}

	second := createDraftRelease(t, service, itemID, "第二版")
	approveRelease(t, service, second.ID)
	if _, err := service.Publish(ctx, second.ID, int64Ptr(1), testActor); err != nil {
		t.Fatalf("publish second: %v", err)
	}

	storedFirst, err := service.Get(ctx, first.ID)
	if err != nil || storedFirst.Status != domain.ReleaseStatusSuperseded {
		t.Fatalf("first release must be superseded: %v %+v", err, storedFirst)
	}
	storedSecond, err := service.Get(ctx, second.ID)
	if err != nil || storedSecond.Status != domain.ReleaseStatusActive {
		t.Fatalf("second release must be active: %v %+v", err, storedSecond)
	}

	third := createDraftRelease(t, service, itemID, "第三版")
	approveRelease(t, service, third.ID)
	if _, err := service.Publish(ctx, third.ID, int64Ptr(1), testActor); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("stale pointer version must conflict, got %v", err)
	}
	pointer, err := repos.Pointers.Get(ctx, "dev", "platform")
	if err != nil {
		t.Fatalf("get pointer: %v", err)
	}
	if pointer.ActiveReleaseID != second.ID || pointer.Version != 2 {
		t.Fatalf("pointer must stay on second release: %+v", pointer)
	}
}

func TestRollbackCreatesNewRelease(t *testing.T) {
	service, repos, emitter := newReleaseService(t)
	ctx := context.Background()
	itemID := seedItem(t, repos, 1001, "http.timeout", "10s")

	target := createDraftRelease(t, service, itemID, "目标版本")
	approveRelease(t, service, target.ID)
	if _, err := service.Publish(ctx, target.ID, nil, testActor); err != nil {
		t.Fatalf("publish target: %v", err)
	}

	current := createDraftRelease(t, service, itemID, "当前版本")
	approveRelease(t, service, current.ID)
	if _, err := service.Publish(ctx, current.ID, nil, testActor); err != nil {
		t.Fatalf("publish current: %v", err)
	}

	rolled, err := service.Rollback(ctx, app.RollbackInput{
		TargetReleaseID: target.ID, Actor: testActor,
	})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolled.ID == target.ID || rolled.ID == current.ID {
		t.Fatalf("rollback must create a new release: %+v", rolled)
	}
	if rolled.Status != domain.ReleaseStatusActive || rolled.SnapshotHash != target.SnapshotHash {
		t.Fatalf("rolled release mismatch: %+v", rolled)
	}
	if rolled.BasedOnReleaseID == nil || *rolled.BasedOnReleaseID != current.ID {
		t.Fatalf("rollback based_on mismatch: %+v", rolled.BasedOnReleaseID)
	}
	if rolled.ReleaseNo != current.ReleaseNo+1 {
		t.Fatalf("rollback release_no = %d, want %d", rolled.ReleaseNo, current.ReleaseNo+1)
	}

	pointer, err := repos.Pointers.Get(ctx, "dev", "platform")
	if err != nil || pointer.ActiveReleaseID != rolled.ID || pointer.Version != 3 {
		t.Fatalf("pointer mismatch after rollback: %v %+v", err, pointer)
	}
	rolledCurrent, err := service.Get(ctx, current.ID)
	if err != nil || rolledCurrent.Status != domain.ReleaseStatusRolledBack {
		t.Fatalf("previous active must be rolled_back: %v %+v", err, rolledCurrent)
	}
	storedTarget, err := service.Get(ctx, target.ID)
	if err != nil || storedTarget.Status != domain.ReleaseStatusSuperseded {
		t.Fatalf("target must stay superseded: %v %+v", err, storedTarget)
	}

	items, err := service.ListItems(ctx, rolled.ID)
	if err != nil || len(items) != 1 || items[0].ValueHash != domain.HashValue("10s") {
		t.Fatalf("rollback snapshot mismatch: %v %+v", err, items)
	}

	events := emitter.all()
	if last := events[len(events)-1]; last.Action != audit.ActionConfigReleaseRollback {
		t.Fatalf("unexpected last audit event: %+v", last)
	}

	if _, err := service.Rollback(ctx, app.RollbackInput{
		TargetReleaseID: rolled.ID, Actor: testActor,
	}); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("rolling back to active release must conflict, got %v", err)
	}

	draft := createDraftRelease(t, service, itemID, "从未发布")
	if _, err := service.Rollback(ctx, app.RollbackInput{
		TargetReleaseID: draft.ID, Actor: testActor,
	}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("rolling back to never-published release must be bad request, got %v", err)
	}
}

func TestCancelRelease(t *testing.T) {
	service, repos, _ := newReleaseService(t)
	ctx := context.Background()
	itemID := seedItem(t, repos, 1001, "http.timeout", "10s")
	release := createDraftRelease(t, service, itemID, "待取消")

	cancelled, err := service.Cancel(ctx, release.ID, "需求变更", testActor)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != domain.ReleaseStatusCancelled {
		t.Fatalf("cancelled status = %s", cancelled.Status)
	}
	if _, err := service.Submit(ctx, release.ID, testActor); err == nil {
		t.Fatal("cancelled release must not be submitted")
	}
	if _, err := service.Cancel(ctx, release.ID, "再次取消", testActor); err == nil {
		t.Fatal("cancelled release must not be cancelled twice")
	}
}

func TestSnapshotHashIsOrderIndependent(t *testing.T) {
	items := []domain.ReleaseItem{
		{ReleaseID: 1, ItemID: 2, ItemVersion: 1, ConfigKey: "http.retries", ValueHash: domain.HashValue("3")},
		{ReleaseID: 1, ItemID: 1, ItemVersion: 1, ConfigKey: "http.timeout", ValueHash: domain.HashValue("10s")},
	}
	reversed := []domain.ReleaseItem{items[1], items[0]}
	if app.SnapshotHash(items) != app.SnapshotHash(reversed) {
		t.Fatal("snapshot hash must not depend on entry order")
	}

	changed := []domain.ReleaseItem{
		items[0],
		{ReleaseID: 1, ItemID: 1, ItemVersion: 2, ConfigKey: "http.timeout", ValueHash: domain.HashValue("20s")},
	}
	if app.SnapshotHash(items) == app.SnapshotHash(changed) {
		t.Fatal("snapshot hash must change when item version changes")
	}
}
