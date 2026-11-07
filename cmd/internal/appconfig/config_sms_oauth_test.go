package appconfig_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"
	"github.com/admin-frame/admin-frame/internal/security"
)

// cJWTConfig 返回 C 端 JWT 校验配置（仅测试用假密钥）。
func cJWTConfig() platformconfig.AuthConfig {
	return platformconfig.AuthConfig{
		Mode:            platformconfig.AuthModeJWT,
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
func cBaseConfig() *appconfig.Config {
	return &appconfig.Config{
		Config: platformconfig.Config{
			Env:     "local",
			Service: "c-api",
			Channel: "c",
			Addr:    ":8083",
			Auth:    cJWTConfig(),
			Log:     platformconfig.LogConfig{Level: "info", Format: "json"},
			DB:      platformconfig.DBConfig{Driver: "memory"},
			Storage: platformstorage.Config{Driver: "memory"},
			HTTP:    platformconfig.HTTPConfig{Timeout: 30 * time.Second},
			Redis:   platformconfig.RedisConfig{Addr: "127.0.0.1:6379"},
		},
		IAM: iamconfig.IAMConfig{
			Mode:        iamconfig.IAMModeOff,
			LoadTimeout: 300 * time.Millisecond,
			Cache: iamconfig.IAMCacheConfig{
				Driver:      iamconfig.IAMCacheDriverMemory,
				L1TTL:       3 * time.Second,
				L2TTL:       5 * time.Minute,
				NegativeTTL: 30 * time.Second,
			},
			Limits: iamconfig.IAMLimitsConfig{
				MaxRolesPerSubject:    20,
				MaxPermissionsPerRole: 200,
				MaxBindingsPerSubject: 50,
			},
		},
		Identity: identityconfig.IdentityConfig{
			Enabled:    true,
			AccessTTL:  15 * time.Minute,
			RefreshTTL: 168 * time.Hour,
			Session: identityconfig.IdentitySessionConfig{
				IdleTimeout:   72 * time.Hour,
				MaxPerAccount: 10,
			},
			Retention: identityconfig.IdentityRetentionConfig{
				SessionDays:      30,
				LoginAttemptDays: 180,
				CleanupInterval:  time.Hour,
			},
			Revocation: identityconfig.IdentityRevocationConfig{
				Driver:      identityconfig.IdentityRevocationDriverRedis,
				FailureMode: platformconfig.RateLimitFailureClosed,
			},
			Lockout: identityconfig.IdentityLockoutConfig{
				MaxFailures: 5,
				Window:      15 * time.Minute,
				Duration:    15 * time.Minute,
			},
			LoginRateLimit: identityconfig.IdentityLoginRateLimitConfig{RPS: 1, Burst: 5},
			MFA:            identityconfig.IdentityMFAConfig{Issuer: "AdminFrame"},
			SMS: identityconfig.IdentitySMSConfig{
				Enabled:        true,
				Driver:         identityconfig.IdentitySMSDriverMemory,
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
func cProdConfig() *appconfig.Config {
	cfg := cBaseConfig()
	cfg.Env = "prod"
	cfg.Auth = cJWTConfig()
	cfg.ID = prodIDConfig()
	cfg.Storage.Driver = "s3"
	cfg.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
	cfg.Async.Enabled = true
	cfg.Identity.SMS.Driver = identityconfig.IdentitySMSDriverAliyun
	cfg.Identity.SMS.DailyBudget = 1000
	cfg.Identity.HashKey = "01234567890123456789012345678901"
	cfg.Identity.HashKeyID = "v1"
	return cfg
}

// oauthProvider 返回满足校验的最小 OAuth 提供方配置。
func oauthProvider(name string) identityconfig.IdentityOAuthProviderConfig {
	return identityconfig.IdentityOAuthProviderConfig{ //nolint:gosec // G101: 测试用假凭证与环境变量名
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

// oauthOIDCProvider 返回满足 OIDC 校验的最小提供方配置（显式开启 oidc_enabled）。
func oauthOIDCProvider(name string) identityconfig.IdentityOAuthProviderConfig {
	provider := oauthProvider(name)
	provider.UserInfoURL = ""
	provider.OIDCEnabled = true
	provider.Issuer = "https://idp.example.com"
	provider.JWKSURL = "https://idp.example.com/oidc/jwks"
	provider.JWKSTTL = 15 * time.Minute
	provider.JWKSMinRefreshInterval = time.Minute
	provider.IDTokenLeeway = 30 * time.Second
	return provider
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
	oauthOnly.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{oauthProvider("github")}
	if err := oauthOnly.Validate(); err != nil {
		t.Fatalf("prod c config with oauth only: %v", err)
	}

	oidcOnly := cProdConfig()
	oidcOnly.Identity.SMS.Enabled = false
	oidcOnly.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{oauthOIDCProvider("keycloak")}
	if err := oidcOnly.Validate(); err != nil {
		t.Fatalf("prod c config with oidc only: %v", err)
	}
}

// TestValidateSMSRules 表驱动覆盖短信参数校验。
func TestValidateSMSRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(cfg *appconfig.Config)
		want   string
	}{
		{
			name: "enabled on admin channel",
			mutate: func(cfg *appconfig.Config) {
				cfg.Channel = "admin"
				cfg.Auth.ExpectedChannel = "admin"
				cfg.IAM.Mode = iamconfig.IAMModeEnforce
			},
			want: "identity.sms.enabled is only supported on channel=c",
		},
		{
			name:   "unsupported driver",
			mutate: func(cfg *appconfig.Config) { cfg.Identity.SMS.Driver = "twilio" },
			want:   "unsupported identity.sms.driver",
		},
		{
			name:   "missing template id",
			mutate: func(cfg *appconfig.Config) { cfg.Identity.SMS.TemplateID = "  " },
			want:   "identity.sms.template_id is required",
		},
		{
			name:   "code ttl too long",
			mutate: func(cfg *appconfig.Config) { cfg.Identity.SMS.CodeTTL = 16 * time.Minute },
			want:   "identity.sms.code_ttl must be in (0, 15m]",
		},
		{
			name:   "max attempts out of range",
			mutate: func(cfg *appconfig.Config) { cfg.Identity.SMS.MaxAttempts = 11 },
			want:   "identity.sms.max_attempts must be in [1, 10]",
		},
		{
			name:   "resend interval exceeds ttl",
			mutate: func(cfg *appconfig.Config) { cfg.Identity.SMS.ResendInterval = 6 * time.Minute },
			want:   "identity.sms.resend_interval must be in (0, identity.sms.code_ttl]",
		},
		{
			name:   "daily limit invalid",
			mutate: func(cfg *appconfig.Config) { cfg.Identity.SMS.DailyPerPhone = 0 },
			want:   "identity.sms.daily_per_phone must be >= 1",
		},
		{
			name:   "hourly ip limit invalid",
			mutate: func(cfg *appconfig.Config) { cfg.Identity.SMS.HourlyPerIP = 0 },
			want:   "identity.sms.hourly_per_ip must be >= 1",
		},
		{
			name:   "negative breaker failures",
			mutate: func(cfg *appconfig.Config) { cfg.Identity.SMS.BreakerFailures = -1 },
			want:   "identity.sms.breaker_failures must be >= 0",
		},
		{
			name:   "breaker timeout too short",
			mutate: func(cfg *appconfig.Config) { cfg.Identity.SMS.BreakerTimeout = 500 * time.Millisecond },
			want:   "identity.sms.breaker_timeout must be >= 1s",
		},
		{
			name: "memory driver in prod",
			mutate: func(cfg *appconfig.Config) {
				cfg.Env = "prod"
				cfg.Auth = cJWTConfig()
				cfg.ID = prodIDConfig()
				cfg.Storage.Driver = "s3"
				cfg.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
				cfg.Identity.SMS.Driver = identityconfig.IdentitySMSDriverMemory
			},
			want: "identity.sms.driver must not be memory in prod/staging",
		},
		{
			name: "missing daily budget in prod",
			mutate: func(cfg *appconfig.Config) {
				cfg.Env = "prod"
				cfg.Auth = cJWTConfig()
				cfg.ID = prodIDConfig()
				cfg.Storage.Driver = "s3"
				cfg.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
				cfg.Identity.SMS.Driver = identityconfig.IdentitySMSDriverAliyun
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
		mutate func(cfg *appconfig.Config)
		want   string
	}{
		{
			name: "providers on admin channel",
			mutate: func(cfg *appconfig.Config) {
				cfg.Channel = "admin"
				cfg.Auth.ExpectedChannel = "admin"
				cfg.IAM.Mode = iamconfig.IAMModeEnforce
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{oauthProvider("github")}
			},
			want: "identity.oauth.providers is only supported on channel=c",
		},
		{
			name: "prod c without sms or oauth",
			mutate: func(cfg *appconfig.Config) {
				cfg.Env = "prod"
				cfg.Auth = cJWTConfig()
				cfg.ID = prodIDConfig()
				cfg.Storage.Driver = "s3"
				cfg.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
				cfg.Identity.SMS.Enabled = false
			},
			want: "channel=c requires identity.sms.enabled, at least one identity.oauth.providers, or identity.saml providers",
		},
		{
			name: "invalid provider name",
			mutate: func(cfg *appconfig.Config) {
				provider := oauthProvider("GitHub")
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].name must match",
		},
		{
			name: "duplicate provider name",
			mutate: func(cfg *appconfig.Config) {
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{
					oauthProvider("github"),
					oauthProvider("github"),
				}
			},
			want: `identity.oauth.providers[1].name "github" is duplicated`,
		},
		{
			name: "missing client id",
			mutate: func(cfg *appconfig.Config) {
				provider := oauthProvider("github")
				provider.ClientID = " "
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].client_id is required",
		},
		{
			name: "missing client secret env",
			mutate: func(cfg *appconfig.Config) {
				provider := oauthProvider("github")
				provider.ClientSecretEnv = ""
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].client_secret_env is required",
		},
		{
			name: "missing redirect uri",
			mutate: func(cfg *appconfig.Config) {
				provider := oauthProvider("github")
				provider.RedirectURI = ""
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].redirect_uri is required",
		},
		{
			name: "relative auth url",
			mutate: func(cfg *appconfig.Config) {
				provider := oauthProvider("github")
				provider.AuthURL = "/oauth/authorize"
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].auth_url",
		},
		{
			name: "negative oauth rps",
			mutate: func(cfg *appconfig.Config) {
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{oauthProvider("github")}
				cfg.Identity.OAuth.RateLimit.RPS = -1
			},
			want: "identity.oauth.rate_limit.rps must be >= 0",
		},
		{
			name: "oauth fail_open in prod",
			mutate: func(cfg *appconfig.Config) {
				cfg.Env = "prod"
				cfg.Auth = cJWTConfig()
				cfg.ID = prodIDConfig()
				cfg.Storage.Driver = "s3"
				cfg.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
				cfg.Identity.SMS.Driver = identityconfig.IdentitySMSDriverAliyun
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{oauthProvider("github")}
				cfg.Identity.OAuth.RateLimit.FailureMode = platformconfig.RateLimitFailureOpen
			},
			want: "identity.oauth.rate_limit.failure_mode must be fail_closed",
		},
		{
			name: "http url in prod",
			mutate: func(cfg *appconfig.Config) {
				cfg.Env = "prod"
				cfg.Auth = cJWTConfig()
				cfg.ID = prodIDConfig()
				cfg.Storage.Driver = "s3"
				cfg.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "c:c@tcp(127.0.0.1:3306)/c"}
				cfg.Identity.SMS.Driver = identityconfig.IdentitySMSDriverAliyun
				provider := oauthProvider("github")
				provider.TokenURL = "http://idp.example.com/oauth/token"
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{provider}
			},
			want: "identity.oauth.providers[0].token_url",
		},
		{
			name: "oidc enabled without jwks",
			mutate: func(cfg *appconfig.Config) {
				provider := oauthOIDCProvider("keycloak")
				provider.JWKSURL = ""
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{provider}
			},
			want: "issuer and jwks_url are required when oidc_enabled is true",
		},
		{
			name: "oidc fields without explicit flag",
			mutate: func(cfg *appconfig.Config) {
				provider := oauthOIDCProvider("keycloak")
				provider.OIDCEnabled = false
				cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{provider}
			},
			want: "issuer and jwks_url require oidc_enabled=true",
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
	cfg.Identity.OAuth.Providers = []identityconfig.IdentityOAuthProviderConfig{oauthProvider("github")}
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
	// 域配置不再内联进平台段快照，LogValue 只输出平台字段；
	// 这里额外确认身份域段整体未进入快照，避免将来误把原始结构写进日志。
	if strings.Contains(out, "identity") || strings.Contains(out, "client_secret") {
		t.Fatalf("aggregated log snapshot must not embed identity config: %s", out)
	}
}
