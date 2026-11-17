package appconfig_test

import (
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/cmd/internal/configboot"
)

// TestValuesOverrideConfig 验证 Values 覆盖代码默认值（ADR-0016 拉取结果注入）。
func TestValuesOverrideConfig(t *testing.T) {
	values := map[string]string{
		"service":                       "admin-api",
		"channel":                       "admin",
		"server.addr":                   ":9090",
		"identity.lockout.max_failures": "99",
	}
	cfg, err := appconfig.LoadFromValues(func() (map[string]string, error) {
		return values, nil
	})
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
	values := map[string]string{
		"service":     "admin-api",
		"channel":     "admin",
		"env":         "dev",
		"server.addr": ":9090",
	}

	cfg, err := appconfig.LoadFromValues(func() (map[string]string, error) {
		return values, nil
	})
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

// TestProcessIdentityEnvVisibleToValidate 覆盖 CC-R-01：各进程身份证在
// LoadFromValues 前注入 env 后，prod 专属门禁必须生效（不得「先校验后赋值」）。
func TestProcessIdentityEnvVisibleToValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		id      configboot.ProcessIdentity
		wantErr string
	}{
		{
			name: "admin-api prod demo forbidden",
			id: configboot.ProcessIdentity{
				Service: "admin-api", Channel: "admin", Env: "prod", Addr: ":8081",
			},
			wantErr: "auth.mode=demo is forbidden when env=prod",
		},
		{
			name: "biz-api prod demo forbidden",
			id: configboot.ProcessIdentity{
				Service: "biz-api", Channel: "biz", Env: "prod", Addr: ":8082",
			},
			wantErr: "auth.mode=demo is forbidden when env=prod",
		},
		{
			name: "c-api prod demo forbidden",
			id: configboot.ProcessIdentity{
				Service: "c-api", Channel: "c", Env: "prod", Addr: ":8083",
			},
			wantErr: "auth.mode=demo is forbidden when env=prod",
		},
		{
			name: "worker prod demo forbidden",
			id: configboot.ProcessIdentity{
				Service: "worker", Channel: "worker", Env: "prod", Addr: ":8090",
			},
			wantErr: "auth.mode=demo is forbidden when env=prod",
		},
		{
			name: "admin-api prod missing hash_key",
			id: configboot.ProcessIdentity{
				Service: "admin-api", Channel: "admin", Env: "prod", Addr: ":8081",
			},
			wantErr: "identity.hash_key is required in prod/staging",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := appconfig.LoadFromValues(func() (map[string]string, error) {
				values := configboot.WithProcessIdentity(map[string]string{
					"auth.mode":        "demo",
					"identity.enabled": "true",
					"log.format":       "json",
				}, tc.id)
				return values, nil
			})
			if err == nil {
				t.Fatal("expected Validate failure when env is injected before Load")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v want substring %q", err, tc.wantErr)
			}
		})
	}
}

// TestConfigAPIEnvInjectedBeforeValidate 覆盖 config-api 自举路径的 CC-R-01。
func TestConfigAPIEnvInjectedBeforeValidate(t *testing.T) {
	t.Parallel()
	_, err := appconfig.LoadFromValues(func() (map[string]string, error) {
		return map[string]string{
			"service":    "config-api",
			"channel":    "internal",
			"addr":       ":8085",
			"env":        "prod",
			"auth.mode":  "demo",
			"log.format": "json",
		}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "auth.mode=demo is forbidden when env=prod") {
		t.Fatalf("err=%v want prod demo rejection", err)
	}
}
