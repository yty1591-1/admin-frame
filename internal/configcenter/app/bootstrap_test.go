package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

func newBootstrapService(t *testing.T) (*app.BootstrapService, *memory.Repositories, *fakeEmitter) {
	t.Helper()
	repos := memory.New()
	emitter := &fakeEmitter{}
	service, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		testSpecs(t), emitter, newIDs(),
		app.Options{Service: "admin-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new bootstrap service: %v", err)
	}
	return service, repos, emitter
}

func TestBootstrapSeedsBaseline(t *testing.T) {
	service, repos, emitter := newBootstrapService(t)
	ctx := context.Background()

	result, err := service.EnsureBaseline(ctx, app.BootstrapInput{
		Env: domain.EnvDev, Namespace: "platform", Actor: testActor,
	})
	if err != nil {
		t.Fatalf("ensure baseline: %v", err)
	}
	if !result.Seeded || result.ItemCount != 3 || result.ReleaseNo != 1 {
		t.Fatalf("unexpected bootstrap result: %+v", result)
	}
	if len(result.SkippedKeys) != 1 || result.SkippedKeys[0] != "smtp.password" {
		t.Fatalf("sensitive key must be skipped: %+v", result.SkippedKeys)
	}

	pointer, err := repos.Pointers.Get(ctx, domain.EnvDev, "platform")
	if err != nil || pointer.ActiveReleaseID != result.ReleaseID || pointer.Version != 1 {
		t.Fatalf("pointer mismatch: %v %+v", err, pointer)
	}
	release, err := repos.Releases.Get(ctx, result.ReleaseID)
	if err != nil {
		t.Fatalf("get release: %v", err)
	}
	if release.Status != domain.ReleaseStatusActive || release.SnapshotSize != 3 ||
		release.Name != "baseline" || release.PublishedAt == nil {
		t.Fatalf("baseline release mismatch: %+v", release)
	}
	if release.SnapshotHash != app.SnapshotHash(mustItems(t, repos, release.ID)) {
		t.Fatal("baseline snapshot hash mismatch")
	}

	item, err := repos.Items.GetByKey(ctx, domain.EnvDev, "platform", "http.timeout")
	if err != nil {
		t.Fatalf("get seeded item: %v", err)
	}
	if item.Status != domain.ItemStatusActive || item.CurrentVersion != 1 {
		t.Fatalf("seeded item mismatch: %+v", item)
	}
	version, err := repos.Items.GetVersion(ctx, item.ID, 1)
	if err != nil || version.ValueText != "10s" || version.ChangeSummary != "bootstrap" {
		t.Fatalf("seeded version mismatch: %v %+v", err, version)
	}

	events := emitter.all()
	if len(events) != 1 || events[0].Action != audit.ActionConfigBootstrapCompleted {
		t.Fatalf("unexpected audit events: %+v", events)
	}
	if events[0].Attrs["item_count"] != "3" || events[0].Attrs["namespace"] != "platform" {
		t.Fatalf("unexpected bootstrap attrs: %+v", events[0].Attrs)
	}
}

func TestBootstrapIsIdempotent(t *testing.T) {
	service, repos, emitter := newBootstrapService(t)
	ctx := context.Background()
	input := app.BootstrapInput{Env: domain.EnvDev, Namespace: "platform", Actor: testActor}

	first, err := service.EnsureBaseline(ctx, input)
	if err != nil || !first.Seeded {
		t.Fatalf("first bootstrap: %v %+v", err, first)
	}
	second, err := service.EnsureBaseline(ctx, input)
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if second.Seeded || second.ReleaseID != first.ReleaseID {
		t.Fatalf("second bootstrap must be a no-op: %+v", second)
	}

	_, total, err := repos.Releases.List(ctx, domain.ReleaseListFilter{
		Env: domain.EnvDev, Namespace: "platform",
	}, 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("release count mismatch: %v %d", err, total)
	}
	if events := emitter.all(); len(events) != 1 {
		t.Fatalf("second bootstrap must not emit audit, got %d events", len(events))
	}
}

func TestBootstrapSkipsNamespaceWithoutDefaults(t *testing.T) {
	repos := memory.New()
	emitter := &fakeEmitter{}
	registry := spec.NewRegistry()
	if err := registry.Register(domain.KeySpec{
		Namespace: "platform", Key: "smtp.password", ValueType: domain.ValueTypeString,
		Description: "SMTP 口令", Owner: "team-platform", Sensitive: true,
	}); err != nil {
		t.Fatalf("register sensitive spec: %v", err)
	}
	service, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, emitter, newIDs(),
		app.Options{Service: "admin-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new bootstrap service: %v", err)
	}

	result, err := service.EnsureBaseline(context.Background(), app.BootstrapInput{
		Env: domain.EnvDev, Namespace: "platform", Actor: testActor,
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if result.Seeded || len(result.SkippedKeys) != 1 {
		t.Fatalf("no defaults must not seed: %+v", result)
	}
	if _, err := repos.Pointers.Get(context.Background(), domain.EnvDev, "platform"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("pointer must not be created: %v", err)
	}
}

func TestBootstrapValidation(t *testing.T) {
	service, _, _ := newBootstrapService(t)
	ctx := context.Background()

	if _, err := service.EnsureBaseline(ctx, app.BootstrapInput{
		Env: domain.EnvDev, Namespace: "platform",
	}); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("missing actor must be unauthorized, got %v", err)
	}
	if _, err := service.EnsureBaseline(ctx, app.BootstrapInput{
		Env: "prod-2", Namespace: "platform", Actor: testActor,
	}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("invalid env must be bad request, got %v", err)
	}
	if _, err := service.EnsureBaseline(ctx, app.BootstrapInput{
		Env: domain.EnvDev, Namespace: "unknown", Actor: testActor,
	}); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("namespace without specs must be bad request, got %v", err)
	}
}

