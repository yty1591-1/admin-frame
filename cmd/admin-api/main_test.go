package main

import (
	"testing"

	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
)

func TestApplyLocalIdentityPIIKeysGuardedByEnv(t *testing.T) {
	t.Parallel()

	const (
		wantHash = "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00"
		wantSub  = "faceb00cfaceb00cfaceb00cfaceb00cfaceb00cfaceb00cfaceb00cfaceb00c"
	)

	t.Run("local fills missing keys", func(t *testing.T) {
		cfg := &identityconfig.IdentityConfig{Enabled: true}
		applyLocalIdentityPIIKeys("local", cfg)
		if cfg.HashKey != wantHash || cfg.HashKeyID != "v1" {
			t.Fatalf("hash = %q/%q", cfg.HashKey, cfg.HashKeyID)
		}
		if len(cfg.Subject.Keys) != 1 || cfg.Subject.Keys[0].Key != wantSub {
			t.Fatalf("subject keys = %+v", cfg.Subject.Keys)
		}
	})

	t.Run("prod skips fallback", func(t *testing.T) {
		cfg := &identityconfig.IdentityConfig{Enabled: true}
		applyLocalIdentityPIIKeys("prod", cfg)
		if cfg.HashKey != "" || len(cfg.Subject.Keys) != 0 {
			t.Fatalf("prod must not inject local keys: %+v", cfg)
		}
	})

	t.Run("staging skips fallback", func(t *testing.T) {
		cfg := &identityconfig.IdentityConfig{Enabled: true}
		applyLocalIdentityPIIKeys("staging", cfg)
		if cfg.HashKey != "" || len(cfg.Subject.Keys) != 0 {
			t.Fatalf("staging must not inject local keys: %+v", cfg)
		}
	})

	t.Run("empty env skips fallback", func(t *testing.T) {
		cfg := &identityconfig.IdentityConfig{Enabled: true}
		applyLocalIdentityPIIKeys("", cfg)
		if cfg.HashKey != "" {
			t.Fatalf("empty env must not inject: %q", cfg.HashKey)
		}
	})

	t.Run("does not overwrite existing", func(t *testing.T) {
		cfg := &identityconfig.IdentityConfig{
			Enabled:   true,
			HashKey:   "existing-hash-key-000000000000000000000000000000",
			HashKeyID: "keep",
			Subject: identityconfig.IdentitySubjectConfig{
				ActiveKeyID: "keep",
				Keys: []identityconfig.IdentityMFAKeyConfig{
					{ID: "keep", Key: "existing-subject-key-000000000000000000000000"},
				},
			},
		}
		applyLocalIdentityPIIKeys("dev", cfg)
		if cfg.HashKeyID != "keep" || cfg.Subject.ActiveKeyID != "keep" {
			t.Fatalf("overwrote existing keys: %+v", cfg)
		}
	})
}
