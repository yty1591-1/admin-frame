package identityruntime

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	identitycache "github.com/admin-frame/admin-frame/internal/identity/infrastructure/cache"
	identityoauth "github.com/admin-frame/admin-frame/internal/identity/infrastructure/oauth"
	identitysaml "github.com/admin-frame/admin-frame/internal/identity/infrastructure/saml"
	identitysms "github.com/admin-frame/admin-frame/internal/identity/infrastructure/sms"
	identityobs "github.com/admin-frame/admin-frame/internal/identity/observability"
	identityc "github.com/admin-frame/admin-frame/internal/identity/transport/c"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/platform/dependency"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/httpclient"
	pkgsms "github.com/admin-frame/admin-frame/pkg/sms"
)

// C 端短信驱动凭证的环境变量名；禁止写入 YAML（D7）。
const (
	aliyunSMSAccessKeyIDEnv     = "ALIYUN_SMS_ACCESS_KEY_ID"
	aliyunSMSAccessKeySecretEnv = "ALIYUN_SMS_ACCESS_KEY_SECRET" //nolint:gosec // G101: 环境变量名，不是凭证值
)

// buildChannelC 装配 C 端短信 / OAuth 登录栈并构造 C 端 Handler。
//
// 短信未启用时 SMSLoginService 为 nil，login 端点仍注册并由 Handler 返回 50300；
// 未配置 provider 时不注册 OAuth 路由。所有外部依赖缺失一律启动失败（D8）。
func buildChannelC(
	deps *platform.Deps,
	cfg configView,
	accounts domain.AccountRepository,
	identities domain.OAuthIdentityRepository,
	guard *app.LoginGuard,
	authSvc *app.AuthService,
	hasher *crypto.Hasher,
	metrics app.Metrics,
	observer *identityobs.Metrics,
	restHandler *rest.Handler,
) (*identityc.Handler, error) {
	smsService, err := buildSMSLoginService(deps, cfg, accounts, guard, authSvc, hasher, metrics, observer)
	if err != nil {
		return nil, err
	}
	oauthService, err := buildOAuthService(deps, cfg, accounts, identities, authSvc, hasher, metrics, observer)
	if err != nil {
		return nil, err
	}
	samlService, err := buildSAMLService(deps, cfg, accounts, identities, authSvc, hasher, observer)
	if err != nil {
		return nil, err
	}

	var providers []string
	if oauthService != nil {
		providers = oauthService.ProviderNames()
	}
	handler, err := identityc.NewHandler(identityc.HandlerConfig{
		Rest:            restHandler,
		SMS:             smsService,
		OAuth:           oauthService,
		SAML:            samlService,
		SAMLRedirectURL: cfg.Identity.SAML.LoginRedirectURL,
		Auth:            authSvc,
		Providers:       providers,
		ClientIP:        deps.ClientIP,
		Hasher:          hasher,
	})
	if err != nil {
		return nil, err
	}
	return handler, nil
}

