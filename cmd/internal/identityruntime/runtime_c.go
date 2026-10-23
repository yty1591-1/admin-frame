package identityruntime

import (
	"context"
	"os"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	identitycache "github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	identityoauth "github.com/admin-frame/admin-frame/internal/identity/infrastructure/oauth"
	identitysms "github.com/admin-frame/admin-frame/internal/identity/infrastructure/sms"
	identityc "github.com/admin-frame/admin-frame/internal/identity/transport/c"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	pkgsms "github.com/admin-frame/admin-frame/pkg/sms"
)

// C 端短信驱动凭证的环境变量名；禁止写入 YAML（D7）。
const (
	aliyunSMSAccessKeyIDEnv     = "ALIYUN_SMS_ACCESS_KEY_ID"
	aliyunSMSAccessKeySecretEnv = "ALIYUN_SMS_ACCESS_KEY_SECRET"
)

// buildChannelC 装配 C 端短信 / OAuth 登录栈并构造 C 端 Handler。
//
// 短信未启用时 SMSLoginService 为 nil，login 端点仍注册并由 Handler 返回 50300；
// 未配置 provider 时不注册 OAuth 路由。所有外部依赖缺失一律启动失败（D8）。
func buildChannelC(
	deps *platform.Deps,
	cfg *platform.Config,
	accounts domain.AccountRepository,
	identities domain.OAuthIdentityRepository,
	guard *app.LoginGuard,
	authSvc *app.AuthService,
	restHandler *rest.Handler,
) (*identityc.Handler, error) {
	smsService, err := buildSMSLoginService(deps, cfg, accounts, guard, authSvc)
	if err != nil {
		return nil, err
	}
	oauthService, err := buildOAuthService(deps, cfg, accounts, identities, authSvc)
	if err != nil {
		return nil, err
	}

	var providers []string
	if oauthService != nil {
		providers = oauthService.ProviderNames()
	}
	handler, err := identityc.NewHandler(identityc.HandlerConfig{
		Rest:      restHandler,
		SMS:       smsService,
		OAuth:     oauthService,
		Providers: providers,
	})
	if err != nil {
		return nil, err
	}
	return handler, nil
}

// buildSMSLoginService 构造短信登录服务；identity.sms.enabled=false 时返回 nil。
func buildSMSLoginService(
	deps *platform.Deps,
	cfg *platform.Config,
	accounts domain.AccountRepository,
	guard *app.LoginGuard,
	authSvc *app.AuthService,
) (*app.SMSLoginService, error) {
	smsCfg := cfg.Identity.SMS
	if !smsCfg.Enabled {
		return nil, nil
	}

	sender, err := pkgsms.Open(context.Background(), pkgsms.Config{
		Driver: smsCfg.Driver,
		Aliyun: pkgsms.AliyunConfig{
			AccessKeyID:     os.Getenv(aliyunSMSAccessKeyIDEnv),
			AccessKeySecret: os.Getenv(aliyunSMSAccessKeySecretEnv),
			SignName:        smsCfg.SignName,
		},
	})
	if err != nil {
		return nil, apperr.Internal("identity sms driver: "+smsCfg.Driver, err)
	}
	codeSender, err := identitysms.NewCodeSender(sender, smsCfg.TemplateID, smsCfg.SignName, smsCfg.Locale)
	if err != nil {
		return nil, apperr.Internal("identity sms code sender", err)
	}

	codes, limiter := buildSMSCodeStores(deps)
	service, err := app.NewSMSLoginService(app.SMSLoginConfig{
		Accounts:       accounts,
		Guard:          guard,
		Codes:          codes,
		Limiter:        limiter,
		Sender:         codeSender,
		Auth:           authSvc,
		CodeTTL:        smsCfg.CodeTTL,
		ResendInterval: smsCfg.ResendInterval,
		MaxAttempts:    smsCfg.MaxAttempts,
		DailyPerPhone:  smsCfg.DailyPerPhone,
		HourlyPerIP:    smsCfg.HourlyPerIP,
	})
	if err != nil {
		return nil, apperr.Internal("identity sms login service", err)
	}
	return service, nil
}

// buildSMSCodeStores 优先使用 Redis（跨副本一致）；未配置 Redis 时退回进程内实现。
func buildSMSCodeStores(deps *platform.Deps) (app.SMSCodeStore, app.SMSRateLimiter) {
	if deps.Redis != nil {
		client := deps.Redis.Cache()
		return identitycache.NewRedisSMSCodeStore(client),
			identitycache.NewRedisSMSRateLimiter(client, nil)
	}
	return identitycache.NewMemorySMSCodeStore(nil),
		identitycache.NewMemorySMSRateLimiter(nil)
}

// buildOAuthService 构造 OAuth 登录服务；未配置 provider 时返回 nil。
func buildOAuthService(
	deps *platform.Deps,
	cfg *platform.Config,
	accounts domain.AccountRepository,
	identities domain.OAuthIdentityRepository,
	authSvc *app.AuthService,
) (*app.OAuthService, error) {
	providerCfgs := cfg.Identity.OAuth.Providers
	if len(providerCfgs) == 0 {
		return nil, nil
	}

	providers := make(map[string]app.OAuthProvider, len(providerCfgs))
	for _, providerCfg := range providerCfgs {
		secretEnv := strings.TrimSpace(providerCfg.ClientSecretEnv)
		secret := os.Getenv(secretEnv)
		if secretEnv == "" || strings.TrimSpace(secret) == "" {
			return nil, apperr.Internal(
				"identity oauth provider "+providerCfg.Name+": client secret env "+secretEnv+" is empty", nil)
		}
		provider, err := identityoauth.NewOAuth2Provider(identityoauth.Config{
			Name:         providerCfg.Name,
			ClientID:     providerCfg.ClientID,
			ClientSecret: secret,
			AuthURL:      providerCfg.AuthURL,
			TokenURL:     providerCfg.TokenURL,
			UserInfoURL:  providerCfg.UserInfoURL,
			RedirectURI:  providerCfg.RedirectURI,
			Scopes:       providerCfg.Scopes,
			SubjectField: providerCfg.SubjectField,
		})
		if err != nil {
			return nil, apperr.Internal("identity oauth provider "+providerCfg.Name, err)
		}
		providers[providerCfg.Name] = provider
	}

	var states app.OAuthStateStore
	if deps.Redis != nil {
		states = identitycache.NewRedisOAuthStateStore(deps.Redis.Cache())
	} else {
		states = identitycache.NewMemoryOAuthStateStore(nil)
	}

	service, err := app.NewOAuthService(app.OAuthServiceConfig{
		Providers:     providers,
		States:        states,
		Identities:    identities,
		Accounts:      accounts,
		IDs:           deps.IDs,
		Auth:          authSvc,
		AutoProvision: cfg.Identity.OAuth.AutoProvision,
		StateTTL:      app.DefaultOAuthStateTTL,
	})
	if err != nil {
		return nil, apperr.Internal("identity oauth service", err)
	}
	return service, nil
}
