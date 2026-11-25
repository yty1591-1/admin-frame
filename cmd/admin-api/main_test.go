package main

import (
	"errors"
	"strings"
	"testing"

	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// TestStorageHealGapsPointAtConfigKeys 守护存储降级时上报的键是配置中心
// 可写的非敏感连接参数：写入不可回显的 AK/SK 只会泄漏键名而无助于排障。
func TestStorageHealGapsPointAtConfigKeys(t *testing.T) {
	gaps := storageHealGaps()
	if len(gaps) == 0 {
		t.Fatal("storage degrade must point operators at the keys to fix")
	}
	for _, gap := range gaps {
		if !strings.HasPrefix(gap, "platform/") {
			t.Fatalf("gap %q must be namespace/key", gap)
		}
		if strings.Contains(gap, "access_key") || strings.Contains(gap, "secret_key") {
			t.Fatalf("gap %q must not expose credential keys", gap)
		}
	}
}

// TestStorageUnavailableIsDistinguishable 守护降级触发条件足够窄：
// 只有 ErrStorageUnavailable 才进 setup-only；其他装配错误保持 fail-fast。
func TestStorageUnavailableIsDistinguishable(t *testing.T) {
	wrapped := errors.Join(platform.ErrStorageUnavailable, errors.New("s3 requires bucket and region"))
	if !errors.Is(wrapped, platform.ErrStorageUnavailable) {
		t.Fatal("storage failure must match ErrStorageUnavailable after wrapping")
	}
	if errors.Is(errors.New("db: connection refused"), platform.ErrStorageUnavailable) {
		t.Fatal("unrelated boot errors must not match ErrStorageUnavailable")
	}
}

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
