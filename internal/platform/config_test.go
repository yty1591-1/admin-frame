package platform_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
)

func TestValidateRejectsProdDemoMode(t *testing.T) {
	cfg := &platform.Config{
		Env:     "prod",
		Service: "c-api",
		Channel: "c",
		Addr:    ":8083",
		Auth:    platform.AuthConfig{DemoMode: true, ExpectedChannel: "c"},
		DB:      platform.DBConfig{Driver: "memory"},
		Log:     platform.LogConfig{Level: "info"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for prod+demo_mode")
	}
}

func TestValidateChannelMismatch(t *testing.T) {
	cfg := &platform.Config{
		Service: "c-api",
		Channel: "c",
		Addr:    ":8083",
		Auth:    platform.AuthConfig{ExpectedChannel: "admin"},
		DB:      platform.DBConfig{Driver: "memory"},
		Log:     platform.LogConfig{Level: "info"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected channel mismatch error")
	}
}

func TestLoadConfigEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	content := []byte(`
service: c-api
channel: c
addr: ":8083"
env: local
auth:
  demo_mode: true
  expected_channel: c
db:
  driver: memory
  dsn: ""
log:
  level: info
  format: json
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ADMIN_FRAME_ADDR", ":18083")
	t.Setenv("ADMIN_FRAME_LOG_LEVEL", "warn")
	t.Setenv("ADMIN_FRAME_SHUTDOWN_TIMEOUT", "30s")

	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":18083" {
		t.Fatalf("addr=%s", cfg.Addr)
	}
	if cfg.Log.Level != "warn" {
		t.Fatalf("level=%s", cfg.Log.Level)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Fatalf("timeout=%s", cfg.ShutdownTimeout)
	}
}

func TestLoadDBConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "migrate.yaml")
	content := []byte("db:\n  driver: mysql\n  dsn: \"file-dsn\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADMIN_FRAME_DB_DRIVER", "postgres")
	t.Setenv("ADMIN_FRAME_DB_DSN", "env-dsn")

	got, err := platform.LoadDBConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Driver != "postgres" || got.DSN != "env-dsn" {
		t.Fatalf("got=%+v", got)
	}
}

func TestLoadDBConfigEmptyPath(t *testing.T) {
	t.Setenv("ADMIN_FRAME_DB_DRIVER", "mysql")
	t.Setenv("ADMIN_FRAME_DB_DSN", "env-only")
	got, err := platform.LoadDBConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Driver != "mysql" || got.DSN != "env-only" {
		t.Fatalf("got=%+v", got)
	}
}

func TestLoadConfigProdDemoModeFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	content := []byte(`
service: c-api
channel: c
addr: ":8083"
auth:
  demo_mode: true
  expected_channel: c
db:
  driver: memory
log:
  level: info
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADMIN_FRAME_ENV", "prod")
	t.Setenv("ADMIN_FRAME_AUTH_DEMO_MODE", "true")
	if _, err := platform.LoadConfig(path); err == nil {
		t.Fatal("expected validate failure")
	}
}