func TestBootstrapReconcilesMissingKeys(t *testing.T) {
	repos := memory.New()
	emitter := &fakeEmitter{}
	registry := spec.NewRegistry()
	if err := registry.Register(
		domain.KeySpec{
			Namespace: "platform", Key: "http.timeout", ValueType: domain.ValueTypeDuration,
			Default: "10s", Description: "HTTP 超时", Owner: "team-platform", Reloadable: true,
		},
	); err != nil {
		t.Fatalf("register initial: %v", err)
	}
	service, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, emitter, newIDs(),
		app.Options{Service: "admin-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new bootstrap: %v", err)
	}
	ctx := context.Background()
	input := app.BootstrapInput{Env: domain.EnvDev, Namespace: "platform", Actor: testActor}
	first, err := service.EnsureBaseline(ctx, input)
	if err != nil || !first.Seeded || first.ItemCount != 1 {
		t.Fatalf("first bootstrap: %v %+v", err, first)
	}

	if err := registry.Register(domain.KeySpec{
		Namespace: "platform", Key: "log.level", ValueType: domain.ValueTypeString,
		Default: "info", Description: "日志级别", Owner: "team-platform", Reloadable: true,
		Enum: []string{"debug", "info", "warn"},
	}); err != nil {
		t.Fatalf("register new key: %v", err)
	}
	second, err := service.EnsureBaseline(ctx, input)
	if err != nil || !second.Seeded || second.ItemCount != 1 {
		t.Fatalf("reconcile must seed one new key: %v %+v", err, second)
	}
	if second.ReleaseID == first.ReleaseID {
		t.Fatal("reconcile must publish a new release")
	}
	items := mustItems(t, repos, second.ReleaseID)
	if len(items) != 2 {
		t.Fatalf("reconciled snapshot size = %d, want 2", len(items))
	}
	old, err := repos.Releases.Get(ctx, first.ReleaseID)
	if err != nil || old.Status != domain.ReleaseStatusSuperseded {
		t.Fatalf("old release must be superseded: %v %+v", err, old)
	}
	third, err := service.EnsureBaseline(ctx, input)
	if err != nil || third.Seeded {
		t.Fatalf("stable reconcile must be no-op: %v %+v", err, third)
	}
}

