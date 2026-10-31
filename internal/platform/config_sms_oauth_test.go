package platform_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/security"
)

// cJWTConfig 返回 C 端 JWT 校验配置（仅测试用假密钥）。
func cJWTConfig() platform.AuthConfig {
	return platform.AuthConfig{
		Mode:            platform.AuthModeJWT,
		ExpectedChannel: "c",
		JWT: security.JWTConfig{
			Alg:      "HS256",
			Issuer:   "https://iam.example.com",
			Audience: []string{"c"},
			Secret:   "0123456789abcdef0123456789abcdef",
		},
	}
}

// cBaseConfig 返回可通过校验的最小 C 端配置（已启用短信验证码，driver=memory）。
func cBaseConfig() *platform.Config {
	return &platform.Config{
		Env:     "local",
		Service: "c-api",
		Channel: "c",
		Addr:    ":8083",
		Auth:    cJWTConfig(),
		Log:     platform.LogConfig{Level: "info", Format: "json"},
		DB:      platform.DBConfig{Driver: "memory"},
		Storage: platform.StorageConfig{Driver: "memory"},
		HTTP:    platform.HTTPConfig{Timeout: 30 * time.Second},
		Redis:   platform.RedisConfig{Addr: "127.0.0.1:6379"},
		IAM: platform.IAMConfig{
			Mode:        platform.IAMModeOff,
			LoadTimeout: 300 * time.Millisecond,
			Cache: platform.IAMCacheConfig{
				Driver:      platform.IAMCacheDriverMemory,
				L1TTL:       3 * time.Second,
				L2TTL:       5 * time.Minute,
				NegativeTTL: 30 * time.Second,
			},
			Limits: platform.IAMLimitsConfig{
				MaxRolesPerSubject:    20,
				MaxPermissionsPerRole: 200,
				MaxBindingsPerSubject: 50,
			},
		},
		Identity: platform.IdentityConfig{
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
			MFA:            platform.IdentityMFAConfig{Issuer: "AdminFrame"},
			SMS: platform.IdentitySMSConfig{
				Enabled:        true,
				Driver:         platform.IdentitySMSDriverMemory,
				TemplateID:     "SMS_123456",
				CodeTTL:        5 * time.Minute,
				MaxAttempts:    5,
				ResendInterval: time.Minute,
				DailyPerPhone:  10,
				HourlyPerIP:    30,
			},
		},
	}
}

// cProdConfig 返回满足生产约束的 C 端配置（短信使用 aliyun 驱动）。
func cProdConfig() *platform.Config {
	cfg := cBaseConfig()
	cfg.Env = "prod"
	cfg.Auth = cJWTConfig()
	cfg.ID = prodIDConfig()
	cfg.Storage.Driver = "s3"
	cfg.DB = platform.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
	cfg.Async.Enabled = true
	cfg.Identity.SMS.Driver = platform.IdentitySMSDriverAliyun
	cfg.Identity.SMS.DailyBudget = 1000
	cfg.Identity.HashKey = "01234567890123456789012345678901"
	cfg.Identity.HashKeyID = "v1"
	return cfg
}

// oauthProvider 返回满足校验的最小 OAuth 提供方配置。
func oauthProvider(name string) platform.IdentityOAuthProviderConfig {
	return platform.IdentityOAuthProviderConfig{ //nolint:gosec // G101: 测试用假凭证与环境变量名
		Name:            name,
		ClientID:        "client-id",
		ClientSecretEnv: "OAUTH_CLIENT_SECRET",
		AuthURL:         "https://idp.example.com/oauth/authorize",
		TokenURL:        "https://idp.example.com/oauth/token",
		UserInfoURL:     "https://idp.example.com/oauth/userinfo",
		RedirectURI:     "https://c.example.com/c/v1/auth/oauth/" + name + "/callback",
		Scopes:          []string{"openid", "profile"},
	}
}

