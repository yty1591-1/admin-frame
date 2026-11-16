package secretruntime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

func TestApplyJWTSecretFillsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"items": []map[string]any{{
					"name": "auth.jwt.private_key", "version": 1,
					"value":            "0123456789abcdef0123456789abcdef",
					"lease_expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano),
				}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	client, err := secretclient.New(secretclient.Config{
		BaseURL: srv.URL, ServiceIdentity: "spiffe://local/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &platformconfig.Config{Env: "dev"}
	if err := secretruntime.ApplyJWTSecret(context.Background(), cfg, client, nil); err != nil {
		t.Fatalf("ApplyJWTSecret: %v", err)
	}
	if cfg.Auth.JWT.Secret != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("secret = %q", cfg.Auth.JWT.Secret)
	}
}

func TestApplyJWTSecretOverwritesExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"items": []map[string]any{{
					"name": "auth.jwt.private_key", "version": 1,
					"value":            "from-config-api-secret-value-32ch",
					"lease_expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano),
				}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	client, err := secretclient.New(secretclient.Config{
		BaseURL: srv.URL, ServiceIdentity: "spiffe://local/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &platformconfig.Config{Env: "dev"}
	cfg.Auth.JWT.Secret = "already-set-secret-value-32chars!!"
	if err := secretruntime.ApplyJWTSecret(context.Background(), cfg, client, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.JWT.Secret != "from-config-api-secret-value-32ch" {
		t.Fatalf("secret = %q, want config-api value", cfg.Auth.JWT.Secret)
	}
}

func TestApplyJWTSecretRequiresClient(t *testing.T) {
	cfg := &platformconfig.Config{Env: "dev"}
	err := secretruntime.ApplyJWTSecret(context.Background(), cfg, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "secretclient is required") {
		t.Fatalf("err=%v", err)
	}
}

func TestNewFromEnvRequiresBoth(t *testing.T) {
	t.Setenv("CONFIG_API_URL", "")
	t.Setenv("CONFIG_SERVICE_IDENTITY", "")
	if _, err := secretruntime.NewFromEnv(); err == nil {
		t.Fatal("expected error when CONFIG_API_* unset")
	}
	t.Setenv("CONFIG_API_URL", "http://127.0.0.1:8085")
	if _, err := secretruntime.NewFromEnv(); err == nil {
		t.Fatal("expected error when identity unset")
	}
}
