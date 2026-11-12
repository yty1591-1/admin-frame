package app_test

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// newSigner 生成测试密钥并返回签名器与验签器。
func newSigner(t *testing.T) (*configclient.Signer, *configclient.Verifier) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	signer, err := configclient.NewSigner("k1", privateKey)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	verifier, err := configclient.NewVerifier(map[string]ed25519.PublicKey{"k1": publicKey})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return signer, verifier
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

func TestPublishSignsSnapshotAndClientVerifies(t *testing.T) {
	repos := memory.New()
	signer, verifier := newSigner(t)
	service, err := app.NewReleaseService(repos.Items, repos.Releases, repos.Pointers,
		&fakeEmitter{}, newIDs(), app.Options{
			Service: "admin-api", Now: func() time.Time { return testNow }, Signer: signer,
		})
	if err != nil {
		t.Fatalf("new release service: %v", err)
	}
	ctx := context.Background()
	itemID := seedItem(t, repos, 1001, "http.timeout", "15s")
	sensitiveID := seedSensitiveItem(t, repos, 1002, "smtp.password", "secret")

	release, err := service.CreateRelease(ctx, app.CreateReleaseInput{
		Env: "dev", Namespace: "platform", Name: "签名发布", Strategy: domain.StrategyAll,
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
	if published.SnapshotKeyID != "k1" || published.SnapshotSignature == "" {
		t.Fatalf("publish must record signature: %+v", published)
	}
	stored, err := repos.Releases.Get(ctx, release.ID)
	if err != nil {
		t.Fatalf("get release: %v", err)
	}
	if stored.SnapshotSignature != published.SnapshotSignature ||
		stored.SnapshotKeyID != published.SnapshotKeyID {
		t.Fatalf("signature not persisted: %+v", stored)
	}

	snapshots, err := app.NewSnapshotService(repos.Items, repos.Releases, repos.Pointers)
	if err != nil {
		t.Fatalf("new snapshot service: %v", err)
	}
	snapshot, err := snapshots.Load(ctx, "dev", "platform")
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if err := verifier.Verify(snapshot); err != nil {
		t.Fatalf("verify snapshot: %v", err)
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

func TestSnapshotServiceRequiresRepositories(t *testing.T) {
	repos := memory.New()
	if _, err := app.NewSnapshotService(nil, repos.Releases, repos.Pointers); err == nil {
		t.Fatal("snapshot service without item repository must be rejected")
	}
	if _, err := app.NewSnapshotService(repos.Items, nil, repos.Pointers); err == nil {
		t.Fatal("snapshot service without release repository must be rejected")
	}
	if _, err := app.NewSnapshotService(repos.Items, repos.Releases, nil); err == nil {
		t.Fatal("snapshot service without pointer repository must be rejected")
	}
}
