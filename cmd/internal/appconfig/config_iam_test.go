package appconfig_test

import (
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"
	"github.com/admin-frame/admin-frame/internal/security"
)

// iamBaseConfig 返回可直接 Validate 的最小 IAM 配置（字段已按 applyDefaults 填充）。
func iamBaseConfig() *appconfig.Config {
	return &appconfig.Config{
		Config: platformconfig.Config{
			Env:     "local",
			Service: "admin-api",
			Channel: "admin",
			Addr:    ":8081",
			Auth:    platformconfig.AuthConfig{Mode: platformconfig.AuthModeDemo, ExpectedChannel: "admin"},
			Log:     platformconfig.LogConfig{Level: "info", Format: "json"},
			DB:      platformconfig.DBConfig{Driver: "memory"},
			Storage: platformstorage.Config{Driver: "memory"},
			HTTP:    platformconfig.HTTPConfig{Timeout: 30 * time.Second},
		},
		IAM: iamconfig.IAMConfig{
			Mode:        iamconfig.IAMModeEnforce,
			LoadTimeout: 300 * time.Millisecond,
			Cache: iamconfig.IAMCacheConfig{
				Driver:      iamconfig.IAMCacheDriverMemory,
				L1TTL:       3 * time.Second,
				L2TTL:       5 * time.Minute,
				NegativeTTL: 30 * time.Second,
			},
			Limits: iamconfig.IAMLimitsConfig{
				MaxRolesPerSubject:        20,
				MaxPermissionsPerRole:     200,
				MaxBindingsPerSubject:     50,
				MaxCustomRolesPerMerchant: 50,
			},
			Approval: iamconfig.IAMApprovalConfig{Enabled: true},
		},
	}
}

// prodIdempotencyConfig 补齐生产 A/B 端强制项：幂等开启且缺键即拒绝。
func prodIdempotencyConfig(cfg *appconfig.Config) *appconfig.Config {
	cfg.Idempotency = platformconfig.IdempotencyConfig{
		Enabled:      true,
		Driver:       platformconfig.IdempotencyDriverRedis,
		Header:       "Idempotency-Key",
		TTL:          24 * time.Hour,
		Required:     true,
		MaxBodyBytes: 1 << 20,
	}
	return cfg
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
	redisMissing.IAM.Cache.Driver = iamconfig.IAMCacheDriverRedis
	if err := redisMissing.Validate(); err == nil || !strings.Contains(err.Error(), "redis.addr is required") {
		t.Fatalf("err=%v want redis.addr requirement", err)
	}
}

// prodJWTConfig 返回可通过生产校验的最小 JWT 配置（仅测试用假密钥）。
func prodJWTConfig() platformconfig.AuthConfig {
	return platformconfig.AuthConfig{
		Mode:            platformconfig.AuthModeJWT,
		ExpectedChannel: "admin",
		JWT: security.JWTConfig{
			Alg:      "HS256",
			Issuer:   "https://iam.example.com",
			Audience: []string{"admin"},
			Secret:   "0123456789abcdef0123456789abcdef",
		},
	}
}

// prodIDConfig 返回满足生产必填项的最小 ID 配置。
func prodIDConfig() platformconfig.IDConfig {
	nodeID := int64(7)
	return platformconfig.IDConfig{NodeID: &nodeID}
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
	prodRedis.ID = prodIDConfig()
	prodRedis.Storage.Driver = "s3"
	prodRedis.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
	prodRedis.IAM.Cache.Driver = iamconfig.IAMCacheDriverRedis
	prodRedis.Redis = platformconfig.RedisConfig{Addr: "127.0.0.1:6379"}
	prodRedis.Async.Enabled = true
	prodIdempotencyConfig(prodRedis)
	if err := prodRedis.Validate(); err != nil {
		t.Fatalf("prod redis enforce: %v", err)
	}

	prodWildcard := iamBaseConfig()
	prodWildcard.Env = "prod"
	prodWildcard.Auth = prodJWTConfig()
	prodWildcard.ID = prodIDConfig()
	prodWildcard.Storage.Driver = "s3"
	prodWildcard.IAM.Security.AllowWildcard = true
	prodWildcard.IAM.Cache.Driver = iamconfig.IAMCacheDriverRedis
	prodWildcard.Redis = platformconfig.RedisConfig{Addr: "127.0.0.1:6379"}
	if err := prodWildcard.Validate(); err == nil || !strings.Contains(err.Error(), "allow_wildcard") {
		t.Fatalf("err=%v want prod wildcard rejection", err)
	}
}

