package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/config"
)

// devIAMConfig 返回一份能通过校验的最小 IAM 配置（local / admin 端）。
func devIAMConfig() *config.IAMConfig {
	return &config.IAMConfig{
		Mode:        config.IAMModeEnforce,
		LoadTimeout: 300 * time.Millisecond,
		Cache: config.IAMCacheConfig{
			Driver:       config.IAMCacheDriverMemory,
			L1TTL:        3 * time.Second,
			L2TTL:        5 * time.Minute,
			NegativeTTL:  30 * time.Second,
			KeyPrefix:    "iam",
			L1MaxEntries: 10000,
		},
		Limits: config.IAMLimitsConfig{
			MaxRolesPerSubject:        20,
			MaxPermissionsPerRole:     200,
			MaxBindingsPerSubject:     50,
			MaxCustomRolesPerMerchant: 50,
		},
	}
}

// prodIAMConfig 返回生产 A/B 端的合规配置：enforce + redis + 审批 + 异步均开启。
func prodIAMConfig() *config.IAMConfig {
	cfg := devIAMConfig()
	cfg.Cache.Driver = config.IAMCacheDriverRedis
	cfg.Approval.Enabled = true
	return cfg
}

func TestApplyDefaultsFillsZeroValue(t *testing.T) {
	var cfg config.IAMConfig
	cfg.ApplyDefaults()

	if cfg.LoadTimeout != 300*time.Millisecond {
		t.Fatalf("LoadTimeout=%v want 300ms", cfg.LoadTimeout)
	}
	if cfg.Cache.Driver != config.IAMCacheDriverMemory {
		t.Fatalf("Cache.Driver=%q want memory", cfg.Cache.Driver)
	}
	if cfg.Cache.L1TTL != 3*time.Second {
		t.Fatalf("Cache.L1TTL=%v want 3s", cfg.Cache.L1TTL)
	}
	if cfg.Cache.L2TTL != 5*time.Minute {
		t.Fatalf("Cache.L2TTL=%v want 5m", cfg.Cache.L2TTL)
	}
	if cfg.Cache.NegativeTTL != 30*time.Second {
		t.Fatalf("Cache.NegativeTTL=%v want 30s", cfg.Cache.NegativeTTL)
	}
	if cfg.Cache.KeyPrefix != "iam" {
		t.Fatalf("Cache.KeyPrefix=%q want iam", cfg.Cache.KeyPrefix)
	}
	if cfg.Cache.L1MaxEntries != 10000 {
		t.Fatalf("Cache.L1MaxEntries=%d want 10000", cfg.Cache.L1MaxEntries)
	}
	if cfg.Limits.MaxRolesPerSubject != 20 {
		t.Fatalf("Limits.MaxRolesPerSubject=%d want 20", cfg.Limits.MaxRolesPerSubject)
	}
	if cfg.Limits.MaxPermissionsPerRole != 200 {
		t.Fatalf("Limits.MaxPermissionsPerRole=%d want 200", cfg.Limits.MaxPermissionsPerRole)
	}
	if cfg.Limits.MaxBindingsPerSubject != 50 {
		t.Fatalf("Limits.MaxBindingsPerSubject=%d want 50", cfg.Limits.MaxBindingsPerSubject)
	}
	if cfg.Limits.MaxCustomRolesPerMerchant != 50 {
		t.Fatalf("Limits.MaxCustomRolesPerMerchant=%d want 50", cfg.Limits.MaxCustomRolesPerMerchant)
	}
	// 默认开启访问复核：未配置与显式 false 语义不同（§P1-7）。
	if !cfg.Certification.IsEnabled() {
		t.Fatal("Certification.IsEnabled()=false want true by default")
	}
	// Mode 不在默认值里，留给 Normalize 归一为 off。
	if cfg.Mode != "" {
		t.Fatalf("Mode=%q want empty (Normalize owns it)", cfg.Mode)
	}
}