// buildSMSLoginService 构造短信登录服务；identity.sms.enabled=false 时返回 nil。
func buildSMSLoginService(
	deps *platform.Deps,
	cfg configView,
	accounts domain.AccountRepository,
	guard *app.LoginGuard,
	authSvc *app.AuthService,
	hasher *crypto.Hasher,
	metrics app.Metrics,
	observer *identityobs.Metrics,
) (*app.SMSLoginService, error) {
	smsCfg := cfg.Identity.SMS
	if !smsCfg.Enabled {
		return nil, nil
	}

	smsDriverCfg := pkgsms.Config{
		Driver: smsCfg.Driver,
		Aliyun: pkgsms.AliyunConfig{
			AccessKeyID:     os.Getenv(aliyunSMSAccessKeyIDEnv),
			AccessKeySecret: os.Getenv(aliyunSMSAccessKeySecretEnv),
			SignName:        smsCfg.SignName,
		},
	}
	// 出站客户端经依赖注册表构造：超时与依赖指标统一口径；重试保持关闭，
	// 由 identitysms 编排层按供应商语义决定，避免与注册表重试叠加。
	if deps.Dependencies != nil {
		caller, depErr := deps.Dependencies.HTTP(dependency.Spec{
			Name: "identity:sms:" + smsCfg.Driver,
		}, httpclient.Config{DisableRetry: true})
		if depErr != nil {
			return nil, apperr.Internal("identity sms dependency", depErr)
		}
		smsDriverCfg.HTTPClient = caller.Client()
	}
	sender, err := pkgsms.Open(context.Background(), smsDriverCfg)
	if err != nil {
		return nil, apperr.Internal("identity sms driver: "+smsCfg.Driver, err)
	}
	breaker, err := buildSMSBreaker(smsCfg, deps.Logger)
	if err != nil {
		return nil, apperr.Internal("identity sms circuit breaker", err)
	}
	// OPS-07：熔断状态必须可被采集；状态变化仍由 buildSMSBreaker 写告警日志。
	if observer != nil {
		if gaugeErr := observer.RegisterSMSBreakerGauge(func() string { return breaker.State().String() }); gaugeErr != nil {
			return nil, apperr.Internal("identity sms breaker gauge", gaugeErr)
		}
	}
	alertSink := buildSMSAlertSink(deps, metrics)
	codeSender, err := identitysms.NewCodeSender(
		sender, smsCfg.TemplateID, smsCfg.SignName, smsCfg.Locale,
		identitysms.WithBreaker(breaker),
		identitysms.WithAlertSink(alertSink),
	)
	if err != nil {
		return nil, apperr.Internal("identity sms code sender", err)
	}

	quotaLocation := time.UTC
	if zone := strings.TrimSpace(smsCfg.QuotaTimezone); zone != "" {
		if loc, locErr := time.LoadLocation(zone); locErr == nil {
			quotaLocation = loc
		}
	}
	codes, limiter := buildSMSCodeStores(deps, quotaLocation)
	service, err := app.NewSMSLoginService(app.SMSLoginConfig{
		Accounts:       accounts,
		Guard:          guard,
		Codes:          codes,
		Limiter:        limiter,
		Sender:         codeSender,
		Auth:           authSvc,
		Hasher:         hasher,
		Metrics:        metrics,
		CodeTTL:        smsCfg.CodeTTL,
		ResendInterval: smsCfg.ResendInterval,
		MaxAttempts:    smsCfg.MaxAttempts,
		DailyPerPhone:  smsCfg.DailyPerPhone,
		HourlyPerIP:    smsCfg.HourlyPerIP,
		DailyBudget:    smsCfg.DailyBudget,
		DefaultRegion:  smsCfg.DefaultRegion,
	})
	if err != nil {
		return nil, apperr.Internal("identity sms login service", err)
	}
	return service, nil
}

// buildSMSCodeStores 优先使用 Redis（跨副本一致）；未配置 Redis 时退回进程内实现。
//
// quotaLocation 决定日配额 / 全局预算的分桶时区（BIZ-05）。
func buildSMSCodeStores(deps *platform.Deps, quotaLocation *time.Location) (app.SMSCodeStore, app.SMSRateLimiter) {
	if deps.Redis != nil {
		client := deps.Redis.Cache()
		return identitycache.NewRedisSMSCodeStore(client),
			identitycache.NewRedisSMSRateLimiterWithLocation(client, nil, quotaLocation)
	}
	return identitycache.NewMemorySMSCodeStore(nil),
		identitycache.NewMemorySMSRateLimiterWithLocation(nil, quotaLocation)
}

// buildSMSBreaker 按配置构造供应商熔断器（IDN-P2-05）。
//
// 永久拒绝（模板/签名非法、号码非法）不视为供应商故障，不触发熔断。
func buildSMSBreaker(cfg identityconfig.IdentitySMSConfig, logger *slog.Logger) (*circuitbreaker.Breaker, error) {
	failures := cfg.BreakerFailures
	if failures <= 0 {
		failures = 5
	}
	return circuitbreaker.New(circuitbreaker.Config{
		Name:    "identity-sms-" + cfg.Driver,
		Timeout: cfg.BreakerTimeout,
		ReadyToTrip: func(counts circuitbreaker.Counts) bool {
			return int(counts.ConsecutiveFailures) >= failures
		},
		IsSuccessful: identitysms.BreakerIsSuccessful,
		OnStateChange: func(name string, from, to circuitbreaker.State) {
			if logger != nil {
				logger.Warn("identity sms circuit breaker",
					"provider", name, "from", from.String(), "to", to.String())
			}
		},
	})
}