func TestValidateIAMEnforceRequiresAsync(t *testing.T) {
	prodWithoutAsync := iamBaseConfig()
	prodWithoutAsync.Env = "prod"
	prodWithoutAsync.Auth = prodJWTConfig()
	prodWithoutAsync.ID = prodIDConfig()
	prodWithoutAsync.Storage.Driver = "s3"
	prodWithoutAsync.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
	prodWithoutAsync.IAM.Cache.Driver = iamconfig.IAMCacheDriverRedis
	prodWithoutAsync.Redis = platformconfig.RedisConfig{Addr: "127.0.0.1:6379"}
	if err := prodWithoutAsync.Validate(); err == nil || !strings.Contains(err.Error(), "async.enabled must be true") {
		t.Fatalf("err=%v want prod async requirement", err)
	}

	prodWithAsync := iamBaseConfig()
	prodWithAsync.Env = "prod"
	prodWithAsync.Auth = prodJWTConfig()
	prodWithAsync.ID = prodIDConfig()
	prodWithAsync.Storage.Driver = "s3"
	prodWithAsync.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
	prodWithAsync.IAM.Cache.Driver = iamconfig.IAMCacheDriverRedis
	prodWithAsync.Redis = platformconfig.RedisConfig{Addr: "127.0.0.1:6379"}
	prodWithAsync.Async.Enabled = true
	prodIdempotencyConfig(prodWithAsync)
	if err := prodWithAsync.Validate(); err != nil {
		t.Fatalf("prod enforce with async: %v", err)
	}
}

