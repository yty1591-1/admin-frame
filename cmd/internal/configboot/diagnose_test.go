package configboot

import (
	"fmt"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// TestFillPlaceholdersDeclaredKeys 验证占位填充只碰「已声明、非敏感、为空」的键。
func TestFillPlaceholdersDeclaredKeys(t *testing.T) {
	values := map[string]string{"auth.jwt.issuer": "admin-frame"}
	filled, refs, err := FillPlaceholders(values)
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("expected at least one declared empty key")
	}
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	for _, ref := range refs {
		namespace, key, ok := strings.Cut(ref, "/")
		if !ok || namespace == "" || key == "" {
			t.Fatalf("bad ref %q", ref)
		}
		keySpec, ok := registry.Lookup(namespace, key)
		if !ok {
			t.Fatalf("filled undeclared key %q", ref)
		}
		if keySpec.Sensitive {
			t.Fatalf("filled sensitive key %q", ref)
		}
		if strings.TrimSpace(filled[key]) == "" {
			t.Fatalf("placeholder for %q is empty", ref)
		}
	}
	// 调用方传入的 map 不被就地修改（装配闭包会复用同一份 values）。
	if len(values) != 1 || values["auth.jwt.issuer"] != "admin-frame" {
		t.Fatalf("input map mutated: %+v", values)
	}
	if filled["auth.jwt.issuer"] != "admin-frame" {
		t.Fatalf("existing value overwritten: %q", filled["auth.jwt.issuer"])
	}
}

// TestDiagnoseMissingValues 验证「缺值」与「值写错」的区分（ADR-0018 决策 3）。
func TestDiagnoseMissingValues(t *testing.T) {
	required := func(values map[string]string) error {
		for _, key := range []string{"auth.jwt.issuer", "auth.jwt.audience"} {
			if strings.TrimSpace(values[key]) == "" {
				return fmt.Errorf("%s is required", key)
			}
		}
		return nil
	}

	t.Run("missing values are diagnosed", func(t *testing.T) {
		gaps, ok := DiagnoseMissingValues(map[string]string{"auth.jwt.issuer": ""}, required)
		if !ok {
			t.Fatal("expected missing-value diagnosis")
		}
		if !contains(gaps, "platform/auth.jwt.audience") {
			t.Fatalf("expected audience gap, got %+v", gaps)
		}
	})

	t.Run("invalid value blocks degrade", func(t *testing.T) {
		validate := func(values map[string]string) error {
			if values["db.driver"] == "bogus" {
				return fmt.Errorf("db.driver must be memory or mysql")
			}
			return required(values)
		}
		values := map[string]string{"auth.jwt.issuer": "", "db.driver": "bogus"}
		if gaps, ok := DiagnoseMissingValues(values, validate); ok {
			t.Fatalf("invalid value must not degrade, got gaps %+v", gaps)
		}
	})

	t.Run("nil validate never degrades", func(t *testing.T) {
		if _, ok := DiagnoseMissingValues(map[string]string{"auth.jwt.issuer": ""}, nil); ok {
			t.Fatal("nil validate must not degrade")
		}
	})
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
