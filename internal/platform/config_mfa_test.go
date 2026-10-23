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

// TestValidateMFARequiresSecretKey 验证 admin_required=true 时必须配置可用密钥。
func TestValidateMFARequiresSecretKey(t *testing.T) {
	missing := mfaBaseConfig()
	missing.Identity.MFA.SecretKey = ""
	if err := missing.Validate(); err == nil || !strings.Contains(err.Error(), "identity.mfa.secret_key is required") {
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