func TestApplyDefaultsKeepsExplicitValues(t *testing.T) {
	cfg := &config.IAMConfig{
		LoadTimeout: time.Second,
		Cache: config.IAMCacheConfig{
			Driver:       config.IAMCacheDriverRedis,
			L1TTL:        time.Second,
			L2TTL:        10 * time.Minute,
			NegativeTTL:  20 * time.Second,
			KeyPrefix:    "custom",
			L1MaxEntries: 500,
		},
		Limits: config.IAMLimitsConfig{
			MaxRolesPerSubject:        5,
			MaxPermissionsPerRole:     7,
			MaxBindingsPerSubject:     9,
			MaxCustomRolesPerMerchant: 11,
		},
		Certification: config.IAMCertificationConfig{Enabled: boolPtr(false)},
	}
	cfg.ApplyDefaults()

	if cfg.LoadTimeout != time.Second || cfg.Cache.Driver != config.IAMCacheDriverRedis {
		t.Fatalf("explicit values overwritten: %+v", cfg)
	}
	if cfg.Cache.KeyPrefix != "custom" || cfg.Cache.L1MaxEntries != 500 {
		t.Fatalf("explicit cache values overwritten: %+v", cfg.Cache)
	}
	if cfg.Limits.MaxRolesPerSubject != 5 || cfg.Limits.MaxPermissionsPerRole != 7 ||
		cfg.Limits.MaxBindingsPerSubject != 9 || cfg.Limits.MaxCustomRolesPerMerchant != 11 {
		t.Fatalf("explicit limits overwritten: %+v", cfg.Limits)
	}
	// 显式 false 必须保留，否则无法下线访问复核。
	if cfg.Certification.IsEnabled() {
		t.Fatal("explicit certification.enabled=false must be preserved")
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name       string
		mode       string
		constraint string
		wantMode   string
		wantConstr string
	}{
		{"zero value falls back to off/shadow", "", "", config.IAMModeOff, config.IAMConstraintsModeShadow},
		{"trims and lowercases", "  Enforce  ", " ENFORCE ", config.IAMModeEnforce, config.IAMConstraintsModeEnforce},
		{"shadow preserved", "shadow", "off", config.IAMModeShadow, config.IAMConstraintsModeOff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.IAMConfig{Mode: tc.mode}
			cfg.Constraints.Mode = tc.constraint
			cfg.Normalize()
			if cfg.Mode != tc.wantMode {
				t.Fatalf("Mode=%q want %q", cfg.Mode, tc.wantMode)
			}
			if cfg.Constraints.Mode != tc.wantConstr {
				t.Fatalf("Constraints.Mode=%q want %q", cfg.Constraints.Mode, tc.wantConstr)
			}
		})
	}
}

func TestEnabledReportsSnapshotLoad(t *testing.T) {
	cases := map[string]bool{
		config.IAMModeOff:     false,
		config.IAMModeShadow:  true,
		config.IAMModeEnforce: true,
		"":                    false,
	}
	for mode, want := range cases {
		if got := (config.IAMConfig{Mode: mode}).Enabled(); got != want {
			t.Fatalf("mode=%q Enabled()=%v want %v", mode, got, want)
		}
	}
}

func TestValidateAcceptsCompliantConfig(t *testing.T) {
	if err := devIAMConfig().Validate("local", "admin", "mysql", true, true); err != nil {
		t.Fatalf("dev config rejected: %v", err)
	}
	if err := prodIAMConfig().Validate("prod", "admin", "mysql", true, true); err != nil {
		t.Fatalf("prod config rejected: %v", err)
	}
	if err := prodIAMConfig().Validate("staging", "biz", "mysql", true, true); err != nil {
		t.Fatalf("staging config rejected: %v", err)
	}
	// 完全未配置 iam 段时保持历史行为，不得误报。
	var zero config.IAMConfig
	if err := zero.Validate("local", "c", "memory", false, false); err != nil {
		t.Fatalf("zero-value config rejected: %v", err)
	}
}