func TestValidateIAMRejectsUnsupportedChannel(t *testing.T) {
	cfg := &appconfig.Config{
		Config: platformconfig.Config{
			Env:     "local",
			Service: "c-api",
			Channel: "c",
			Addr:    ":8083",
			Auth:    platformconfig.AuthConfig{Mode: platformconfig.AuthModeDemo, ExpectedChannel: "c"},
			Log:     platformconfig.LogConfig{Level: "info", Format: "json"},
			DB:      platformconfig.DBConfig{Driver: "memory"},
		},
		IAM: iamconfig.IAMConfig{
			Mode:        iamconfig.IAMModeShadow,
			LoadTimeout: 300 * time.Millisecond,
			Cache: iamconfig.IAMCacheConfig{
				Driver: iamconfig.IAMCacheDriverMemory, L1TTL: 3 * time.Second,
				L2TTL: 5 * time.Minute, NegativeTTL: 30 * time.Second,
			},
			Limits: iamconfig.IAMLimitsConfig{
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
	cfg, err := appconfig.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.IAM.Mode != iamconfig.IAMModeOff {
		t.Fatalf("iam.mode = %q, want off", cfg.IAM.Mode)
	}
	if cfg.IAM.LoadTimeout != 300*time.Millisecond {
		t.Fatalf("load_timeout = %s", cfg.IAM.LoadTimeout)
	}
	if cfg.IAM.Cache.Driver != iamconfig.IAMCacheDriverMemory ||
		cfg.IAM.Cache.L1TTL != 3*time.Second ||
		cfg.IAM.Cache.L2TTL != 5*time.Minute ||
		cfg.IAM.Cache.NegativeTTL != 30*time.Second {
		t.Fatalf("cache defaults = %+v", cfg.IAM.Cache)
	}
	if cfg.IAM.Limits.MaxRolesPerSubject != 20 || cfg.IAM.Limits.MaxPermissionsPerRole != 200 {
		t.Fatalf("limit defaults = %+v", cfg.IAM.Limits)
	}
	if cfg.Async.Enabled || cfg.Async.Driver != platformconfig.AsyncDriverDB ||
		cfg.Async.Outbox.PollInterval != time.Second ||
		cfg.Async.Outbox.BatchSize != 100 || cfg.Async.Outbox.MaxAttempts != 10 {
		t.Fatalf("async defaults = %+v", cfg.Async)
	}

	cfg, err = appconfig.LoadWithValues(path, func() (map[string]string, error) {
		return map[string]string{"iam.mode": "shadow"}, nil
	})
	if err != nil {
		t.Fatalf("load with snapshot values: %v", err)
	}
	if cfg.IAM.Mode != iamconfig.IAMModeShadow {
		t.Fatalf("iam.mode = %q, want shadow", cfg.IAM.Mode)
	}
}

func TestValidateIAMBreakglassRules(t *testing.T) {
	enabled := iamBaseConfig()
	enabled.IAM.Breakglass = iamconfig.IAMBreakglassConfig{
		Enabled:         true,
		AllowedSubjects: []string{"1001"},
	}
	if err := enabled.Validate(); err != nil {
		t.Fatalf("breakglass enforce: %v", err)
	}

	shadow := iamBaseConfig()
	shadow.IAM.Mode = iamconfig.IAMModeShadow
	shadow.IAM.Breakglass = iamconfig.IAMBreakglassConfig{Enabled: true}
	if err := shadow.Validate(); err == nil || !strings.Contains(err.Error(), "requires iam.mode=enforce") {
		t.Fatalf("err=%v want breakglass enforce requirement", err)
	}

	biz := iamBaseConfig()
	biz.Channel = "biz"
	biz.Auth.ExpectedChannel = "biz"
	biz.IAM.Breakglass = iamconfig.IAMBreakglassConfig{Enabled: true}
	if err := biz.Validate(); err == nil || !strings.Contains(err.Error(), "channel=admin") {
		t.Fatalf("err=%v want admin-only rejection", err)
	}

	prod := iamBaseConfig()
	prod.Env = "prod"
	prod.Auth = prodJWTConfig()
	prod.Storage.Driver = "s3"
	prod.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
	prod.IAM.Cache.Driver = iamconfig.IAMCacheDriverRedis
	prod.Redis = platformconfig.RedisConfig{Addr: "127.0.0.1:6379"}
	prod.Async.Enabled = true
	prod.IAM.Breakglass = iamconfig.IAMBreakglassConfig{Enabled: true}
	if err := prod.Validate(); err == nil || !strings.Contains(err.Error(), "allowed_subjects") {
		t.Fatalf("err=%v want prod allowlist requirement", err)
	}

	blank := iamBaseConfig()
	blank.IAM.Breakglass = iamconfig.IAMBreakglassConfig{
		Enabled:         true,
		AllowedSubjects: []string{"  "},
	}
	if err := blank.Validate(); err == nil || !strings.Contains(err.Error(), "allowed_subjects[0]") {
		t.Fatalf("err=%v want blank subject rejection", err)
	}
}

func TestValidateIAMApprovalRules(t *testing.T) {
	// local + memory 允许开启审批（待审批单可随进程重启丢失，仅用于开发）。
	local := iamBaseConfig()
	local.IAM.Approval.Enabled = true
	if err := local.Validate(); err != nil {
		t.Fatalf("local approval: %v", err)
	}

	// C 端 / worker 没有 IAM transport，开启审批必须拒绝启动。
	cChannel := iamBaseConfig()
	cChannel.Service = "c-api"
	cChannel.Channel = "c"
	cChannel.Auth.ExpectedChannel = "c"
	cChannel.IAM.Mode = iamconfig.IAMModeOff
	cChannel.IAM.Approval.Enabled = true
	if err := cChannel.Validate(); err == nil || !strings.Contains(err.Error(), "iam.approval.enabled is only supported") {
		t.Fatalf("err=%v want channel rejection", err)
	}

	// prod 下待审批单必须持久化。
	prodMemory := iamBaseConfig()
	prodMemory.Env = "prod"
	prodMemory.Auth = prodJWTConfig()
	prodMemory.ID = prodIDConfig()
	prodMemory.Storage.Driver = "s3"
	prodMemory.IAM.Cache.Driver = iamconfig.IAMCacheDriverRedis
	prodMemory.Redis = platformconfig.RedisConfig{Addr: "127.0.0.1:6379"}
	prodMemory.IAM.Approval.Enabled = true
	if err := prodMemory.Validate(); err == nil || !strings.Contains(err.Error(), "persistent db.driver") {
		t.Fatalf("err=%v want persistent db requirement", err)
	}

	prodSQL := iamBaseConfig()
	prodSQL.Env = "prod"
	prodSQL.Auth = prodJWTConfig()
	prodSQL.ID = prodIDConfig()
	prodSQL.Storage.Driver = "s3"
	prodSQL.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
	prodSQL.IAM.Cache.Driver = iamconfig.IAMCacheDriverRedis
	prodSQL.Redis = platformconfig.RedisConfig{Addr: "127.0.0.1:6379"}
	prodSQL.Async.Enabled = true
	prodSQL.IAM.Approval.Enabled = true
	prodIdempotencyConfig(prodSQL)
	if err := prodSQL.Validate(); err != nil {
		t.Fatalf("prod mysql approval: %v", err)
	}
}

// TestValidateProdHardeningSwitches 验证 P1-3：生产 A/B 端三开关不可降级。
func TestValidateProdHardeningSwitches(t *testing.T) {
	shadow := prodBaseConfig()
	shadow.IAM.Mode = iamconfig.IAMModeShadow
	if err := shadow.Validate(); err == nil || !strings.Contains(err.Error(), "iam.mode must be enforce") {
		t.Fatalf("err=%v want enforce requirement", err)
	}

	noApproval := prodBaseConfig()
	noApproval.IAM.Approval.Enabled = false
	if err := noApproval.Validate(); err == nil || !strings.Contains(err.Error(), "iam.approval.enabled must be true") {
		t.Fatalf("err=%v want approval requirement", err)
	}

	noIdempotency := prodBaseConfig()
	noIdempotency.Idempotency.Enabled = false
	if err := noIdempotency.Validate(); err == nil || !strings.Contains(err.Error(), "idempotency.enabled must be true") {
		t.Fatalf("err=%v want idempotency enable requirement", err)
	}

	optionalKey := prodBaseConfig()
	optionalKey.Idempotency.Required = false
	if err := optionalKey.Validate(); err == nil || !strings.Contains(err.Error(), "idempotency.required must be true") {
		t.Fatalf("err=%v want idempotency required requirement", err)
	}
}

// TestValidateRejectsPublishedSampleSecrets 验证 P1-1：生产拒绝仓库已公开的演练密钥。
func TestValidateRejectsPublishedSampleSecrets(t *testing.T) {
	hashKey := prodBaseConfig()
	hashKey.Identity.HashKey = "7a1ec0f4290851c052cee9fb3b972bc5a8c14297c65e4301bb320b3e6f426454"
	if err := hashKey.Validate(); err == nil || !strings.Contains(err.Error(), "published sample secret") {
		t.Fatalf("err=%v want sample hash key rejection", err)
	}

	dsn := prodBaseConfig()
	dsn.DB.DSN = "root:root@tcp(localhost:3306)/admin_frame?parseTime=true&loc=UTC"
	if err := dsn.Validate(); err == nil || !strings.Contains(err.Error(), "published local rehearsal DSN") {
		t.Fatalf("err=%v want sample DSN rejection", err)
	}

	mfaKey := prodBaseConfig()
	mfaKey.Identity.MFA.SecretKey = "a0c1663bfcdb9f923a4d7171709b6b0d36ab2298053a36f4c26cf7a3b5c17c8a"
	if err := mfaKey.Validate(); err == nil || !strings.Contains(err.Error(), "published sample secret") {
		t.Fatalf("err=%v want sample mfa key rejection", err)
	}

	// 非生产环境不受该限制，本地演练仍可使用样例值。
	local := iamBaseConfig()
	local.Identity.HashKey = "7a1ec0f4290851c052cee9fb3b972bc5a8c14297c65e4301bb320b3e6f426454"
	if err := local.Validate(); err != nil {
		t.Fatalf("local sample secret: %v", err)
	}
}

func TestRedisConfigRedaction(t *testing.T) {
	cfg := platformconfig.RedisConfig{Addr: "127.0.0.1:6379", Password: "p@ss", DB: 1}
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
	if (platformconfig.RedisConfig{}).Enabled() {
		t.Fatal("empty redis must be disabled")
	}
}
