package appconfig_test

import (
	"path/filepath"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
)

// TestValuesOverrideConfig 验证 Values 覆盖静态配置（ADR-0016 拉取结果注入）。
func TestValuesOverrideConfig(t *testing.T) {
	configPath := writeConfig(t, "admin.yaml", `
service: admin-api
channel: admin
addr: ":8080"
identity:
  lockout:
    max_failures: 5
`)
	values := map[string]string{
		"server.addr":                  ":9090",
		"identity.lockout.max_failures": "99",
	}
	cfg, err := appconfig.LoadWithValuesAllowMissing(configPath, func() (map[string]string, error) {
		return values, nil
	}, false)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Fatalf("addr = %q, want values override", cfg.Addr)
	}
	if cfg.Identity.Lockout.MaxFailures != 99 {
		t.Fatalf("max failures = %d, want 99", cfg.Identity.Lockout.MaxFailures)
	}
}

// TestLoadWithoutConfigFile 验证无文件启动：业务键来自 Values。
func TestLoadWithoutConfigFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	values := map[string]string{
		"service":     "admin-api",
		"channel":     "admin",
		"env":         "dev",
		"server.addr": ":9090",
	}

	cfg, err := appconfig.LoadWithValuesAllowMissing(missing, func() (map[string]string, error) {
		return values, nil
	}, true)
	if err != nil {
		t.Fatalf("load without file: %v", err)
	}
	if cfg.Service != "admin-api" || cfg.Channel != "admin" {
		t.Fatalf("deployment identity = %s/%s", cfg.Service, cfg.Channel)
	}
	if cfg.Addr != ":9090" {
		t.Fatalf("addr = %q, want values", cfg.Addr)
	}
}