// TestValidateRejects 覆盖每条拒绝分支：越权与 4-eyes 一旦被误配，
// 只会在故障后暴露，故逐条锁定错误文案里的字段路径。
func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name          string
		mutate        func(*config.IAMConfig)
		env           string
		channel       string
		dbDriver      string
		redisEnabled  bool
		asyncEnabled  bool
		wantErrSubstr string
	}{
		{
			name:          "unsupported mode",
			mutate:        func(c *config.IAMConfig) { c.Mode = "on" },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "unsupported iam.mode",
		},
		{
			name:          "prod admin must enforce",
			mutate:        func(c *config.IAMConfig) { c.Mode = config.IAMModeShadow },
			env:           "prod",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.mode must be enforce",
		},
		{
			name:          "prod admin requires approval",
			mutate:        func(c *config.IAMConfig) { c.Approval.Enabled = false },
			env:           "prod",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.approval.enabled must be true",
		},
		{
			name:          "c channel must be off",
			mutate:        func(*config.IAMConfig) {},
			env:           "local",
			channel:       "c",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.mode must be off when channel is c",
		},
		{
			name:          "worker channel must be off",
			mutate:        func(*config.IAMConfig) {},
			env:           "local",
			channel:       "worker",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.mode must be off when channel is worker",
		},
		{
			name:          "load timeout too high",
			mutate:        func(c *config.IAMConfig) { c.LoadTimeout = 2 * time.Second },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.load_timeout",
		},
		{
			name:          "load timeout not positive",
			mutate:        func(c *config.IAMConfig) { c.LoadTimeout = -time.Second },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.load_timeout",
		},
		{
			name: "redis driver without redis",
			mutate: func(c *config.IAMConfig) {
				c.Cache.Driver = config.IAMCacheDriverRedis
			},
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  false,
			asyncEnabled:  true,
			wantErrSubstr: "redis.addr is required",
		},
		{
			name:          "unsupported cache driver",
			mutate:        func(c *config.IAMConfig) { c.Cache.Driver = "memcached" },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "unsupported iam.cache.driver",
		},
		{
			name:          "l1 ttl too high",
			mutate:        func(c *config.IAMConfig) { c.Cache.L1TTL = 6 * time.Second },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.cache.l1_ttl",
		},
		{
			name:          "l2 ttl too high",
			mutate:        func(c *config.IAMConfig) { c.Cache.L2TTL = 16 * time.Minute },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.cache.l2_ttl must be in",
		},
		{
			name:          "l2 ttl not greater than l1",
			mutate:        func(c *config.IAMConfig) { c.Cache.L2TTL = c.Cache.L1TTL },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "must be greater than iam.cache.l1_ttl",
		},
		{
			name:          "negative ttl above l2",
			mutate:        func(c *config.IAMConfig) { c.Cache.NegativeTTL = 10 * time.Minute },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.cache.negative_ttl",
		},
		{
			name:          "negative l1 max entries",
			mutate:        func(c *config.IAMConfig) { c.Cache.L1MaxEntries = -1 },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.cache.l1_max_entries",
		},
		{
			name:          "roles limit not positive",
			mutate:        func(c *config.IAMConfig) { c.Limits.MaxRolesPerSubject = 0 },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.limits.max_roles_per_subject",
		},
		{
			name:          "permissions limit not positive",
			mutate:        func(c *config.IAMConfig) { c.Limits.MaxPermissionsPerRole = 0 },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.limits.max_permissions_per_role",
		},
		{
			name:          "bindings limit not positive",
			mutate:        func(c *config.IAMConfig) { c.Limits.MaxBindingsPerSubject = 0 },
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.limits.max_bindings_per_subject",
		},
		{
			name:          "biz requires custom roles limit",
			mutate:        func(c *config.IAMConfig) { c.Limits.MaxCustomRolesPerMerchant = 0 },
			env:           "local",
			channel:       "biz",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "max_custom_roles_per_merchant",
		},
		{
			name: "unsupported constraints mode",
			mutate: func(c *config.IAMConfig) {
				c.Constraints.Mode = "warn"
			},
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "unsupported iam.constraints.mode",
		},
		{
			name: "prod enforce requires redis cache",
			mutate: func(c *config.IAMConfig) {
				c.Cache.Driver = config.IAMCacheDriverMemory
			},
			env:           "prod",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.cache.driver must be redis",
		},
		{
			name:          "prod forbids wildcard",
			mutate:        func(c *config.IAMConfig) { c.Security.AllowWildcard = true },
			env:           "prod",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.security.allow_wildcard must be false",
		},
		{
			name:          "breakglass only on admin",
			mutate:        func(c *config.IAMConfig) { c.Breakglass.Enabled = true },
			env:           "local",
			channel:       "biz",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.breakglass.enabled is only supported on channel=admin",
		},
		{
			name: "breakglass requires enforce",
			mutate: func(c *config.IAMConfig) {
				c.Mode = config.IAMModeShadow
				c.Breakglass.Enabled = true
			},
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.breakglass.enabled requires iam.mode=enforce",
		},
		{
			name: "breakglass needs subjects in prod",
			mutate: func(c *config.IAMConfig) {
				c.Breakglass.Enabled = true
			},
			env:           "prod",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.breakglass.allowed_subjects is required",
		},
		{
			name: "breakglass rejects blank subject",
			mutate: func(c *config.IAMConfig) {
				c.Breakglass.Enabled = true
				c.Breakglass.AllowedSubjects = []string{"ok", "  "}
			},
			env:           "staging",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "allowed_subjects[1] must not be empty",
		},
		{
			name:          "approval unsupported on c channel",
			mutate:        func(c *config.IAMConfig) { c.Mode = config.IAMModeOff },
			env:           "local",
			channel:       "c",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "iam.approval.enabled is only supported",
		},
		{
			name:          "approval needs persistent db in prod",
			mutate:        func(*config.IAMConfig) {},
			env:           "prod",
			channel:       "admin",
			dbDriver:      "memory",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "requires a persistent db.driver",
		},
		{
			name:          "prod enforce requires async",
			mutate:        func(*config.IAMConfig) {},
			env:           "staging",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  false,
			wantErrSubstr: "async.enabled must be true",
		},
		{
			name: "auto revoke requires certification enabled",
			mutate: func(c *config.IAMConfig) {
				c.Certification = config.IAMCertificationConfig{Enabled: boolPtr(false), AutoRevoke: true}
			},
			env:           "local",
			channel:       "admin",
			dbDriver:      "mysql",
			redisEnabled:  true,
			asyncEnabled:  true,
			wantErrSubstr: "auto_revoke requires iam.certification.enabled=true",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// prod 场景从生产合规配置出发，避免被无关分支提前挡住；
			// 其余场景从 dev 配置出发，只让被测分支生效。
			cfg := devIAMConfig()
			if tc.env == "prod" || tc.env == "staging" {
				cfg = prodIAMConfig()
			}
			if tc.name == "approval unsupported on c channel" {
				cfg.Approval.Enabled = true
			}
			tc.mutate(cfg)

			err := cfg.Validate(tc.env, tc.channel, tc.dbDriver, tc.redisEnabled, tc.asyncEnabled)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("err=%v want substring %q", err, tc.wantErrSubstr)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }
