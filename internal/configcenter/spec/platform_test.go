package spec_test

import (
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

func TestPlatformCatalog(t *testing.T) {
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("platform registry: %v", err)
	}
	wantNamespaces := []string{
		spec.NamespaceAudit, spec.NamespaceIAM, spec.NamespaceIdentity,
		spec.NamespaceNotification, spec.NamespacePlatform, spec.NamespaceTenant,
	}
	gotNamespaces := registry.Namespaces()
	if len(gotNamespaces) != len(wantNamespaces) {
		t.Fatalf("unexpected namespaces: %v", gotNamespaces)
	}
	for i, want := range wantNamespaces {
		if gotNamespaces[i] != want {
			t.Fatalf("namespace[%d] = %q, want %q", i, gotNamespaces[i], want)
		}
	}

	platformKeys := registry.List(spec.NamespacePlatform)
	if len(platformKeys) < 50 {
		t.Fatalf("platform catalog too small: %d keys", len(platformKeys))
	}
	for _, keySpec := range registry.List(spec.NamespacePlatform) {
		assertKeySpec(t, keySpec)
	}

	iamKeys := registry.List(spec.NamespaceIAM)
	if len(iamKeys) < 15 {
		t.Fatalf("iam catalog too small: %d keys", len(iamKeys))
	}
	mode, ok := findKey(iamKeys, "iam.mode")
	if !ok {
		t.Fatal("iam.mode must be declared")
	}
	if mode.Default != "" || mode.Required {
		t.Fatalf("iam.mode must stay undeclared by default: %+v", mode)
	}

	minimum := map[string]int{
		spec.NamespaceIdentity:     50,
		spec.NamespaceAudit:        10,
		spec.NamespaceTenant:       4,
		spec.NamespaceNotification: 5,
	}
	for namespace, want := range minimum {
		keys := registry.List(namespace)
		if len(keys) < want {
			t.Fatalf("%s catalog too small: %d keys, want >= %d", namespace, len(keys), want)
		}
		for _, keySpec := range keys {
			assertKeySpec(t, keySpec)
		}
	}
}

// assertKeySpec 校验单个声明可注册且默认值合法。
func assertKeySpec(t *testing.T, keySpec domain.KeySpec) {
	t.Helper()
	if err := keySpec.Validate(); err != nil {
		t.Fatalf("invalid key spec: %v", err)
	}
	if keySpec.Sensitive {
		t.Fatalf("sensitive key %s must not enter M1 catalog", keySpec.Key)
	}
	if keySpec.Default != "" {
		if err := keySpec.ValidateValue(keySpec.Default); err != nil {
			t.Fatalf("default of %s is invalid: %v", keySpec.Key, err)
		}
	}
}

// findKey 在声明列表中按 key 查找。
func findKey(keys []domain.KeySpec, key string) (domain.KeySpec, bool) {
	for _, keySpec := range keys {
		if keySpec.Key == key {
			return keySpec, true
		}
	}
	return domain.KeySpec{}, false
}

func TestPlatformCatalogExcludesSecrets(t *testing.T) {
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("platform registry: %v", err)
	}
	forbidden := []string{"password", "secret", "private_key", "credential", "token", "dsn"}
	for _, keySpec := range registry.List(spec.NamespacePlatform) {
		for _, needle := range forbidden {
			if strings.Contains(keySpec.Key, needle) {
				t.Fatalf("key %s must not be declared in the config center", keySpec.Key)
			}
		}
		if strings.HasPrefix(keySpec.Key, "secrets.") || strings.HasPrefix(keySpec.Key, "auth.jwt.keys") {
			t.Fatalf("key %s must stay outside the config center", keySpec.Key)
		}
	}
}

func TestPlatformDefaultsExcludeRequiredKeys(t *testing.T) {
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("platform registry: %v", err)
	}
	defaults := registry.Defaults(spec.NamespacePlatform)
	if _, ok := defaults["auth.jwt.issuer"]; ok {
		t.Fatal("required key auth.jwt.issuer must not be seeded")
	}
	if defaults["server.addr"] != ":8080" || defaults["log.level"] != "info" {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}

	keys := map[string]domain.KeySpec{}
	for _, keySpec := range registry.List(spec.NamespacePlatform) {
		keys[keySpec.Key] = keySpec
	}
	reloadable := 0
	for _, keySpec := range keys {
		if keySpec.Reloadable {
			reloadable++
		}
	}
	if reloadable == 0 {
		t.Fatal("catalog must mark platform hot keys as reloadable")
	}
}
