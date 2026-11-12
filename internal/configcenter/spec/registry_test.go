package spec_test

import (
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

func durationSpec() domain.KeySpec {
	return domain.KeySpec{
		Namespace: "platform", Key: "http.timeout", ValueType: domain.ValueTypeDuration,
		Default: "10s", Description: "HTTP 超时", Owner: "team-platform", Reloadable: true,
	}
}

func intSpec() domain.KeySpec {
	minValue, maxValue := int64(1), int64(10)
	return domain.KeySpec{
		Namespace: "platform", Key: "http.retries", ValueType: domain.ValueTypeInt,
		Default: "3", Description: "重试次数", Owner: "team-platform",
		MinInt: &minValue, MaxInt: &maxValue,
	}
}

func requiredSpec() domain.KeySpec {
	return domain.KeySpec{
		Namespace: "platform", Key: "http.base_url", ValueType: domain.ValueTypeString,
		Description: "上游地址", Owner: "team-platform", Required: true,
	}
}

func sensitiveSpec() domain.KeySpec {
	return domain.KeySpec{
		Namespace: "platform", Key: "smtp.password", ValueType: domain.ValueTypeString,
		Description: "SMTP 口令", Owner: "team-platform", Sensitive: true,
	}
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	registry := spec.NewRegistry()
	if err := registry.Register(durationSpec(), intSpec()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, ok := registry.Lookup("platform", "http.timeout"); !ok {
		t.Fatal("registered spec must be found")
	}
	if _, ok := registry.Lookup("identity", "http.timeout"); ok {
		t.Fatal("lookup must be namespace scoped")
	}
	if _, ok := registry.Lookup("platform", "http.missing"); ok {
		t.Fatal("unknown key must not be found")
	}
}

func TestRegistryRejectsInvalidAndDuplicate(t *testing.T) {
	registry := spec.NewRegistry()
	if err := registry.Register(domain.KeySpec{Namespace: "platform", Key: "bad"}); err == nil {
		t.Fatal("invalid spec must be rejected")
	}
	if err := registry.Register(durationSpec()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register(durationSpec()); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate spec must be rejected, got %v", err)
	}
	if err := registry.Register(durationSpec(), durationSpec()); err == nil {
		t.Fatal("duplicate within one batch must be rejected")
	}
}

func TestRegistryListAndDefaults(t *testing.T) {
	registry := spec.NewRegistry()
	registry.MustRegister(durationSpec(), intSpec(), requiredSpec(), sensitiveSpec())

	listed := registry.List("platform")
	if len(listed) != 4 {
		t.Fatalf("expected 4 specs, got %d", len(listed))
	}
	if listed[0].Key != "http.base_url" || listed[1].Key != "http.retries" {
		t.Fatalf("list must be sorted by key: %+v", listed)
	}

	defaults := registry.Defaults("platform")
	if len(defaults) != 2 {
		t.Fatalf("only non-empty defaults expected, got %+v", defaults)
	}
	if defaults["http.timeout"] != "10s" || defaults["http.retries"] != "3" {
		t.Fatalf("defaults mismatch: %+v", defaults)
	}
	if _, ok := defaults["http.base_url"]; ok {
		t.Fatal("required key must not have a default")
	}
	if _, ok := defaults["smtp.password"]; ok {
		t.Fatal("sensitive key must not expose a default")
	}
}

func TestRegistryValidateValues(t *testing.T) {
	registry := spec.NewRegistry()
	registry.MustRegister(durationSpec(), intSpec(), requiredSpec())

	valid := map[string]string{
		"http.timeout":  "30s",
		"http.retries":  "5",
		"http.base_url": "https://example.com",
	}
	if err := registry.ValidateValues("platform", valid); err != nil {
		t.Fatalf("valid values rejected: %v", err)
	}

	unknown := map[string]string{"http.unknown": "1"}
	if err := registry.ValidateValues("platform", unknown); err == nil ||
		!strings.Contains(err.Error(), "not declared") {
		t.Fatalf("unknown key must be rejected, got %v", err)
	}

	missingRequired := map[string]string{"http.timeout": "10s"}
	if err := registry.ValidateValues("platform", missingRequired); err == nil ||
		!strings.Contains(err.Error(), "required key") {
		t.Fatalf("missing required key must be rejected, got %v", err)
	}

	invalid := map[string]string{"http.retries": "99", "http.base_url": "https://example.com"}
	if err := registry.ValidateValues("platform", invalid); err == nil ||
		!strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("invalid value must be rejected, got %v", err)
	}
}
