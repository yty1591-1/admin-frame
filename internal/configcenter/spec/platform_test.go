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
	if got := registry.Namespaces(); len(got) != 2 ||
		got[0] != spec.NamespaceIAM || got[1] != spec.NamespacePlatform {
		t.Fatalf("unexpected namespaces: %v", got)
	}

	platformKeys := registry.List(spec.NamespacePlatform)
	if len(platformKeys) < 50 {
		t.Fatalf("platform catalog too small: %d keys", len(platformKeys))
	}
	for _, keySpec := range platformKeys {
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

	iamKeys := registry.List(spec.NamespaceIAM)
	if len(iamKeys) != 1 || iamKeys[0].Key != "iam.mode" {
		t.Fatalf("unexpected iam keys: %+v", iamKeys)
	}
	if iamKeys[0].Default != "" || iamKeys[0].Required {
		t.Fatalf("iam.mode must stay undeclared by default: %+v", iamKeys[0])
	}
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