// smsAlertObserver 是告警指标的可选能力，避免在 app.Metrics 接口上扩散基础设施细节。
type smsAlertObserver interface {
	ObserveSMSProviderAlert(kind, provider string)
}

// buildSMSAlertSink 构造供应商告警出口：错误日志 + 指标计数，不包含 PII。
func buildSMSAlertSink(deps *platform.Deps, metrics app.Metrics) identitysms.AlertSink {
	if deps == nil || (deps.Logger == nil && metrics == nil) {
		return nil
	}
	return func(alert identitysms.Alert) {
		if deps.Logger != nil {
			deps.Logger.Error("identity sms provider alert",
				"alert", alert.Kind, "provider", alert.Provider, "code", alert.Code)
		}
		if observer, ok := metrics.(smsAlertObserver); ok {
			observer.ObserveSMSProviderAlert(alert.Kind, alert.Provider)
		}
	}
}

// buildOAuthService 构造 OAuth 登录服务；未配置 provider 时返回 nil。
func buildOAuthService(
	deps *platform.Deps,
	cfg configView,
	accounts domain.AccountRepository,
	identities domain.OAuthIdentityRepository,
	authSvc *app.AuthService,
	hasher *crypto.Hasher,
	metrics app.Metrics,
	observer *identityobs.Metrics,
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
			Name:                   providerCfg.Name,
			ClientID:               providerCfg.ClientID,
			ClientSecret:           secret,
			AuthURL:                providerCfg.AuthURL,
			TokenURL:               providerCfg.TokenURL,
			UserInfoURL:            providerCfg.UserInfoURL,
			RedirectURI:            providerCfg.RedirectURI,
			Scopes:                 providerCfg.Scopes,
			SubjectField:           providerCfg.SubjectField,
			Timeout:                cfg.Identity.OAuth.Timeout,
			OIDCEnabled:            providerCfg.OIDCEnabled,
			Issuer:                 providerCfg.Issuer,
			JWKSURL:                providerCfg.JWKSURL,
			Audience:               providerCfg.Audience,
			JWKSTTL:                providerCfg.JWKSTTL,
			JWKSMinRefreshInterval: providerCfg.JWKSMinRefreshInterval,
			Leeway:                 providerCfg.IDTokenLeeway,
			Metrics:                metrics,
		})
		if err != nil {
			return nil, apperr.Internal("identity oauth provider "+providerCfg.Name, err)
		}
		// OIDC 提供方额外暴露公钥缓存新鲜度；轮换 / IdP 故障时该值持续增长。
		if observer != nil && provider.OIDCEnabled() {
			if gaugeErr := observer.RegisterOAuthJWKSAgeGauge(providerCfg.Name, provider.JWKSCacheAge); gaugeErr != nil {
				return nil, apperr.Internal("identity oauth jwks age gauge", gaugeErr)
			}
		}
		providers[providerCfg.Name] = provider
	}

	var states app.OAuthStateStore
	if deps.Redis != nil {
		states = identitycache.NewRedisOAuthStateStore(deps.Redis.Cache())
	} else {
		states = identitycache.NewMemoryOAuthStateStore(nil)
	}
	limiter, err := buildOAuthLimiter(cfg, deps)
	if err != nil {
		return nil, err
	}

	service, err := app.NewOAuthService(app.OAuthServiceConfig{
		Providers:       providers,
		States:          states,
		Identities:      identities,
		Accounts:        accounts,
		IDs:             deps.IDs,
		Auth:            authSvc,
		Hasher:          hasher,
		AutoProvision:   cfg.Identity.OAuth.AutoProvision,
		StateTTL:        app.DefaultOAuthStateTTL,
		Metrics:         metrics,
		Limiter:         limiter,
		BreakerFailures: cfg.Identity.OAuth.BreakerFailures,
		BreakerTimeout:  cfg.Identity.OAuth.BreakerTimeout,
	})
	if err != nil {
		return nil, apperr.Internal("identity oauth service", err)
	}
	return service, nil
}

