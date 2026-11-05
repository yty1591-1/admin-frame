package platform_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// mfaRawKey 是 32 字节 TOTP 主密钥（仅测试用假密钥）。
const mfaRawKey = "0123456789abcdef0123456789abcdef"

// mfaBaseConfig 返回启用身份域且 MFA 配置合法的本地配置。
func mfaBaseConfig() *platform.Config {
	cfg := iamBaseConfig()
	cfg.Auth = prodJWTConfig()
	cfg.Redis = platform.RedisConfig{Addr: "127.0.0.1:6379"}
	cfg.Identity = platform.IdentityConfig{
		Enabled:    true,
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 168 * time.Hour,
		Session: platform.IdentitySessionConfig{
			IdleTimeout:   72 * time.Hour,
			MaxPerAccount: 10,
		},
		Retention: platform.IdentityRetentionConfig{
			SessionDays:      30,
			LoginAttemptDays: 180,
			CleanupInterval:  time.Hour,
		},
		Revocation: platform.IdentityRevocationConfig{
			Driver:      platform.IdentityRevocationDriverRedis,
			FailureMode: platform.RateLimitFailureClosed,
		},
		Lockout: platform.IdentityLockoutConfig{
			MaxFailures: 5,
			Window:      15 * time.Minute,
			Duration:    15 * time.Minute,
		},
		LoginRateLimit: platform.IdentityLoginRateLimitConfig{RPS: 1, Burst: 5},
		HashKey:        mfaRawKey,
		HashKeyID:      "v1",
		MFA: platform.IdentityMFAConfig{
			AdminRequired: true,
			Issuer:        "AdminFrame",
			SecretKey:     mfaRawKey,
		},
	}
	return cfg
}

// mfaProdConfig 返回满足生产约束的 MFA 配置。
func mfaProdConfig() *platform.Config {
	cfg := mfaBaseConfig()
	cfg.Env = "prod"
	cfg.ID = prodIDConfig()
	cfg.Storage.Driver = "s3"
	cfg.DB = platform.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
	cfg.IAM.Cache.Driver = platform.IAMCacheDriverRedis
	cfg.Async.Enabled = true
	prodIdempotencyConfig(cfg)
	return cfg
}

// TestDecodeMFASecretKey 表驱动覆盖 raw / hex / base64 / base64url 与非法输入。
func TestDecodeMFASecretKey(t *testing.T) {
	raw := []byte(mfaRawKey)
	cases := []struct {
		name    string
		value   string
		wantNil bool
		wantErr bool
	}{
		{name: "empty", value: " ", wantNil: true},
		{name: "raw", value: mfaRawKey},
		{name: "hex", value: hex.EncodeToString(raw)},
		{name: "base64", value: base64.StdEncoding.EncodeToString(raw)},
		{name: "base64url", value: base64.RawURLEncoding.EncodeToString(raw)},
		{name: "too short", value: "short-key", wantErr: true},
		{name: "wrong hex", value: strings.Repeat("zz", 32), wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := platform.IdentityMFAConfig{SecretKey: tc.value}.DecodeSecretKey()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "32-byte key") {
					t.Fatalf("err = %v, want 32-byte key rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if tc.wantNil {
				if key != nil {
					t.Fatalf("key = %x, want nil", key)
				}
				return
			}
			if string(key) != mfaRawKey {
				t.Fatalf("key = %x, want %x", key, raw)
			}
		})
	}
}

// TestValidateMFARequiresAdminMFAInProduction 验证生产必须强制 A 端 MFA。
func TestValidateMFARequiresAdminMFAInProduction(t *testing.T) {
	prod := mfaProdConfig()
	if err := prod.Validate(); err != nil {
		t.Fatalf("baseline prod mfa config: %v", err)
	}

	disabled := mfaProdConfig()
	disabled.Identity.MFA.AdminRequired = false
	if err := disabled.Validate(); err == nil || !strings.Contains(err.Error(), "identity.mfa.admin_required must be true") {
		t.Fatalf("err=%v want prod admin_required rejection", err)
	}
}

// mfaBizConfig 返回启用身份域的 B 端配置（MFA 不参与）。
func mfaBizConfig() *platform.Config {
	cfg := mfaBaseConfig()
	cfg.Channel = "biz"
	cfg.Auth = prodJWTConfig()
	cfg.Auth.ExpectedChannel = "biz"
	cfg.Auth.JWT.Audience = []string{"biz"}
	cfg.Identity.MFA.AdminRequired = false
	cfg.Identity.MFA.SecretKey = ""
	return cfg
}