// TestValidateCChannelBaseline 验证 C 端本地与生产基线配置可通过对齐校验。
func TestValidateCChannelBaseline(t *testing.T) {
	if err := cBaseConfig().Validate(); err != nil {
		t.Fatalf("baseline local c config: %v", err)
	}
	if err := cProdConfig().Validate(); err != nil {
		t.Fatalf("baseline prod c config: %v", err)
	}

	oauthOnly := cProdConfig()
	oauthOnly.Identity.SMS.Enabled = false
	oauthOnly.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{oauthProvider("github")}
	if err := oauthOnly.Validate(); err != nil {
		t.Fatalf("prod c config with oauth only: %v", err)
	}
}

// TestValidateSMSRules 表驱动覆盖短信参数校验。
func TestValidateSMSRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(cfg *platform.Config)
		want   string
	}{
		{
			name: "enabled on admin channel",
			mutate: func(cfg *platform.Config) {
				cfg.Channel = "admin"
				cfg.Auth.ExpectedChannel = "admin"
				cfg.IAM.Mode = platform.IAMModeEnforce
			},
			want: "identity.sms.enabled is only supported on channel=c",
		},
		{
			name:   "unsupported driver",
			mutate: func(cfg *platform.Config) { cfg.Identity.SMS.Driver = "twilio" },
			want:   "unsupported identity.sms.driver",
		},
		{
			name:   "missing template id",
			mutate: func(cfg *platform.Config) { cfg.Identity.SMS.TemplateID = "  " },
			want:   "identity.sms.template_id is required",
		},
		{
			name:   "code ttl too long",
			mutate: func(cfg *platform.Config) { cfg.Identity.SMS.CodeTTL = 16 * time.Minute },
			want:   "identity.sms.code_ttl must be in (0, 15m]",
		},
		{
			name:   "max attempts out of range",
			mutate: func(cfg *platform.Config) { cfg.Identity.SMS.MaxAttempts = 11 },
			want:   "identity.sms.max_attempts must be in [1, 10]",
		},
		{
			name:   "resend interval exceeds ttl",
			mutate: func(cfg *platform.Config) { cfg.Identity.SMS.ResendInterval = 6 * time.Minute },
			want:   "identity.sms.resend_interval must be in (0, identity.sms.code_ttl]",
		},
		{
			name:   "daily limit invalid",
			mutate: func(cfg *platform.Config) { cfg.Identity.SMS.DailyPerPhone = 0 },
			want:   "identity.sms.daily_per_phone must be >= 1",
		},
		{
			name:   "hourly ip limit invalid",
			mutate: func(cfg *platform.Config) { cfg.Identity.SMS.HourlyPerIP = 0 },
			want:   "identity.sms.hourly_per_ip must be >= 1",
		},
		{
			name:   "negative breaker failures",
			mutate: func(cfg *platform.Config) { cfg.Identity.SMS.BreakerFailures = -1 },
			want:   "identity.sms.breaker_failures must be >= 0",
		},
		{
			name:   "breaker timeout too short",
			mutate: func(cfg *platform.Config) { cfg.Identity.SMS.BreakerTimeout = 500 * time.Millisecond },
			want:   "identity.sms.breaker_timeout must be >= 1s",
		},
		{
			name: "memory driver in prod",
			mutate: func(cfg *platform.Config) {
				cfg.Env = "prod"
				cfg.Auth = cJWTConfig()
				cfg.ID = prodIDConfig()
				cfg.Storage.Driver = "s3"
				cfg.DB = platform.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
				cfg.Identity.SMS.Driver = platform.IdentitySMSDriverMemory
			},
			want: "identity.sms.driver must not be memory in prod/staging",
		},
		{
			name: "missing daily budget in prod",
			mutate: func(cfg *platform.Config) {
				cfg.Env = "prod"
				cfg.Auth = cJWTConfig()
				cfg.ID = prodIDConfig()
				cfg.Storage.Driver = "s3"
				cfg.DB = platform.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
				cfg.Identity.SMS.Driver = platform.IdentitySMSDriverAliyun
				cfg.Identity.HashKey = "01234567890123456789012345678901"
				cfg.Identity.SMS.DailyBudget = 0
			},
			want: "identity.sms.daily_budget must be > 0 in prod/staging",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cBaseConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

// TestValidateOAuthRules 表驱动覆盖 OAuth 提供方参数校验。
func TestValidateOAuthRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(cfg *platform.Config)
		want   string
	}{
		{
			name: "providers on admin channel",
			mutate: func(cfg *platform.Config) {
				cfg.Channel = "admin"
				cfg.Auth.ExpectedChannel = "admin"
				cfg.IAM.Mode = platform.IAMModeEnforce
				cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{oauthProvider("github")}
			},
			want: "identity.oauth.providers is only supported on channel=c",
		},
		{
			name: "prod c without sms or oauth",
			mutate: func(cfg *platform.Config) {
				cfg.Env = "prod"
				cfg.Auth = cJWTConfig()
				cfg.ID = prodIDConfig()
				cfg.Storage.Driver = "s3"
				cfg.DB = platform.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
				cfg.Identity.SMS.Enabled = false
			},
			want: "channel=c requires identity.sms.enabled or at least one identity.oauth.providers",
		},
		{
			name: "invalid provider name",
			mutate: func(cfg *platform.Config) {
				provider := oauthProvider("GitHub")
				cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].name must match",
		},
		{
			name: "duplicate provider name",
			mutate: func(cfg *platform.Config) {
				cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{
					oauthProvider("github"),
					oauthProvider("github"),
				}
			},
			want: `identity.oauth.providers[1].name "github" is duplicated`,
		},
		{
			name: "missing client id",
			mutate: func(cfg *platform.Config) {
				provider := oauthProvider("github")
				provider.ClientID = " "
				cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].client_id is required",
		},
		{
			name: "missing client secret env",
			mutate: func(cfg *platform.Config) {
				provider := oauthProvider("github")
				provider.ClientSecretEnv = ""
				cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].client_secret_env is required",
		},
		{
			name: "missing redirect uri",
			mutate: func(cfg *platform.Config) {
				provider := oauthProvider("github")
				provider.RedirectURI = ""
				cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].redirect_uri is required",
		},
		{
			name: "relative auth url",
			mutate: func(cfg *platform.Config) {
				provider := oauthProvider("github")
				provider.AuthURL = "/oauth/authorize"
				cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].auth_url",
		},
		{
			name: "negative oauth rps",
			mutate: func(cfg *platform.Config) {
				cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{oauthProvider("github")}
				cfg.Identity.OAuth.RateLimit.RPS = -1
			},
			want: "identity.oauth.rate_limit.rps must be >= 0",
		},
		{
			name: "oauth fail_open in prod",
			mutate: func(cfg *platform.Config) {
				cfg.Env = "prod"
				cfg.Auth = cJWTConfig()
				cfg.ID = prodIDConfig()
				cfg.Storage.Driver = "s3"
				cfg.DB = platform.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
				cfg.Identity.SMS.Driver = platform.IdentitySMSDriverAliyun
				cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{oauthProvider("github")}
				cfg.Identity.OAuth.RateLimit.FailureMode = platform.RateLimitFailureOpen
			},
			want: "identity.oauth.rate_limit.failure_mode must be fail_closed",
		},
		{
			name: "http url in prod",
			mutate: func(cfg *platform.Config) {
				cfg.Env = "prod"
				cfg.Auth = cJWTConfig()
				cfg.ID = prodIDConfig()
				cfg.Storage.Driver = "s3"
				cfg.DB = platform.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
				cfg.Identity.SMS.Driver = platform.IdentitySMSDriverAliyun
				provider := oauthProvider("github")
				provider.TokenURL = "http://idp.example.com/oauth/token"
				cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].token_url",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cBaseConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

// TestConfigLogValueHidesOAuthSecrets 验证日志快照只输出提供方名称，不泄露密钥。
func TestConfigLogValueHidesOAuthSecrets(t *testing.T) {
	cfg := cBaseConfig()
	cfg.Identity.OAuth.Providers = []platform.IdentityOAuthProviderConfig{oauthProvider("github")}
	cfg.Identity.OAuth.Providers[0].ClientSecretEnv = "OAUTH_SECRET_ENV_NAME"
	secret := "super-secret-value"
	t.Setenv("OAUTH_SECRET_ENV_NAME", secret)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("config", "config", cfg)

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("log output leaks oauth client secret: %s", out)
	}
	if !strings.Contains(out, "provider_names") || !strings.Contains(out, "github") {
		t.Fatalf("expected provider names in log output: %s", out)
	}
}
