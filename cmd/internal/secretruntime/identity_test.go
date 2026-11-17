package secretruntime

import (
	"context"
	"testing"

	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
)

func TestApplyIdentitySubjectKeyFillsEmptyRing(t *testing.T) {
	cfg := &identityconfig.IdentityConfig{}
	ApplyIdentitySubjectKey(cfg, "  aabbccdd  ")
	if cfg.Subject.ActiveKeyID != "v1" || len(cfg.Subject.Keys) != 1 {
		t.Fatalf("subject = %+v", cfg.Subject)
	}
	if cfg.Subject.Keys[0].Key != "aabbccdd" {
		t.Fatalf("key = %q", cfg.Subject.Keys[0].Key)
	}
	ApplyIdentitySubjectKey(cfg, "other")
	if cfg.Subject.Keys[0].Key != "aabbccdd" {
		t.Fatal("must not overwrite existing subject keys")
	}
}

func TestResolveIdentitySecretRequiresClient(t *testing.T) {
	t.Parallel()
	_, err := ResolveIdentityHashKey(context.Background(), "dev", nil)
	if err == nil {
		t.Fatal("nil client must fail")
	}
	_, err = ResolveIdentityMFASecretKey(context.Background(), "dev", nil)
	if err == nil {
		t.Fatal("nil client must fail for mfa")
	}
	_, err = ResolveIdentitySAMLPrivateKey(context.Background(), "dev", nil)
	if err == nil {
		t.Fatal("nil client must fail for saml")
	}
	_, err = ResolveIdentitySubjectKey(context.Background(), "dev", nil)
	if err == nil {
		t.Fatal("nil client must fail for subject key")
	}
}
