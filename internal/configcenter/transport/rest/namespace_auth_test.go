package rest

import (
	"strings"
	"testing"
)

func TestAuthorizeNamespace(t *testing.T) {
	t.Setenv(namespaceAllowlistEnv, "admin-api:platform,identity;biz-api:platform;*:platform")
	resetNamespaceAllowlistForTest()

	if err := authorizeNamespace("prod", "admin-api", "identity"); err != nil {
		t.Fatalf("admin-api identity: %v", err)
	}
	if err := authorizeNamespace("prod", "spiffe://cluster/ns/biz-api", "platform"); err != nil {
		t.Fatalf("biz-api leaf: %v", err)
	}
	if err := authorizeNamespace("prod", "c-api", "platform"); err != nil {
		t.Fatalf("wildcard service platform: %v", err)
	}
	if err := authorizeNamespace("prod", "c-api", "identity"); err == nil {
		t.Fatal("c-api must not read identity without grant")
	}
	if err := authorizeNamespace("prod", "admin-api", "iam"); err == nil {
		t.Fatal("admin-api must not read iam")
	}
}

func TestAuthorizeNamespaceProdRequiresAllowlist(t *testing.T) {
	t.Setenv(namespaceAllowlistEnv, "")
	resetNamespaceAllowlistForTest()
	err := authorizeNamespace("prod", "admin-api", "platform")
	if err == nil || !strings.Contains(err.Error(), namespaceAllowlistEnv) {
		t.Fatalf("err=%v want allowlist required", err)
	}
	if err := authorizeNamespace("", "admin-api", "platform"); err == nil {
		t.Fatal("empty deploy env must require allowlist")
	}
	if err := authorizeNamespace("local", "admin-api", "platform"); err != nil {
		t.Fatalf("local without allowlist ok: %v", err)
	}
}
