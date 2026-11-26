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

// TestBootstrapSeedsAllDeclaredNamespaces 验证空库首次启动为全部有默认值的
// namespace 生成 baseline（AC-CC-12），且业务域默认值与 spec 一致。
func TestBootstrapSeedsAllDeclaredNamespaces(t *testing.T) {
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("platform registry: %v", err)
	}
	repos := memory.New()
	service, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, &fakeEmitter{}, newIDs(),
		app.Options{Service: "admin-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new bootstrap service: %v", err)
	}
	ctx := context.Background()

	// 所有已声明 namespace 都有默认值可播种；iam.mode 本身无默认值被跳过。
	minimum := map[string]int{
		spec.NamespacePlatform:     50,
		spec.NamespaceIAM:          15,
		spec.NamespaceIdentity:     50,
		spec.NamespaceAudit:        10,
		spec.NamespaceTenant:       4,
		spec.NamespaceNotification: 4,
	}
	for _, namespace := range registry.Namespaces() {
		result, ensureErr := service.EnsureBaseline(ctx, app.BootstrapInput{
			Env: domain.EnvDev, Namespace: namespace, Actor: testActor,
		})
		if ensureErr != nil {
			t.Fatalf("bootstrap %s: %v", namespace, ensureErr)
		}
		want, ok := minimum[namespace]
		if !ok {
			t.Fatalf("namespace %s has no expected minimum; update this test", namespace)
		}
		if !result.Seeded || result.ItemCount < want {
			t.Fatalf("namespace %s bootstrap = %+v, want >= %d items", namespace, result, want)
		}
		pointer, pointerErr := repos.Pointers.Get(ctx, domain.EnvDev, namespace)
		if pointerErr != nil || pointer.ActiveReleaseID != result.ReleaseID {
			t.Fatalf("namespace %s pointer mismatch: %v %+v", namespace, pointerErr, pointer)
		}
	}

	// 二次启动幂等：不产生新 Release。
	again, err := service.EnsureBaseline(ctx, app.BootstrapInput{
		Env: domain.EnvDev, Namespace: spec.NamespaceIdentity, Actor: testActor,
	})
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if again.Seeded {
		t.Fatalf("second bootstrap must be a no-op: %+v", again)
	}
}
