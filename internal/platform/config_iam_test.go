package platform_test

import (
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/security"
)

// iamBaseConfig 返回可直接 Validate 的最小 IAM 配置（字段已按 applyDefaults 填充）。
func iamBaseConfig() *platform.Config {
	return &platform.Config{
		Env:     "local",
		Service: "admin-api",
		Channel: "admin",
		Addr:    ":8081",
		Auth:    platform.AuthConfig{Mode: platform.AuthModeDemo, ExpectedChannel: "admin"},
		Log:     platform.LogConfig{Level: "info", Format: "json"},
		DB:      platform.DBConfig{Driver: "memory"},
		Storage: platform.StorageConfig{Driver: "memory"},
		HTTP:    platform.HTTPConfig{Timeout: 30 * time.Second},
		IAM: platform.IAMConfig{
			Mode:        platform.IAMModeEnforce,
			LoadTimeout: 300 * time.Millisecond,
			Cache: platform.IAMCacheConfig{
				Driver:      platform.IAMCacheDriverMemory,
				L1TTL:       3 * time.Second,
				L2TTL:       5 * time.Minute,
				NegativeTTL: 30 * time.Second,
			},
			Limits: platform.IAMLimitsConfig{
				MaxRolesPerSubject:        20,
				MaxPermissionsPerRole:     200,
				MaxBindingsPerSubject:     50,
				MaxCustomRolesPerMerchant: 50,
			},
		},
	}
}

func TestValidateIAMDefaultsAndRules(t *testing.T) {
	cfg := iamBaseConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("baseline iam config: %v", err)
	}

	unsupported := iamBaseConfig()
	unsupported.IAM.Mode = "on"
	if err := unsupported.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported iam.mode") {
		t.Fatalf("err=%v want unsupported iam.mode", err)
	}

	slowLoad := iamBaseConfig()
	slowLoad.IAM.LoadTimeout = 2 * time.Second
	if err := slowLoad.Validate(); err == nil || !strings.Contains(err.Error(), "iam.load_timeout") {
		t.Fatalf("err=%v want iam.load_timeout rejection", err)
	}

	badTTL := iamBaseConfig()
	badTTL.IAM.Cache.L2TTL = time.Second
	if err := badTTL.Validate(); err == nil || !strings.Contains(err.Error(), "iam.cache.l2_ttl") {
		t.Fatalf("err=%v want l2_ttl rejection", err)
	}

	badNegative := iamBaseConfig()
	badNegative.IAM.Cache.NegativeTTL = 10 * time.Minute
	if err := badNegative.Validate(); err == nil || !strings.Contains(err.Error(), "iam.cache.negative_ttl") {
		t.Fatalf("err=%v want negative_ttl rejection", err)
	}

	badLimits := iamBaseConfig()
	badLimits.IAM.Limits.MaxRolesPerSubject = 0
	if err := badLimits.Validate(); err == nil || !strings.Contains(err.Error(), "max_roles_per_subject") {
		t.Fatalf("err=%v want limits rejection", err)
	}

	redisMissing := iamBaseConfig()
	redisMissing.IAM.Cache.Driver = platform.IAMCacheDriverRedis
	if err := redisMissing.Validate(); err == nil || !strings.Contains(err.Error(), "redis.addr is required") {
		t.Fatalf("err=%v want redis.addr requirement", err)
	}
}

// prodJWTConfig 返回可通过生产校验的最小 JWT 配置（仅测试用假密钥）。
func prodJWTConfig() platform.AuthConfig {
	return platform.AuthConfig{
		Mode:            platform.AuthModeJWT,
		ExpectedChannel: "admin",
		JWT: security.JWTConfig{
			Alg:      "HS256",
			Issuer:   "https://iam.example.com",
			Audience: []string{"admin"},
			Secret:   "0123456789abcdef0123456789abcdef",
		},
	}
}

func TestValidateIAMProductionRules(t *testing.T) {
	memoryEnforce := iamBaseConfig()
	memoryEnforce.Env = "prod"
	memoryEnforce.Auth = prodJWTConfig()
	if err := memoryEnforce.Validate(); err == nil || !strings.Contains(err.Error(), "iam.cache.driver must be redis") {
		t.Fatalf("err=%v want prod enforce redis requirement", err)
	}

	prodRedis := iamBaseConfig()
	prodRedis.Env = "prod"
	prodRedis.Auth = prodJWTConfig()
	prodRedis.Storage.Driver = "s3"
	prodRedis.IAM.Cache.Driver = platform.IAMCacheDriverRedis
	prodRedis.Redis = platform.RedisConfig{Addr: "127.0.0.1:6379"}
	if err := prodRedis.Validate(); err != nil {
		t.Fatalf("prod redis enforce: %v", err)
	}

	prodWildcard := iamBaseConfig()
	prodWildcard.Env = "prod"
	prodWildcard.Auth = prodJWTConfig()
	prodWildcard.Storage.Driver = "s3"
	prodWildcard.IAM.Security.AllowWildcard = true
	prodWildcard.IAM.Cache.Driver = platform.IAMCacheDriverRedis
	prodWildcard.Redis = platform.RedisConfig{Addr: "127.0.0.1:6379"}
	if err := prodWildcard.Validate(); err == nil || !strings.Contains(err.Error(), "allow_wildcard") {
		t.Fatalf("err=%v want prod wildcard rejection", err)
	}
}

