package spec

import (
	"sort"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// TestEnvDefaultOverlayIntegrity 校验叠加表层自身：键已登记、值合法、无敏感/禁写键。
func TestEnvDefaultOverlayIntegrity(t *testing.T) {
	registry, err := Platform()
	if err != nil {
		t.Fatalf("Platform: %v", err)
	}
	byKey := indexSpecsByKey(registry)

	for name, overlay := range map[string]map[string]string{
		"prodLikeDefaults": prodLikeDefaults,
		"nonProdDefaults":  nonProdDefaults,
	} {
		if len(overlay) == 0 {
			t.Fatalf("%s must not be empty", name)
		}
		keys := sortedKeys(overlay)
		for _, key := range keys {
			value := overlay[key]
			if strings.TrimSpace(value) == "" {
				t.Errorf("%s[%q] must not be empty string", name, key)
				continue
			}
			keySpec, ok := byKey[key]
			if !ok {
				t.Errorf("%s[%q] is not registered in Platform()", name, key)
				continue
			}
			if keySpec.Sensitive {
				t.Errorf("%s[%q] is sensitive; overlays must not carry plaintext", name, key)
				continue
			}
			if reason, forbidden := overlayForbiddenKeys[key]; forbidden {
				t.Errorf("%s[%q] is channel/process exclusive (%s)", name, key, reason)
				continue
			}
			if err := keySpec.ValidateValue(value); err != nil {
				t.Errorf("%s[%q]=%q fails ValidateValue: %v", name, key, value, err)
			}
		}
	}
}

// TestEnvDefaultCoverage 自审查：每个非敏感键在各已知环境下要么有推荐值，要么在白名单。
func TestEnvDefaultCoverage(t *testing.T) {
	registry, err := Platform()
	if err != nil {
		t.Fatalf("Platform: %v", err)
	}

	envs := []string{
		domain.EnvLocal, domain.EnvDev, domain.EnvTest,
		domain.EnvStaging, domain.EnvProd,
	}

	var missing []string
	var unexpectedEmpty []string
	allowlistUnused := copyStringMap(emptyRecommendReasons)

	for _, ns := range registry.Namespaces() {
		for _, keySpec := range registry.List(ns) {
			if reason, ok := emptyRecommendReasons[keySpec.Key]; ok {
				delete(allowlistUnused, keySpec.Key)
				if keySpec.Sensitive {
					t.Errorf("emptyRecommendReasons[%q] marks a sensitive key (%s)", keySpec.Key, reason)
				}
				if keySpec.Default != "" {
					t.Errorf("emptyRecommendReasons[%q] has KeySpec.Default=%q; remove from allowlist or clear Default",
						keySpec.Key, keySpec.Default)
				}
			}

			for _, env := range envs {
				value, source := ResolveDefault(env, keySpec)

				if keySpec.Sensitive {
					if value != "" || source != DefaultSourceNone {
						t.Errorf("%s %s/%s: sensitive must resolve none, got %q/%q",
							env, keySpec.Namespace, keySpec.Key, value, source)
					}
					continue
				}

				if keySpec.Required && value == "" {
					missing = append(missing, env+":"+keySpec.Key+":required")
					continue
				}

				if value != "" {
					if err := keySpec.ValidateValue(value); err != nil {
						t.Errorf("%s %s: resolved %q (%s) invalid: %v",
							env, keySpec.Key, value, source, err)
					}
					continue
				}

				// value == "" → 必须在故意留空白名单。
				if _, ok := emptyRecommendReasons[keySpec.Key]; !ok {
					unexpectedEmpty = append(unexpectedEmpty, env+":"+keySpec.Key)
				}
			}
		}
	}

	if len(allowlistUnused) > 0 {
		t.Errorf("emptyRecommendReasons has unknown keys (not in Platform): %v", sortedKeys(allowlistUnused))
	}
	if len(missing) > 0 {
		t.Errorf("Required keys missing recommended defaults:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(unexpectedEmpty) > 0 {
		sort.Strings(unexpectedEmpty)
		t.Errorf("non-sensitive keys resolve empty without allowlist entry:\n  %s",
			strings.Join(unexpectedEmpty, "\n  "))
	}
}

// TestEnvDefaultProdDiffersFromLocalFriendly 守护 §4.4：生产叠加必须覆盖本地危险默认。
func TestEnvDefaultProdDiffersFromLocalFriendly(t *testing.T) {
	must := map[string]string{
		"db.driver":                          "mysql",
		"storage.driver":                     "s3",
		"async.enabled":                      "true",
		"auth.mode":                          "jwt",
		"idempotency.enabled":                "true",
		"idempotency.required":               "true",
		"idempotency.driver":                 "redis",
		"identity.mfa.admin_required":        "true",
		"notification.channels.email.driver": "disabled",
		"notification.channels.sms.driver":   "disabled",
		"notification.channels.push.driver":  "disabled",
	}
	for key, want := range must {
		if got := prodLikeDefaults[key]; got != want {
			t.Errorf("prodLikeDefaults[%q]=%q, want %q", key, got, want)
		}
		if got := nonProdDefaults[key]; got == want {
			t.Errorf("nonProdDefaults[%q] must not equal prod value %q", key, want)
		}
	}
	if nonProdDefaults["auth.mode"] != "demo" {
		t.Fatalf("nonProdDefaults auth.mode=%q, want demo", nonProdDefaults["auth.mode"])
	}
	if nonProdDefaults["db.driver"] != "memory" {
		t.Fatalf("nonProdDefaults db.driver=%q, want memory", nonProdDefaults["db.driver"])
	}
	if nonProdDefaults["storage.driver"] != "memory" {
		t.Fatalf("nonProdDefaults storage.driver=%q, want memory", nonProdDefaults["storage.driver"])
	}
}

func indexSpecsByKey(registry *Registry) map[string]domain.KeySpec {
	out := make(map[string]domain.KeySpec)
	for _, ns := range registry.Namespaces() {
		for _, keySpec := range registry.List(ns) {
			out[keySpec.Key] = keySpec
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
