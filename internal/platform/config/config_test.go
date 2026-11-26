package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/config"
)

func TestValidateRejectsProdAuthModeDemo(t *testing.T) {
	cfg := &platformconfig.Config{
		Env:     "prod",
		Service: "c-api",
		Channel: "c",
		Addr:    ":8083",
		Auth:    platformconfig.AuthConfig{Mode: platformconfig.AuthModeDemo, ExpectedChannel: "c"},
		DB:      platformconfig.DBConfig{Driver: "memory"},
		Log:     platformconfig.LogConfig{Level: "info"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for prod + auth.mode=demo")
	}
}

func TestValidateChannelMismatch(t *testing.T) {
	cfg := &platformconfig.Config{
		Service: "c-api",
		Channel: "c",
		Addr:    ":8083",
		Auth:    platformconfig.AuthConfig{ExpectedChannel: "admin"},
		DB:      platformconfig.DBConfig{Driver: "memory"},
		Log:     platformconfig.LogConfig{Level: "info"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected channel mismatch error")
	}
}

func TestValidateAcceptsInternalChannel(t *testing.T) {
	cfg := &platformconfig.Config{
		Service: "config-api",
		Channel: "internal",
		Env:     "dev",
		Addr:    ":8085",
		Auth:    platformconfig.AuthConfig{ExpectedChannel: "internal"},
		DB:      platformconfig.DBConfig{Driver: "memory"},
		Log:     platformconfig.LogConfig{Level: "info", Format: "json"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("internal channel should be valid: %v", err)
	}
}

func TestValidateRejectsEmptyEnv(t *testing.T) {
	cfg := &platformconfig.Config{
		Service: "config-api",
		Channel: "internal",
		Addr:    ":8085",
		Auth:    platformconfig.AuthConfig{ExpectedChannel: "internal"},
		DB:      platformconfig.DBConfig{Driver: "memory"},
		Log:     platformconfig.LogConfig{Level: "info", Format: "json"},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "env is required") {
		t.Fatalf("err=%v want env is required", err)
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

	// 业务键不得经 ADMIN_FRAME_* 覆盖；仅 DB/Redis 白名单生效。
	t.Setenv("ADMIN_FRAME_ADDR", ":18083")
	t.Setenv("ADMIN_FRAME_LOG_LEVEL", "warn")
	t.Setenv("ADMIN_FRAME_SHUTDOWN_TIMEOUT", "30s")
	t.Setenv("ADMIN_FRAME_DB_DSN", "env-dsn")

	cfg, err := platformconfig.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr == ":18083" {
		t.Fatal("addr must not be overridden by ADMIN_FRAME_ADDR")
	}
	if cfg.Log.Level == "warn" {
		t.Fatal("log.level must not be overridden by ADMIN_FRAME_LOG_LEVEL")
	}
	if cfg.DB.DSN != "env-dsn" {
		t.Fatalf("db.dsn=%s want env-dsn", cfg.DB.DSN)
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

	got, err := platformconfig.LoadDBConfig(path)
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
	got, err := platformconfig.LoadDBConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Driver != "mysql" || got.DSN != "env-only" {
		t.Fatalf("got=%+v", got)
	}
}

func TestLoadConfigRejectsProdAuthModeDemo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	content := []byte(`
service: c-api
channel: c
env: prod
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
	if _, err := platformconfig.LoadConfig(path); err == nil {
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
	_, err := platformconfig.LoadConfig(path)
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
	"env: dev\n" +
	"addr: \":8083\"\n" +
	"db:\n" +
	"  driver: memory\n"

func TestLoadConfigAppliesDefaults(t *testing.T) {
	path := writeConfig(t, "c.yaml", "service: c-api\nchannel: c\nenv: dev\n")
	cfg, err := platformconfig.LoadConfig(path)
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
	_, err := platformconfig.LoadConfig(path)
	if !errors.Is(err, config.ErrUnknownField) {
		t.Fatalf("err=%v want ErrUnknownField", err)
	}
}

func TestLoadConfigRejectsInvalidDBEnvValue(t *testing.T) {
	path := writeConfig(t, "c.yaml", minimalConfig)
	t.Setenv("ADMIN_FRAME_DB_MAX_OPEN_CONNS", "not-int")
	_, err := platformconfig.LoadConfig(path)
	if !errors.Is(err, config.ErrEnvOverride) {
		t.Fatalf("err=%v want ErrEnvOverride", err)
	}
	if !strings.Contains(err.Error(), "ADMIN_FRAME_DB_MAX_OPEN_CONNS") {
		t.Fatalf("err=%v should name the variable", err)
	}
}

func TestLoadConfigIgnoresBusinessEnvOverride(t *testing.T) {
	path := writeConfig(t, "c.yaml", minimalConfig)
	t.Setenv("ADMIN_FRAME_CHANNEL", "admin")
	t.Setenv("ADMIN_FRAME_AUTH_JWT_SECRET", strings.Repeat("x", 32))
	cfg, err := platformconfig.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Channel == "admin" {
		t.Fatal("channel must not be overridden by ADMIN_FRAME_CHANNEL")
	}
	if cfg.Auth.JWT.Secret == strings.Repeat("x", 32) {
		t.Fatal("jwt secret must not be overridden by ADMIN_FRAME_AUTH_JWT_SECRET")
	}
}

func TestLoadConfigRedisEnvOverride(t *testing.T) {
	path := writeConfig(t, "c.yaml", minimalConfig+"redis:\n  addr: \"127.0.0.1:6379\"\n  password: \"file-pass\"\n")
	t.Setenv("ADMIN_FRAME_REDIS_PASSWORD", "env-pass")
	cfg, err := platformconfig.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Redis.Password != "env-pass" {
		t.Fatalf("redis.password=%q want env-pass", cfg.Redis.Password)
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
	cfg, err := platformconfig.LoadDBConfig(path)
	if err != nil {
		t.Fatalf("load db config: %v", err)
	}
	if cfg.Driver != "mysql" || cfg.DSN != "file-dsn" {
		t.Fatalf("db=%+v", cfg)
	}
}

func TestLoadDBConfigMissingFile(t *testing.T) {
	_, err := platformconfig.LoadDBConfig(filepath.Join(t.TempDir(), "absent.yaml"))
	if !errors.Is(err, config.ErrMissingFile) {
		t.Fatalf("err=%v want ErrMissingFile", err)
	}
}
