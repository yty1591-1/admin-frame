package spec_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

func TestResolveDefaultProdLikeOverlay(t *testing.T) {
	key := domain.KeySpec{
		Namespace: spec.NamespacePlatform,
		Key:       "db.driver",
		ValueType: domain.ValueTypeString,
		Default:   "memory",
	}
	value, source := spec.ResolveDefault(domain.EnvProd, key)
	if value != "mysql" || source != spec.DefaultSourceProd {
		t.Fatalf("prod db.driver=%q/%q", value, source)
	}
	value, source = spec.ResolveDefault(domain.EnvStaging, key)
	if value != "mysql" || source != spec.DefaultSourceProd {
		t.Fatalf("staging db.driver=%q/%q", value, source)
	}
}

func TestResolveDefaultNonProdOverlay(t *testing.T) {
	authMode := domain.KeySpec{
		Namespace: spec.NamespacePlatform,
		Key:       "auth.mode",
		ValueType: domain.ValueTypeString,
		Default:   "",
	}
	value, source := spec.ResolveDefault(domain.EnvDev, authMode)
	if value != "demo" || source != spec.DefaultSourceDev {
		t.Fatalf("dev auth.mode=%q/%q", value, source)
	}

	logLevel := domain.KeySpec{
		Namespace: spec.NamespacePlatform,
		Key:       "log.level",
		ValueType: domain.ValueTypeString,
		Default:   "info",
	}
	value, source = spec.ResolveDefault(domain.EnvLocal, logLevel)
	if value != "debug" || source != spec.DefaultSourceDev {
		t.Fatalf("local log.level=%q/%q", value, source)
	}
	value, source = spec.ResolveDefault(domain.EnvProd, logLevel)
	if value != "info" || source != spec.DefaultSourceCode {
		t.Fatalf("prod log.level should fall back to code default, got %q/%q", value, source)
	}
}

func TestResolveDefaultFallsBackToCodeDefault(t *testing.T) {
	key := domain.KeySpec{
		Namespace: spec.NamespacePlatform,
		Key:       "server.addr",
		ValueType: domain.ValueTypeString,
		Default:   ":8080",
	}
	value, source := spec.ResolveDefault(domain.EnvDev, key)
	if value != ":8080" || source != spec.DefaultSourceCode {
		t.Fatalf("dev server.addr=%q/%q", value, source)
	}
}

func TestResolveDefaultSkipsSensitive(t *testing.T) {
	key := domain.KeySpec{
		Namespace: spec.NamespacePlatform,
		Key:       "redis.password",
		ValueType: domain.ValueTypeString,
		Sensitive: true,
		Default:   "must-not-leak",
	}
	value, source := spec.ResolveDefault(domain.EnvProd, key)
	if value != "" || source != spec.DefaultSourceNone {
		t.Fatalf("sensitive default leaked: %q/%q", value, source)
	}
}

func TestResolveDefaultRequiredKeyFromOverlay(t *testing.T) {
	key := domain.KeySpec{
		Namespace: spec.NamespacePlatform,
		Key:       "auth.jwt.issuer",
		ValueType: domain.ValueTypeString,
		Required:  true,
	}
	value, source := spec.ResolveDefault(domain.EnvProd, key)
	if value != "admin-frame" || source != spec.DefaultSourceProd {
		t.Fatalf("prod issuer=%q/%q", value, source)
	}
	value, source = spec.ResolveDefault(domain.EnvDev, key)
	if value != "admin-frame" || source != spec.DefaultSourceDev {
		t.Fatalf("dev issuer=%q/%q", value, source)
	}
}

func TestEnvDefaultsCopies(t *testing.T) {
	prod := spec.EnvDefaults(domain.EnvProd)
	prod["db.driver"] = "tampered"
	again := spec.EnvDefaults(domain.EnvProd)
	if again["db.driver"] != "mysql" {
		t.Fatalf("EnvDefaults must return a copy, got %q", again["db.driver"])
	}
	if len(spec.EnvDefaults("unknown")) != 0 {
		t.Fatal("unknown env must yield empty overlay")
	}
}