func TestBootstrapRepairOverwritesExistingKey(t *testing.T) {
	repos := memory.New()
	emitter := &fakeEmitter{}
	registry := spec.NewRegistry()
	if err := registry.Register(domain.KeySpec{
		Namespace: "platform", Key: "storage.driver", ValueType: domain.ValueTypeString,
		Default: "s3", Description: "对象存储驱动", Owner: "team-platform", Reloadable: true,
		Enum: []string{"memory", "local", "s3"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	service, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, emitter, newIDs(),
		app.Options{Service: "admin-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new bootstrap: %v", err)
	}
	ctx := context.Background()
	first, err := service.EnsureBaseline(ctx, app.BootstrapInput{
		Env: domain.EnvProd, Namespace: "platform", Actor: testActor,
	})
	if err != nil || !first.Seeded {
		t.Fatalf("first seed: %v %+v", err, first)
	}
	item, err := repos.Items.GetByKey(ctx, domain.EnvProd, "platform", "storage.driver")
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	v1, err := repos.Items.GetVersion(ctx, item.ID, item.CurrentVersion)
	if err != nil || v1.ValueText != "s3" {
		t.Fatalf("seeded value: %v %+v", err, v1)
	}

	// 普通 reconcile 不得覆盖。
	noop, err := service.EnsureBaseline(ctx, app.BootstrapInput{
		Env: domain.EnvProd, Namespace: "platform", Actor: testActor,
		Overrides: map[string]string{"storage.driver": "local"},
	})
	if err != nil || noop.Seeded {
		t.Fatalf("non-repair must not overwrite: %v %+v", err, noop)
	}

	repaired, err := service.EnsureBaseline(ctx, app.BootstrapInput{
		Env: domain.EnvProd, Namespace: "platform", Actor: testActor,
		Overrides: map[string]string{"storage.driver": "local"}, Repair: true,
	})
	if err != nil || !repaired.Seeded || repaired.ItemCount != 1 {
		t.Fatalf("repair overwrite: %v %+v", err, repaired)
	}
	if repaired.ReleaseID == first.ReleaseID {
		t.Fatal("repair must publish a new release")
	}
	item, err = repos.Items.GetByKey(ctx, domain.EnvProd, "platform", "storage.driver")
	if err != nil {
		t.Fatalf("get item after repair: %v", err)
	}
	v2, err := repos.Items.GetVersion(ctx, item.ID, item.CurrentVersion)
	if err != nil || v2.ValueText != "local" || v2.ChangeSummary != "install-repair" {
		t.Fatalf("repaired version: %v %+v", err, v2)
	}
	old, err := repos.Releases.Get(ctx, first.ReleaseID)
	if err != nil || old.Status != domain.ReleaseStatusSuperseded {
		t.Fatalf("old release must be superseded: %v %+v", err, old)
	}
}

// mustItems 读取发布快照条目，用于校验摘要。
func mustItems(t *testing.T, repos *memory.Repositories, releaseID int64) []domain.ReleaseItem {
	t.Helper()
	items, err := repos.Releases.ListItems(context.Background(), releaseID)
	if err != nil {
		t.Fatalf("list release items: %v", err)
	}
	return items
}

// TestBootstrapSkipsEmptyDefaultKeys 守护「无默认值的键不会被自动播种」。
//
// storage.s3.bucket / region 靠这条语义把「未配置」暴露为安装缺口：
// 若它们被空值播种进快照，config-api 会判 ready 而 admin-api 仍在
// OpenStorageSet 失败降级，形成「安装完成 → 重启 → 再降级」的循环。
func TestBootstrapSkipsEmptyDefaultKeys(t *testing.T) {
	repos := memory.New()
	emitter := &fakeEmitter{}
	registry := spec.NewRegistry()
	if err := registry.Register(
		domain.KeySpec{
			Namespace: "platform", Key: "storage.driver", ValueType: domain.ValueTypeString,
			Default: "s3", Description: "存储驱动", Owner: "team-platform",
		},
		domain.KeySpec{
			Namespace: "platform", Key: "storage.s3.bucket", ValueType: domain.ValueTypeString,
			Description: "桶名（无默认值，须向导填写）", Owner: "team-platform",
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}
	service, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, emitter, newIDs(),
		app.Options{Service: "admin-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new bootstrap: %v", err)
	}
	result, err := service.EnsureBaseline(context.Background(), app.BootstrapInput{
		Env: domain.EnvDev, Namespace: "platform", Actor: testActor,
	})
	if err != nil {
		t.Fatalf("ensure baseline: %v", err)
	}
	if result.ItemCount != 1 {
		t.Fatalf("only the defaulted key may be seeded, got %+v", result)
	}
	for _, skipped := range result.SkippedKeys {
		if skipped == "storage.driver" {
			t.Fatal("defaulted key must not be skipped")
		}
	}
	items := mustItems(t, repos, result.ReleaseID)
	for _, item := range items {
		if item.ConfigKey == "storage.s3.bucket" {
			t.Fatal("key without default must not be seeded into the snapshot")
		}
	}
}

// TestMissingRequiredReportsStorageConditional 守护安装态 Gaps 与进程侧一致：
// driver=s3 且未填 bucket/region 时必须出现在 MissingRequired 中，否则
// begin(repair) 会报 no blocking keys，向导无法补齐。
func TestMissingRequiredReportsStorageConditional(t *testing.T) {
	repos := memory.New()
	emitter := &fakeEmitter{}
	registry := spec.NewRegistry()
	if err := registry.Register(
		domain.KeySpec{
			Namespace: spec.NamespacePlatform, Key: "storage.driver",
			ValueType: domain.ValueTypeString, Default: "s3",
			Description: "存储驱动", Owner: "team-platform",
		},
		domain.KeySpec{
			Namespace: spec.NamespacePlatform, Key: "storage.s3.bucket",
			ValueType:   domain.ValueTypeString,
			Description: "桶名", Owner: "team-platform",
		},
		domain.KeySpec{
			Namespace: spec.NamespacePlatform, Key: "storage.s3.region",
			ValueType:   domain.ValueTypeString,
			Description: "区域", Owner: "team-platform",
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}
	service, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, emitter, newIDs(),
		app.Options{Service: "admin-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new bootstrap: %v", err)
	}
	if _, err := service.EnsureBaseline(context.Background(), app.BootstrapInput{
		Env: domain.EnvProd, Namespace: spec.NamespacePlatform, Actor: testActor,
	}); err != nil {
		t.Fatalf("ensure baseline: %v", err)
	}

	gaps, err := service.MissingRequired(context.Background(), domain.EnvProd)
	if err != nil {
		t.Fatalf("MissingRequired: %v", err)
	}
	got := map[string]bool{}
	for _, gap := range gaps {
		got[gap.KeyRef()] = true
	}
	for _, want := range []string{
		"platform/storage.s3.bucket",
		"platform/storage.s3.region",
	} {
		if !got[want] {
			t.Fatalf("MissingRequired gaps=%v, want %s", gaps, want)
		}
	}
}