// TestValidateMFAAdminOnly 验证 B 端禁止强制 MFA，且不设置即可通过校验。
func TestValidateMFAAdminOnly(t *testing.T) {
	biz := mfaBizConfig()
	if err := biz.Validate(); err != nil {
		t.Fatalf("biz baseline without mfa: %v", err)
	}

	// B 端显式设置 admin_required=true 必须启动失败，避免「配置有效但运行不校验」。
	invalid := mfaBizConfig()
	invalid.Identity.MFA.AdminRequired = true
	invalid.Identity.MFA.SecretKey = mfaRawKey
	if err := invalid.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.mfa.admin_required must be false on channel=biz") {
		t.Fatalf("err=%v want biz admin_required rejection", err)
	}

	// 生产 B 端同理（不因 env 放宽）。
	invalidProd := mfaBizConfig()
	invalidProd.Env = "prod"
	invalidProd.ID = prodIDConfig()
	invalidProd.Storage.Driver = "s3"
	invalidProd.DB = platform.DBConfig{Driver: "mysql", DSN: "biz:biz@tcp(127.0.0.1:3306)/biz"}
	invalidProd.Identity.MFA.AdminRequired = true
	invalidProd.Identity.MFA.SecretKey = mfaRawKey
	if err := invalidProd.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.mfa.admin_required must be false on channel=biz") {
		t.Fatalf("err=%v want prod biz admin_required rejection", err)
	}
}

// TestValidateHashKey 验证 PII 摘要主密钥生产必填、长度与 key id 约束。
func TestValidateHashKey(t *testing.T) {
	missing := mfaProdConfig()
	missing.Identity.HashKey = ""
	if err := missing.Validate(); err == nil || !strings.Contains(err.Error(), "identity.hash_key is required in prod/staging") {
		t.Fatalf("err=%v want prod hash_key rejection", err)
	}

	invalid := mfaBizConfig()
	invalid.Identity.HashKey = "not-a-32-byte-key"
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "32-byte key") {
		t.Fatalf("err=%v want key length rejection", err)
	}

	badID := mfaBizConfig()
	badID.Identity.HashKey = mfaRawKey
	badID.Identity.HashKeyID = "bad:id"
	if err := badID.Validate(); err == nil || !strings.Contains(err.Error(), "identity.hash_key_id is invalid") {
		t.Fatalf("err=%v want key id rejection", err)
	}

	previousWithoutActive := mfaBizConfig()
	previousWithoutActive.Identity.HashKey = ""
	previousWithoutActive.Identity.PreviousHashKeys = []platform.IdentityHashKeyConfig{
		{ID: "v0", Key: mfaRawKey},
	}
	if err := previousWithoutActive.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.previous_hash_keys requires identity.hash_key") {
		t.Fatalf("err=%v want previous keys rejection", err)
	}

	valid := mfaBizConfig()
	valid.Identity.HashKey = mfaRawKey
	valid.Identity.HashKeyID = "v2"
	valid.Identity.PreviousHashKeys = []platform.IdentityHashKeyConfig{{ID: "v1", Key: mfaRawKey}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid hash keys rejected: %v", err)
	}
}

// TestValidateSessionAbsoluteTTL 验证设备绝对生命上限必须大于 refresh_ttl。
func TestValidateSessionAbsoluteTTL(t *testing.T) {
	base := mfaBizConfig()
	if err := base.Validate(); err != nil {
		t.Fatalf("baseline session config: %v", err)
	}

	invalid := mfaBizConfig()
	invalid.Identity.Session.AbsoluteTTL = invalid.Identity.RefreshTTL
	if err := invalid.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.session.absolute_ttl must be greater than") {
		t.Fatalf("err=%v want absolute_ttl rejection", err)
	}

	valid := mfaBizConfig()
	valid.Identity.Session.AbsoluteTTL = 720 * time.Hour
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid absolute_ttl rejected: %v", err)
	}
}

