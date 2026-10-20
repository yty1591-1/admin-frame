package platform_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/config"
)

func TestValidateRejectsProdAuthModeDemo(t *testing.T) {
	cfg := &platform.Config{
		Env:     "prod",
		Service: "c-api",
		Channel: "c",
		Addr:    ":8083",
		Auth:    platform.AuthConfig{Mode: platform.AuthModeDemo, ExpectedChannel: "c"},
		DB:      platform.DBConfig{Driver: "memory"},
		Log:     platform.LogConfig{Level: "info"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for prod + auth.mode=demo")
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
  mode: demo
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

func TestLoadConfigRejectsProdAuthModeDemoFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	content := []byte(`
service: c-api
channel: c
addr: ":8083"
auth:
  mode: demo
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
	t.Setenv("ADMIN_FRAME_AUTH_MODE", "demo")
	if _, err := platform.LoadConfig(path); err == nil {
		t.Fatal("expected validate failure")
	}
}

// TestLoadConfigRejectsLegacyDemoModeField 验证已删除的 auth.demo_mode 不再被兼容：
// 严格解析会把它当作未知字段并拒绝启动（CFG-04）。
func TestLoadConfigRejectsLegacyDemoModeField(t *testing.T) {
	path := writeConfig(t, "legacy.yaml", "service: c-api\n"+
		"channel: c\n"+
		"auth:\n"+
		"  demo_mode: true\n")
	_, err := platform.LoadConfig(path)
	if !errors.Is(err, config.ErrUnknownField) {
		t.Fatalf("err=%v want ErrUnknownField", err)
	}
}

// writeConfig 写入临时配置文件并返回路径。
func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// minimalConfig 是最小的可用服务配置（不含 auth.expected_channel，由 normalize 派生）。
const minimalConfig = "service: c-api\n" +
	"channel: c\n" +
	"addr: \":8083\"\n" +
	"db:\n" +
	"  driver: memory\n"

func TestLoadConfigAppliesDefaults(t *testing.T) {
	path := writeConfig(t, "c.yaml", "service: c-api\nchannel: c\n")
	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Addr != ":8080" || cfg.ShutdownTimeout != 15*time.Second {
		t.Fatalf("addr/timeout defaults missing: %+v", cfg)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Fatalf("log defaults missing: %+v", cfg.Log)
	}
	if cfg.DB.Driver != "memory" || cfg.Storage.Driver != "memory" {
		t.Fatalf("driver defaults missing: db=%q storage=%q", cfg.DB.Driver, cfg.Storage.Driver)
	}
	if cfg.Worker.PollInterval != 5*time.Second {
		t.Fatalf("poll_interval=%s", cfg.Worker.PollInterval)
	}
	if cfg.Auth.ExpectedChannel != "c" {
		t.Fatalf("expected_channel=%q want derived from channel", cfg.Auth.ExpectedChannel)
	}
	if cfg.Tracing.Exporter != "none" || !cfg.Tracing.IsEnabled() {
		t.Fatalf("tracing defaults missing: %+v", cfg.Tracing)
	}
}

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	path := writeConfig(t, "typo.yaml", minimalConfig+"adrr: \":9999\"\n")
	_, err := platform.LoadConfig(path)
	if !errors.Is(err, config.ErrUnknownField) {
		t.Fatalf("err=%v want ErrUnknownField", err)
	}
}

func TestLoadConfigRejectsInvalidEnvValue(t *testing.T) {
	path := writeConfig(t, "c.yaml", minimalConfig)
	t.Setenv("ADMIN_FRAME_SHUTDOWN_TIMEOUT", "15")
	_, err := platform.LoadConfig(path)
	if !errors.Is(err, config.ErrEnvOverride) {
		t.Fatalf("err=%v want ErrEnvOverride", err)
	}
	if !strings.Contains(err.Error(), "ADMIN_FRAME_SHUTDOWN_TIMEOUT") {
		t.Fatalf("err=%v should name the variable", err)
	}
}

func TestLoadConfigDerivesExpectedChannelAfterEnvOverride(t *testing.T) {
	path := writeConfig(t, "c.yaml", minimalConfig)
	t.Setenv("ADMIN_FRAME_CHANNEL", "admin")
	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Channel != "admin" || cfg.Auth.ExpectedChannel != "admin" {
		t.Fatalf("channel=%q expected=%q want both admin", cfg.Channel, cfg.Auth.ExpectedChannel)
	}
}

func TestLoadConfigStorageSecretEnv(t *testing.T) {
	path := writeConfig(t, "c.yaml", minimalConfig)
	t.Setenv("ADMIN_FRAME_STORAGE_S3_ACCESS_KEY", "canonical-ak")
	t.Setenv("ADMIN_FRAME_STORAGE_S3_SECRET_KEY", "canonical-sk")
	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Storage.S3.AccessKey != "canonical-ak" || cfg.Storage.S3.SecretKey != "canonical-sk" {
		t.Fatalf("storage env not applied: %+v", cfg.Storage.S3)
	}
}

func TestLoadConfigTypedEnvKinds(t *testing.T) {
	path := writeConfig(t, "c.yaml", minimalConfig)
	t.Setenv("ADMIN_FRAME_WORKER_POLL_INTERVAL", "250ms")
	t.Setenv("ADMIN_FRAME_TRACING_ENABLED", "off")
	t.Setenv("ADMIN_FRAME_TRACING_SAMPLE_RATIO", "0.5")
	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Worker.PollInterval != 250*time.Millisecond {
		t.Fatalf("poll_interval=%s", cfg.Worker.PollInterval)
	}
	if cfg.Tracing.Enabled == nil || *cfg.Tracing.Enabled {
		t.Fatalf("tracing.enabled=%v want explicit false", cfg.Tracing.Enabled)
	}
	if cfg.Tracing.SampleRatio == nil || *cfg.Tracing.SampleRatio != 0.5 {
		t.Fatalf("sample_ratio=%v", cfg.Tracing.SampleRatio)
	}
}

func TestLoadDBConfigIgnoresServiceFields(t *testing.T) {
	// 完整服务配置（含 storage/tracing）用于 migrate，仅取 db 段。
	path := writeConfig(t, "mysql.yaml", "service: c-api\n"+
		"channel: c\n"+
		"db:\n"+
		"  driver: mysql\n"+
		"  dsn: \"file-dsn\"\n"+
		"storage:\n"+
		"  driver: s3\n")
	t.Setenv("ADMIN_FRAME_DB_DRIVER", "")
	t.Setenv("ADMIN_FRAME_DB_DSN", "")
	cfg, err := platform.LoadDBConfig(path)
	if err != nil {
		t.Fatalf("load db config: %v", err)
	}
	if cfg.Driver != "mysql" || cfg.DSN != "file-dsn" {
		t.Fatalf("db=%+v", cfg)
	}
}

func TestLoadDBConfigMissingFile(t *testing.T) {
	_, err := platform.LoadDBConfig(filepath.Join(t.TempDir(), "absent.yaml"))
	if !errors.Is(err, config.ErrMissingFile) {
		t.Fatalf("err=%v want ErrMissingFile", err)
	}
}
