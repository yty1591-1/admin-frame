package secretruntime_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
)

func TestOAuthClientSecretName(t *testing.T) {
	got := secretruntime.OAuthClientSecretName("GitHub")
	if got != "identity.oauth.github.client_secret" {
		t.Fatalf("name = %q", got)
	}
	got = secretruntime.OAuthClientSecretName("Acme Corp!")
	if !strings.HasPrefix(got, "identity.oauth.") || !strings.HasSuffix(got, ".client_secret") {
		t.Fatalf("sanitized name = %q", got)
	}
}

func TestResolveOAuthClientSecretOrEnvLocalFallback(t *testing.T) {
	const envVar = "ADMIN_FRAME_TEST_OAUTH_CLIENT_SECRET"
	t.Setenv(envVar, "oauth-local-secret-value")
	t.Cleanup(func() { _ = os.Unsetenv(envVar) })

	value, err := secretruntime.ResolveOAuthClientSecretOrEnv(
		context.Background(), "local", "demo", envVar)
	if err != nil {
		t.Fatalf("local fallback: %v", err)
	}
	if value != "oauth-local-secret-value" {
		t.Fatalf("value = %q", value)
	}
}

func TestResolveOAuthClientSecretOrEnvProdRejectsBareEnv(t *testing.T) {
	const envVar = "ADMIN_FRAME_TEST_OAUTH_CLIENT_SECRET_PROD"
	t.Setenv(envVar, "should-not-use")
	_, err := secretruntime.ResolveOAuthClientSecretOrEnv(
		context.Background(), "prod", "demo", envVar)
	if err == nil {
		t.Fatal("prod must not fall back to bare env when secretclient missing")
	}
}