// TestValidateTrustedProxiesAndLoginRateLimit 验证可信代理 CIDR 与登录限流故障策略。
func TestValidateTrustedProxiesAndLoginRateLimit(t *testing.T) {
	invalidProxy := mfaBizConfig()
	invalidProxy.HTTP.TrustedProxies = []string{"not-a-cidr"}
	if err := invalidProxy.Validate(); err == nil ||
		!strings.Contains(err.Error(), "http.trusted_proxies must be valid CIDR") {
		t.Fatalf("err=%v want trusted_proxies rejection", err)
	}

	validProxy := mfaBizConfig()
	validProxy.HTTP.TrustedProxies = []string{"10.0.0.0/8", "fd00::/8"}
	if err := validProxy.Validate(); err != nil {
		t.Fatalf("valid trusted_proxies rejected: %v", err)
	}

	prodOpen := mfaProdConfig()
	prodOpen.Identity.LoginRateLimit.FailureMode = platform.RateLimitFailureOpen
	if err := prodOpen.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.login_rate_limit.failure_mode must be fail_closed") {
		t.Fatalf("err=%v want prod fail_open rejection", err)
	}

	localOpen := mfaBizConfig()
	localOpen.Identity.LoginRateLimit.FailureMode = platform.RateLimitFailureOpen
	if err := localOpen.Validate(); err != nil {
		t.Fatalf("local fail_open rejected: %v", err)
	}

	badMode := mfaBizConfig()
	badMode.Identity.LoginRateLimit.FailureMode = "unknown"
	if err := badMode.Validate(); err == nil ||
		!strings.Contains(err.Error(), "unsupported identity.login_rate_limit.failure_mode") {
		t.Fatalf("err=%v want enum rejection", err)
	}
}

// TestValidateMFARequiresSecretKey 验证 admin_required=true 时必须配置可用密钥。
func TestValidateMFARequiresSecretKey(t *testing.T) {
	missing := mfaBaseConfig()
	missing.Identity.MFA.SecretKey = ""
	if err := missing.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.mfa.secret_key or identity.mfa.keys is required") {
		t.Fatalf("err=%v want missing secret rejection", err)
	}

	invalid := mfaBaseConfig()
	invalid.Identity.MFA.SecretKey = "not-a-32-byte-key"
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "32-byte key") {
		t.Fatalf("err=%v want invalid secret rejection", err)
	}

	// 非强制绑定时可以不配置密钥（本地调试）。
	optional := mfaBaseConfig()
	optional.Identity.MFA.AdminRequired = false
	optional.Identity.MFA.SecretKey = ""
	if err := optional.Validate(); err != nil {
		t.Fatalf("optional mfa: %v", err)
	}
}

// TestValidateMFAIssuerRequired 验证 otpauth issuer 不得为空。
func TestValidateMFAIssuerRequired(t *testing.T) {
	cfg := mfaBaseConfig()
	cfg.Identity.MFA.Issuer = "  "
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "identity.mfa.issuer must not be empty") {
		t.Fatalf("err=%v want issuer rejection", err)
	}
}

