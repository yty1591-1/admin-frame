package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

func TestBootstrapProdLikeUsesOverlay(t *testing.T) {
	repos := memory.New()
	emitter := &fakeEmitter{}
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("platform specs: %v", err)
	}
	service, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, emitter, newIDs(),
		app.Options{Service: "config-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new bootstrap: %v", err)
	}
	ctx := context.Background()

	prod, err := service.EnsureBaseline(ctx, app.BootstrapInput{
		Env: domain.EnvProd, Namespace: spec.NamespacePlatform, Actor: testActor,
	})
	if err != nil {
		t.Fatalf("prod bootstrap: %v", err)
	}
	if !prod.Seeded {
		t.Fatalf("prod must seed: %+v", prod)
	}
	assertSeededValue(t, repos, domain.EnvProd, "storage.driver", "s3")
	assertSeededValue(t, repos, domain.EnvProd, "db.driver", "mysql")
	assertSeededValue(t, repos, domain.EnvProd, "auth.mode", "jwt")
	assertSeededValue(t, repos, domain.EnvProd, "idempotency.driver", "redis")
	assertSeededValue(t, repos, domain.EnvProd, "auth.jwt.issuer", "admin-frame")

	devRepos := memory.New()
	devService, err := app.NewBootstrapService(devRepos.Items, devRepos.Releases, devRepos.Pointers,
		registry, &fakeEmitter{}, newIDs(),
		app.Options{Service: "config-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new dev bootstrap: %v", err)
	}
	if _, err := devService.EnsureBaseline(ctx, app.BootstrapInput{
		Env: domain.EnvDev, Namespace: spec.NamespacePlatform, Actor: testActor,
	}); err != nil {
		t.Fatalf("dev bootstrap: %v", err)
	}
	assertSeededValue(t, devRepos, domain.EnvDev, "storage.driver", "memory")
	assertSeededValue(t, devRepos, domain.EnvDev, "db.driver", "memory")
}

func TestBootstrapProdLikeNotificationDrivers(t *testing.T) {
	repos := memory.New()
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("platform specs: %v", err)
	}
	service, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, &fakeEmitter{}, newIDs(),
		app.Options{Service: "config-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new bootstrap: %v", err)
	}
	if _, err := service.EnsureBaseline(context.Background(), app.BootstrapInput{
		Env: domain.EnvStaging, Namespace: spec.NamespaceNotification, Actor: testActor,
	}); err != nil {
		t.Fatalf("staging notification bootstrap: %v", err)
	}
	assertSeededValue(t, repos, domain.EnvStaging, "notification.channels.email.driver", "disabled")
	assertSeededValue(t, repos, domain.EnvStaging, "notification.channels.sms.driver", "disabled")
	assertSeededValue(t, repos, domain.EnvStaging, "notification.channels.push.driver", "disabled")
}

func assertSeededValue(t *testing.T, repos *memory.Repositories, env, key, want string) {
	t.Helper()
	item, err := repos.Items.GetByKey(context.Background(), env, itemNamespace(key), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	version, err := repos.Items.GetVersion(context.Background(), item.ID, item.CurrentVersion)
	if err != nil {
		t.Fatalf("version %s: %v", key, err)
	}
	if version.ValueText != want {
		t.Fatalf("%s = %q, want %q", key, version.ValueText, want)
	}
}

func itemNamespace(key string) string {
	switch {
	case len(key) >= 13 && key[:13] == "notification.":
		return spec.NamespaceNotification
	case len(key) >= 9 && key[:9] == "identity.":
		return spec.NamespaceIdentity
	default:
		return spec.NamespacePlatform
	}
}