func TestValidateIAMRejectsUnsupportedChannel(t *testing.T) {
	cfg := &platform.Config{
		Env:     "local",
		Service: "c-api",
		Channel: "c",
		Addr:    ":8083",
		Auth:    platform.AuthConfig{Mode: platform.AuthModeDemo, ExpectedChannel: "c"},
		Log:     platform.LogConfig{Level: "info", Format: "json"},
		DB:      platform.DBConfig{Driver: "memory"},
		IAM: platform.IAMConfig{
			Mode:        platform.IAMModeShadow,
			LoadTimeout: 300 * time.Millisecond,
			Cache: platform.IAMCacheConfig{
				Driver: platform.IAMCacheDriverMemory, L1TTL: 3 * time.Second,
				L2TTL: 5 * time.Minute, NegativeTTL: 30 * time.Second,
			},
			Limits: platform.IAMLimitsConfig{
				MaxRolesPerSubject: 20, MaxPermissionsPerRole: 200, MaxBindingsPerSubject: 50,
			},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "iam.mode must be off") {
		t.Fatalf("err=%v want channel rejection", err)
	}
}

func TestLoadConfigIAMDefaultsAndEnvOverride(t *testing.T) {
	path := writeConfig(t, "admin.yaml", "service: admin-api\nchannel: admin\n")
	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.IAM.Mode != platform.IAMModeOff {
		t.Fatalf("iam.mode = %q, want off", cfg.IAM.Mode)
	}
	if cfg.IAM.LoadTimeout != 300*time.Millisecond {
		t.Fatalf("load_timeout = %s", cfg.IAM.LoadTimeout)
	}
	if cfg.IAM.Cache.Driver != platform.IAMCacheDriverMemory ||
		cfg.IAM.Cache.L1TTL != 3*time.Second ||
		cfg.IAM.Cache.L2TTL != 5*time.Minute ||
		cfg.IAM.Cache.NegativeTTL != 30*time.Second {
		t.Fatalf("cache defaults = %+v", cfg.IAM.Cache)
	}
	if cfg.IAM.Limits.MaxRolesPerSubject != 20 || cfg.IAM.Limits.MaxPermissionsPerRole != 200 {
		t.Fatalf("limit defaults = %+v", cfg.IAM.Limits)
	}

	t.Setenv("ADMIN_FRAME_IAM_MODE", "shadow")
	cfg, err = platform.LoadConfig(path)
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.IAM.Mode != platform.IAMModeShadow {
		t.Fatalf("iam.mode = %q, want shadow", cfg.IAM.Mode)
	}
}

func TestValidateIAMBreakglassRules(t *testing.T) {
	enabled := iamBaseConfig()
	enabled.IAM.Breakglass = platform.IAMBreakglassConfig{
		Enabled:         true,
		AllowedSubjects: []string{"1001"},
	}
	if err := enabled.Validate(); err != nil {
		t.Fatalf("breakglass enforce: %v", err)
	}

	shadow := iamBaseConfig()
	shadow.IAM.Mode = platform.IAMModeShadow
	shadow.IAM.Breakglass = platform.IAMBreakglassConfig{Enabled: true}
	if err := shadow.Validate(); err == nil || !strings.Contains(err.Error(), "requires iam.mode=enforce") {
		t.Fatalf("err=%v want breakglass enforce requirement", err)
	}

	biz := iamBaseConfig()
	biz.Channel = "biz"
	biz.Auth.ExpectedChannel = "biz"
	biz.IAM.Breakglass = platform.IAMBreakglassConfig{Enabled: true}
	if err := biz.Validate(); err == nil || !strings.Contains(err.Error(), "channel=admin") {
		t.Fatalf("err=%v want admin-only rejection", err)
	}

	prod := iamBaseConfig()
	prod.Env = "prod"
	prod.Auth = prodJWTConfig()
	prod.Storage.Driver = "s3"
	prod.IAM.Cache.Driver = platform.IAMCacheDriverRedis
	prod.Redis = platform.RedisConfig{Addr: "127.0.0.1:6379"}
	prod.IAM.Breakglass = platform.IAMBreakglassConfig{Enabled: true}
	if err := prod.Validate(); err == nil || !strings.Contains(err.Error(), "allowed_subjects") {
		t.Fatalf("err=%v want prod allowlist requirement", err)
	}

	blank := iamBaseConfig()
	blank.IAM.Breakglass = platform.IAMBreakglassConfig{
		Enabled:         true,
		AllowedSubjects: []string{"  "},
	}
	if err := blank.Validate(); err == nil || !strings.Contains(err.Error(), "allowed_subjects[0]") {
		t.Fatalf("err=%v want blank subject rejection", err)
	}
}

func TestRedisConfigRedaction(t *testing.T) {
	cfg := platform.RedisConfig{Addr: "127.0.0.1:6379", Password: "p@ss", DB: 1}
	out := cfg.LogValue().String()
	if strings.Contains(out, "p@ss") {
		t.Fatalf("redis password leaked: %s", out)
	}
	if !strings.Contains(out, "***") {
		t.Fatalf("expected masked placeholder: %s", out)
	}
	if cfg.Enabled() != true {
		t.Fatal("redis with addr must be enabled")
	}
	if (platform.RedisConfig{}).Enabled() {
		t.Fatal("empty redis must be disabled")
	}
}