// TestValidateMFATOTPAndTemporaryPassword 验证 TOTP / 恢复码 / 临时口令边界（MNT-05）。
func TestValidateMFATOTPAndTemporaryPassword(t *testing.T) {
	valid := mfaBaseConfig()
	valid.Identity.MFA.TOTPDigits = 8
	valid.Identity.MFA.TOTPStep = 60 * time.Second
	valid.Identity.MFA.TOTPWindow = 2
	valid.Identity.MFA.RecoveryCodeCount = 20
	valid.Identity.Password.TemporaryLength = 24
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*platform.Config)
		want   string
	}{
		{"digits", func(c *platform.Config) { c.Identity.MFA.TOTPDigits = 7 }, "totp_digits must be 6 or 8"},
		{"step", func(c *platform.Config) { c.Identity.MFA.TOTPStep = 10 * time.Second }, "totp_step must be in [15s, 120s]"},
		{"window", func(c *platform.Config) { c.Identity.MFA.TOTPWindow = 3 }, "totp_window must be in [0, 2]"},
		{"recovery", func(c *platform.Config) { c.Identity.MFA.RecoveryCodeCount = 4 }, "recovery_code_count must be in [5, 20]"},
		{"temporary", func(c *platform.Config) { c.Identity.Password.TemporaryLength = 12 }, "temporary_length must be in [16, 32]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mfaBaseConfig()
			tc.mutate(cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

// TestValidateMFAFactors 验证因子白名单只接受已实现的 totp（BIZ-03）。
func TestValidateMFAFactors(t *testing.T) {
	explicit := mfaBaseConfig()
	explicit.Identity.MFA.Factors = []string{" TOTP "}
	if err := explicit.Validate(); err != nil {
		t.Fatalf("explicit totp rejected: %v", err)
	}

	// 空配置按默认 totp 处理，不影响既有部署。
	implicit := mfaBaseConfig()
	if err := implicit.Validate(); err != nil {
		t.Fatalf("empty factors rejected: %v", err)
	}

	unknown := mfaBaseConfig()
	unknown.Identity.MFA.Factors = []string{"webauthn"}
	if err := unknown.Validate(); err == nil ||
		!strings.Contains(err.Error(), "unsupported identity.mfa.factors entry: webauthn") {
		t.Fatalf("err=%v want unsupported factor rejection", err)
	}

	duplicate := mfaBaseConfig()
	duplicate.Identity.MFA.Factors = []string{"totp", "TOTP"}
	if err := duplicate.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.mfa.factors has duplicate entry: totp") {
		t.Fatalf("err=%v want duplicate factor rejection", err)
	}
}

// TestValidateRevocationTokenGraceAndConsumerWindow 验证 jti 宽限与注销再认证窗口边界（8.4）。
func TestValidateRevocationTokenGraceAndConsumerWindow(t *testing.T) {
	valid := mfaBaseConfig()
	valid.Identity.Revocation.TokenGrace = 60 * time.Second
	valid.Identity.Consumer.DeleteReauthWindow = 5 * time.Minute
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid windows rejected: %v", err)
	}

	// 负数表示显式关闭 jti 宽限，允许通过。
	disabled := mfaBaseConfig()
	disabled.Identity.Revocation.TokenGrace = -time.Second
	if err := disabled.Validate(); err != nil {
		t.Fatalf("negative token grace must disable: %v", err)
	}

	tooLongGrace := mfaBaseConfig()
	tooLongGrace.Identity.Revocation.TokenGrace = 90 * time.Second
	if err := tooLongGrace.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.revocation.token_grace must be <= 60s") {
		t.Fatalf("err=%v want token_grace upper bound rejection", err)
	}

	tooLongWindow := mfaBaseConfig()
	tooLongWindow.Identity.Consumer.DeleteReauthWindow = 45 * time.Minute
	if err := tooLongWindow.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.consumer.delete_reauth_window must be in [1m, 30m]") {
		t.Fatalf("err=%v want delete window upper bound rejection", err)
	}

	tooShortWindow := mfaBaseConfig()
	tooShortWindow.Identity.Consumer.DeleteReauthWindow = 30 * time.Second
	if err := tooShortWindow.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.consumer.delete_reauth_window must be in [1m, 30m]") {
		t.Fatalf("err=%v want delete window lower bound rejection", err)
	}
}

// TestValidateTenantPasswordOverrides 验证租户策略只能收紧（BIZ-04）。
func TestValidateTenantPasswordOverrides(t *testing.T) {
	add := func(cfg *platform.Config, override platform.IdentityPasswordTenantOverride) {
		cfg.Identity.Password.TenantOverrides = append(cfg.Identity.Password.TenantOverrides, override)
	}
	valid := mfaBaseConfig()
	add(valid, platform.IdentityPasswordTenantOverride{MerchantID: "ACCT-1", MinLength: 16, MaxLength: 64})
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid override: %v", err)
	}
	relaxed := mfaBaseConfig()
	add(relaxed, platform.IdentityPasswordTenantOverride{MerchantID: "acct-1", MinLength: 8})
	if err := relaxed.Validate(); err == nil || !strings.Contains(err.Error(), "min_length must be >= global") {
		t.Fatalf("relaxed min error = %v", err)
	}
	tooLong := mfaBaseConfig()
	add(tooLong, platform.IdentityPasswordTenantOverride{MerchantID: "acct-1", MaxLength: 256})
	if err := tooLong.Validate(); err == nil || !strings.Contains(err.Error(), "max_length must be within") {
		t.Fatalf("too long max error = %v", err)
	}
	dup := mfaBaseConfig()
	add(dup, platform.IdentityPasswordTenantOverride{MerchantID: "acct-1", MinLength: 16})
	add(dup, platform.IdentityPasswordTenantOverride{MerchantID: "ACCT-1", MinLength: 16})
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate merchant_id") {
		t.Fatalf("duplicate error = %v", err)
	}
}

