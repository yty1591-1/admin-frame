package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
)

type unavailableReleases struct {
	domain.ReleaseRepository
}

func (unavailableReleases) Get(context.Context, int64) (domain.Release, error) {
	panic("unchanged snapshot must not load release")
}

// seedSensitiveItem 写入一个敏感配置键与版本 1。
func seedSensitiveItem(t *testing.T, repos *memory.Repositories, id int64, key, value string) int64 {
	t.Helper()
	item := domain.Item{
		ID: id, Env: domain.EnvDev, Namespace: "platform", Key: key,
		ValueType: domain.ValueTypeString, Description: "敏感键", Owner: "team-platform",
		Sensitive: true, Status: domain.ItemStatusDraft, CurrentVersion: 1, Version: 1,
		CreatedBy: "admin-1", UpdatedBy: "admin-1", CreatedAt: testNow, UpdatedAt: testNow,
	}
	if err := repos.Items.Create(context.Background(), item); err != nil {
		t.Fatalf("seed sensitive item %s: %v", key, err)
	}
	version := domain.ItemVersion{
		ID: id + 1000, ItemID: item.ID, Version: 1, ValueText: value,
		ValueHash: domain.HashValue(value), ChangeSummary: "seed", CreatedBy: "admin-1", CreatedAt: testNow,
	}
	if err := repos.Items.AppendVersion(context.Background(), version); err != nil {
		t.Fatalf("seed sensitive item version %s: %v", key, err)
	}
	return item.ID
}

func TestPublishExcludesSensitiveAndClientLoads(t *testing.T) {
	repos := memory.New()
	service, err := app.NewReleaseService(repos.Items, repos.Releases, repos.Pointers,
		&fakeEmitter{}, newIDs(), app.Options{
			Service: "admin-api", Now: func() time.Time { return testNow },
		})
	if err != nil {
		t.Fatalf("new release service: %v", err)
	}
	ctx := context.Background()
	itemID := seedItem(t, repos, 1001, "http.timeout", "15s")
	sensitiveID := seedSensitiveItem(t, repos, 1002, "smtp.password", "secret")

	release, err := service.CreateRelease(ctx, app.CreateReleaseInput{
		Env: "dev", Namespace: "platform", Name: "拉取发布", Strategy: domain.StrategyAll,
		Entries: []app.SnapshotEntry{
			{ItemID: itemID, ItemVersion: 1},
			{ItemID: sensitiveID, ItemVersion: 1},
		},
		Actor: testActor,
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	approveRelease(t, service, release.ID)

	published, err := service.Publish(ctx, release.ID, nil, testActor)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if published.SnapshotSignature != "" || published.SnapshotKeyID != "" {
		t.Fatalf("publish must leave signature empty: %+v", published)
	}

	snapshots, err := app.NewSnapshotService(repos.Items, repos.Releases, repos.Pointers)
	if err != nil {
		t.Fatalf("new snapshot service: %v", err)
	}
	// 304 快速路径只读指针；即使 release 仓储不可用也不应访问它。
	pointerOnly, err := app.NewSnapshotService(repos.Items, unavailableReleases{ReleaseRepository: repos.Releases}, repos.Pointers)
	if err != nil {
		t.Fatal(err)
	}
	pointer, err := repos.Pointers.Get(ctx, "dev", "platform")
	if err != nil {
		t.Fatal(err)
	}
	if _, unchanged, err := pointerOnly.LoadIfChanged(ctx, "dev", "platform", app.ReleaseKey(pointer.ActiveReleaseID, pointer.Version)); err != nil || !unchanged {
		t.Fatalf("unchanged snapshot should only read pointer: unchanged=%v err=%v", unchanged, err)
	}
	snapshot, err := snapshots.Load(ctx, "dev", "platform")
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if err := snapshot.ValidateIntegrity(); err != nil {
		t.Fatalf("validate snapshot: %v", err)
	}
	if got := snapshot.Values["http.timeout"]; got != "15s" {
		t.Fatalf("snapshot value = %q, want 15s", got)
	}
	if _, ok := snapshot.Values["smtp.password"]; ok {
		t.Fatal("sensitive value must not be exposed in snapshot")
	}
	if len(snapshot.Items) != 2 || snapshot.SnapshotSize != 2 {
		t.Fatalf("snapshot items = %d size = %d, want 2", len(snapshot.Items), snapshot.SnapshotSize)
	}
}