// buildSAMLService 构造 SAML 登录服务；identity.saml.enabled=false 时返回 nil。
//
// 启动期完成 IdP 元数据加载与 SP 密钥配对校验，任何失败都阻断启动（失败关闭）。
func buildSAMLService(
	deps *platform.Deps,
	cfg configView,
	accounts domain.AccountRepository,
	identities domain.OAuthIdentityRepository,
	authSvc *app.AuthService,
	hasher *crypto.Hasher,
	observer *identityobs.Metrics,
) (*app.SAMLService, error) {
	samlCfg := cfg.Identity.SAML
	if !samlCfg.Enabled {
		return nil, nil
	}
	spCfg := identitysaml.SPConfig{
		EntityID:        samlCfg.EntityID,
		AcsURL:          samlCfg.ACSURL,
		CertificatePEM:  samlCfg.CertificatePEM,
		PrivateKeyPEM:   samlCfg.PrivateKey,
		CertificateNext: samlCfg.CertificateNextPEM,
		PrivateKeyNext:  samlCfg.PrivateKeyNextPEM,
		NameIDFormat:    samlCfg.NameIDFormat,
	}
	providers := make(map[string]app.SAMLProvider, len(samlCfg.Providers))
	for _, providerCfg := range samlCfg.Providers {
		var httpClient *http.Client
		if deps.Dependencies != nil {
			caller, err := deps.Dependencies.HTTP(dependency.Spec{
				Name:    "identity:saml:" + providerCfg.Name,
				Timeout: samlCfg.Timeout,
			}, httpclient.Config{})
			if err != nil {
				return nil, apperr.Internal("identity saml dependency "+providerCfg.Name, err)
			}
			httpClient = caller.HTTP()
		}
		provider, err := identitysaml.NewProvider(context.Background(), identitysaml.Config{
			Name:         providerCfg.Name,
			IDPEntityID:  providerCfg.IDPEntityID,
			MetadataURL:  providerCfg.MetadataURL,
			MetadataXML:  providerCfg.MetadataXML,
			MetadataTTL:  samlCfg.MetadataTTL,
			MinRefresh:   samlCfg.MetadataMinRefresh,
			Timeout:      samlCfg.Timeout,
			ClockSkew:    samlCfg.ClockSkew,
			AssertionTTL: samlCfg.AssertionTTL,
			SP:           spCfg,
			HTTPClient:   httpClient,
		})
		if err != nil {
			return nil, apperr.Internal("identity saml provider "+providerCfg.Name, err)
		}
		providers[providerCfg.Name] = provider
	}
	spMetadata, err := identitysaml.SPMetadataXML(spCfg)
	if err != nil {
		return nil, apperr.Internal("identity saml sp metadata", err)
	}

	var states app.OAuthStateStore
	var replay app.SAMLReplayStore
	var tickets app.SAMLTicketStore
	if deps.Redis != nil {
		client := deps.Redis.Cache()
		states = identitycache.NewRedisOAuthStateStore(client)
		replay = identitycache.NewRedisSAMLReplayStore(client)
		tickets = identitycache.NewRedisSAMLTicketStore(client)
	} else {
		states = identitycache.NewMemoryOAuthStateStore(nil)
		replay = identitycache.NewMemorySAMLReplayStore(nil)
		tickets = identitycache.NewMemorySAMLTicketStore(nil)
	}
	limiter, err := buildSAMLLimiter(cfg, deps)
	if err != nil {
		return nil, err
	}
	var samlMetrics app.SAMLMetrics
	if observer != nil {
		samlMetrics = observer
	}
	service, err := app.NewSAMLService(app.SAMLServiceConfig{
		Providers:     providers,
		States:        states,
		Identities:    identities,
		Accounts:      accounts,
		IDs:           deps.IDs,
		Auth:          authSvc,
		Hasher:        hasher,
		AutoProvision: samlCfg.AutoProvision,
		StateTTL:      samlCfg.RequestTTL,
		ReplayTTL:     samlCfg.ReplayTTL,
		TicketTTL:     samlCfg.TicketTTL,
		Replay:        replay,
		Tickets:       tickets,
		Metrics:       samlMetrics,
		Limiter:       limiter,
		SPMetadataXML: spMetadata,
	})
	if err != nil {
		return nil, apperr.Internal("identity saml service", err)
	}
	return service, nil
}