// TestValidateRevocationStateTTL 验证撤销快照陈旧度必须落在 [1s, 60s]。
func TestValidateRevocationStateTTL(t *testing.T) {
	tooLong := mfaProdConfig()
	tooLong.Identity.Revocation.StateTTL = 90 * time.Second
	if err := tooLong.Validate(); err == nil || !strings.Contains(err.Error(), "state_ttl must be <= 60s") {
		t.Fatalf("err=%v want state_ttl upper bound rejection", err)
	}

	tooShort := mfaBaseConfig()
	tooShort.Identity.Revocation.StateTTL = 500 * time.Millisecond
	if err := tooShort.Validate(); err == nil || !strings.Contains(err.Error(), "state_ttl must be >= 1s") {
		t.Fatalf("err=%v want state_ttl lower bound rejection", err)
	}

	valid := mfaProdConfig()
	valid.Identity.Revocation.StateTTL = 30 * time.Second
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid state_ttl: %v", err)
	}
}

// TestConfigLogValueHidesMFASecretKey 验证日志快照只暴露是否配置密钥。
func TestConfigLogValueHidesMFASecretKey(t *testing.T) {
	cfg := mfaBaseConfig()
	secret := strings.Repeat("ab", 32) // hex 编码的 32 字节密钥
	cfg.Identity.MFA.SecretKey = secret

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("config", "config", cfg)

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("log output leaks mfa secret: %s", out)
	}
	if !strings.Contains(out, "secret_key_configured") {
		t.Fatalf("expected secret_key_configured flag: %s", out)
	}
}

// TestValidateIdentityRequiresPersistentDB 验证生产身份域禁止内存仓储（IDN-P2-01）。
func TestValidateIdentityRequiresPersistentDB(t *testing.T) {
	memoryDB := mfaProdConfig()
	memoryDB.DB = platform.DBConfig{Driver: "memory"}
	if err := memoryDB.Validate(); err == nil ||
		!strings.Contains(err.Error(), "db.driver must not be memory when identity.enabled=true in prod/staging") {
		t.Fatalf("err=%v want prod memory db rejection", err)
	}

	local := mfaBaseConfig()
	if err := local.Validate(); err != nil {
		t.Fatalf("local memory db rejected: %v", err)
	}
}

// TestValidateIdentityRequiresAsync 验证生产身份域必须启用审计 Outbox（IDN-P2-01）。
func TestValidateIdentityRequiresAsync(t *testing.T) {
	noAsync := mfaProdConfig()
	noAsync.Async.Enabled = false
	if err := noAsync.Validate(); err == nil ||
		!strings.Contains(err.Error(), "async.enabled must be true when identity.enabled=true") {
		t.Fatalf("err=%v want prod async rejection", err)
	}

	local := mfaBaseConfig()
	local.Async.Enabled = false
	if err := local.Validate(); err != nil {
		t.Fatalf("local without async rejected: %v", err)
	}
}

// TestValidateIdentityPassword 验证密码策略范围（IDN-P2-01）。
func TestValidateIdentityPassword(t *testing.T) {
	tooShort := mfaBizConfig()
	tooShort.Identity.Password.MinLength = 6
	if err := tooShort.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.password.min_length must be >= 8") {
		t.Fatalf("err=%v want min_length rejection", err)
	}

	reversed := mfaBizConfig()
	reversed.Identity.Password.MinLength = 16
	reversed.Identity.Password.MaxLength = 12
	if err := reversed.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.password.max_length must be >= identity.password.min_length") {
		t.Fatalf("err=%v want max_length ordering rejection", err)
	}

	tooLong := mfaBizConfig()
	tooLong.Identity.Password.MaxLength = 5000
	if err := tooLong.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.password.max_length must be <= 4096") {
		t.Fatalf("err=%v want max_length upper bound rejection", err)
	}

	valid := mfaBizConfig()
	valid.Identity.Password.MinLength = 10
	valid.Identity.Password.MaxLength = 64
	requireComplexity := false
	valid.Identity.Password.RequireComplexity = &requireComplexity
	valid.Identity.Password.MaxConcurrent = 4
	valid.Identity.Password.WaitTimeout = time.Second
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid password policy rejected: %v", err)
	}

	badConcurrency := mfaBizConfig()
	badConcurrency.Identity.Password.MaxConcurrent = -1
	if err := badConcurrency.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.password.max_concurrent must be >= 0") {
		t.Fatalf("err=%v want max_concurrent rejection", err)
	}

	shortWait := mfaBizConfig()
	shortWait.Identity.Password.WaitTimeout = 10 * time.Millisecond
	if err := shortWait.Validate(); err == nil ||
		!strings.Contains(err.Error(), "identity.password.wait_timeout must be >= 100ms") {
		t.Fatalf("err=%v want wait_timeout lower bound rejection", err)
	}
}
