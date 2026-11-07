package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// validateIdentitySMS 校验短信验证码参数（docs/identity-authn.md §8.1 / §10.2）。
func (c *Config) validateIdentitySMS() []error {
	sms := c.Identity.SMS
	var errs []error
	if sms.Enabled && c.Channel != "c" {
		errs = append(errs, errors.New("identity.sms.enabled is only supported on channel=c"))
	}
	// 空值等价于默认 memory（与 applyIdentityDefaults 保持一致）。
	driver := sms.Driver
	if driver == "" {
		driver = IdentitySMSDriverMemory
	}
	switch driver {
	case IdentitySMSDriverMemory, IdentitySMSDriverAliyun:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.sms.driver: %s", sms.Driver))
	}
	if !sms.Enabled {
		return errs
	}
	if strings.TrimSpace(sms.TemplateID) == "" {
		errs = append(errs, errors.New("identity.sms.template_id is required when identity.sms.enabled=true"))
	}
	if sms.CodeTTL <= 0 || sms.CodeTTL > 15*time.Minute {
		errs = append(errs, errors.New("identity.sms.code_ttl must be in (0, 15m]"))
	}
	if sms.MaxAttempts < 1 || sms.MaxAttempts > 10 {
		errs = append(errs, errors.New("identity.sms.max_attempts must be in [1, 10]"))
	}
	if sms.ResendInterval <= 0 || sms.ResendInterval > sms.CodeTTL {
		errs = append(errs, errors.New("identity.sms.resend_interval must be in (0, identity.sms.code_ttl]"))
	}
	if sms.DailyPerPhone < 1 {
		errs = append(errs, errors.New("identity.sms.daily_per_phone must be >= 1"))
	}
	if sms.HourlyPerIP < 1 {
		errs = append(errs, errors.New("identity.sms.hourly_per_ip must be >= 1"))
	}
	if sms.DailyBudget < 0 {
		errs = append(errs, errors.New("identity.sms.daily_budget must be >= 0"))
	}
	// 生产必须有全局硬预算，防止分布式号码池绕过单手机号 / IP 限额造成费用攻击。
	if isProdLikeEnv(c.Env) && sms.DailyBudget == 0 {
		errs = append(errs, errors.New("identity.sms.daily_budget must be > 0 in prod/staging"))
	}
	if sms.BreakerFailures < 0 {
		errs = append(errs, errors.New("identity.sms.breaker_failures must be >= 0"))
	}
	// 0 表示由默认值填充（60s）。
	if sms.BreakerTimeout != 0 {
		if sms.BreakerTimeout < identityMinSMSBreakerTimeout {
			errs = append(errs, errors.New("identity.sms.breaker_timeout must be >= 1s"))
		}
		if sms.BreakerTimeout > identityMaxSMSBreakerTimeout {
			errs = append(errs, errors.New("identity.sms.breaker_timeout must be <= 10m"))
		}
	}
	if isProdLikeEnv(c.Env) && driver == IdentitySMSDriverMemory {
		errs = append(errs, errors.New("identity.sms.driver must not be memory in prod/staging"))
	}
	if region := strings.ToUpper(strings.TrimSpace(sms.DefaultRegion)); region != "" {
		if _, ok := identitySMSRegions[region]; !ok {
			errs = append(errs, fmt.Errorf("unsupported identity.sms.default_region: %s", sms.DefaultRegion))
		}
	}
	if zone := strings.TrimSpace(sms.QuotaTimezone); zone != "" {
		if _, err := time.LoadLocation(zone); err != nil {
			errs = append(errs, fmt.Errorf("unsupported identity.sms.quota_timezone: %s", zone))
		}
	}
	return errs
}

// identitySMSRegions 是允许的短信默认地区及其国家码（BIZ-07）。
//
// 与 internal/identity/app 的 regionDialCodes 保持一致；新增地区需同时更新两处。
var identitySMSRegions = map[string]string{
	"CN": "86", "HK": "852", "TW": "886", "MO": "853",
	"US": "1", "CA": "1", "GB": "44", "JP": "81",
	"SG": "65", "DE": "49", "FR": "33", "AU": "61",
}

// validateIdentityOAuth 校验 OAuth 提供方参数（docs/identity-authn.md §8.2 / §10.2）。
func (c *Config) validateIdentityOAuth() []error {
	providers := c.Identity.OAuth.Providers
	var errs []error
	if len(providers) > 0 && c.Channel != "c" {
		errs = append(errs, errors.New("identity.oauth.providers is only supported on channel=c"))
	}
	if c.Channel == "c" && isProdLikeEnv(c.Env) &&
		!c.Identity.SMS.Enabled && len(providers) == 0 &&
		(!c.Identity.SAML.Enabled || len(c.Identity.SAML.Providers) == 0) {
		errs = append(errs, errors.New("channel=c requires identity.sms.enabled, at least one identity.oauth.providers, or identity.saml providers in prod/staging"))
	}
	if len(providers) > 0 {
		rate := c.Identity.OAuth.RateLimit
		// 0 表示由默认值填充（1 rps / burst 5），直接构造 Config 的单测路径依赖该语义。
		if rate.RPS < 0 {
			errs = append(errs, errors.New("identity.oauth.rate_limit.rps must be >= 0"))
		}
		if rate.Burst < 0 {
			errs = append(errs, errors.New("identity.oauth.rate_limit.burst must be >= 0"))
		}
		switch rate.FailureMode {
		case "", RateLimitFailureClosed, RateLimitFailureOpen:
		default:
			errs = append(errs, fmt.Errorf("unsupported identity.oauth.rate_limit.failure_mode: %s", rate.FailureMode))
		}
		if isProdLikeEnv(c.Env) && rate.FailureMode == RateLimitFailureOpen {
			errs = append(errs, errors.New("identity.oauth.rate_limit.failure_mode must be fail_closed in prod/staging"))
		}
		// 0 表示默认值；显式配置时校验范围。
		if c.Identity.OAuth.Timeout != 0 &&
			(c.Identity.OAuth.Timeout < time.Second || c.Identity.OAuth.Timeout > 30*time.Second) {
			errs = append(errs, errors.New("identity.oauth.timeout must be in [1s, 30s]"))
		}
		if c.Identity.OAuth.BreakerFailures < 0 {
			errs = append(errs, errors.New("identity.oauth.breaker_failures must be >= 0"))
		}
		if c.Identity.OAuth.BreakerTimeout != 0 &&
			(c.Identity.OAuth.BreakerTimeout < time.Second || c.Identity.OAuth.BreakerTimeout > 10*time.Minute) {
			errs = append(errs, errors.New("identity.oauth.breaker_timeout must be in [1s, 10m]"))
		}
	}
	seen := make(map[string]struct{}, len(providers))
	for i, provider := range providers {
		path := fmt.Sprintf("identity.oauth.providers[%d]", i)
		name := strings.TrimSpace(provider.Name)
		if !isOAuthProviderName(name) {
			errs = append(errs, fmt.Errorf("%s.name must match ^[a-z][a-z0-9_-]{1,31}$", path))
		} else if _, dup := seen[name]; dup {
			errs = append(errs, fmt.Errorf("%s.name %q is duplicated", path, name))
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(provider.ClientID) == "" {
			errs = append(errs, fmt.Errorf("%s.client_id is required", path))
		}
		if strings.TrimSpace(provider.ClientSecretEnv) == "" {
			errs = append(errs, fmt.Errorf("%s.client_secret_env is required", path))
		}
		if strings.TrimSpace(provider.RedirectURI) == "" {
			errs = append(errs, fmt.Errorf("%s.redirect_uri is required", path))
		}
		oidcConfigured := strings.TrimSpace(provider.Issuer) != "" || strings.TrimSpace(provider.JWKSURL) != ""
		if provider.OIDCEnabled {
			if strings.TrimSpace(provider.Issuer) == "" || strings.TrimSpace(provider.JWKSURL) == "" {
				// issuer 与 jwks_url 必须成对出现，否则无法完成 id_token 验签与来源校验。
				errs = append(errs, fmt.Errorf("%s.issuer and jwks_url are required when oidc_enabled is true", path))
			}
		} else if oidcConfigured {
			// OIDC 默认关闭；禁止仅凭 issuer/jwks 字段隐式启用，避免绕过评审的配置漂移。
			errs = append(errs, fmt.Errorf("%s.issuer and jwks_url require oidc_enabled=true", path))
		}
		oidcEnabled := provider.OIDCEnabled
		urls := map[string]string{
			"auth_url":     provider.AuthURL,
			"token_url":    provider.TokenURL,
			"redirect_uri": provider.RedirectURI,
		}
		if oidcEnabled {
			urls["issuer"] = provider.Issuer
			urls["jwks_url"] = provider.JWKSURL
		} else {
			urls["userinfo_url"] = provider.UserInfoURL
		}
		for field, raw := range urls {
			if err := validateAbsoluteURL(raw, isProdLikeEnv(c.Env)); err != nil {
				errs = append(errs, fmt.Errorf("%s.%s: %w", path, field, err))
			}
		}
		if oidcEnabled {
			if provider.JWKSTTL < time.Minute || provider.JWKSTTL > 24*time.Hour {
				errs = append(errs, fmt.Errorf("%s.jwks_ttl must be in [1m, 24h]", path))
			}
			if provider.JWKSMinRefreshInterval < 5*time.Second ||
				provider.JWKSMinRefreshInterval > provider.JWKSTTL {
				errs = append(errs, fmt.Errorf("%s.jwks_min_refresh_interval must be in [5s, jwks_ttl]", path))
			}
			if provider.IDTokenLeeway < 0 || provider.IDTokenLeeway > 5*time.Minute {
				errs = append(errs, fmt.Errorf("%s.id_token_leeway must be in [0, 5m]", path))
			}
		}
	}
	return errs
}

// identitySAMLACSPath 是 ACS 端点固定路径；acs_url 的路径必须与之完全一致。
const identitySAMLACSPath = "/c/v1/auth/saml/acs"

// validateIdentitySAML 校验 SAML 参数（docs/identity-authn.md §8.3 / §10）。
//
// 零值时长按默认值处理，便于直接构造 Config 的单测路径；显式配置时校验范围。
func (c *Config) validateIdentitySAML() []error {
	samlCfg := c.Identity.SAML
	providers := samlCfg.Providers
	var errs []error

	if !samlCfg.Enabled {
		if len(providers) > 0 {
			errs = append(errs, errors.New("identity.saml.providers requires identity.saml.enabled=true"))
		}
		if strings.TrimSpace(samlCfg.PrivateKey) != "" {
			errs = append(errs, errors.New("identity.saml.private_key requires identity.saml.enabled=true"))
		}
		return errs
	}
	if c.Channel != "c" {
		errs = append(errs, errors.New("identity.saml is only supported on channel=c"))
	}

	prod := isProdLikeEnv(c.Env)
	if strings.TrimSpace(samlCfg.EntityID) == "" {
		errs = append(errs, errors.New("identity.saml.entity_id is required"))
	} else if err := validateAbsoluteURL(samlCfg.EntityID, prod); err != nil {
		errs = append(errs, fmt.Errorf("identity.saml.entity_id: %w", err))
	}
	if strings.TrimSpace(samlCfg.ACSURL) == "" {
		errs = append(errs, errors.New("identity.saml.acs_url is required"))
	} else if err := validateAbsoluteURL(samlCfg.ACSURL, prod); err != nil {
		errs = append(errs, fmt.Errorf("identity.saml.acs_url: %w", err))
	} else if parsed, parseErr := url.Parse(strings.TrimSpace(samlCfg.ACSURL)); parseErr == nil &&
		parsed.Path != identitySAMLACSPath {
		errs = append(errs, fmt.Errorf("identity.saml.acs_url path must be %s", identitySAMLACSPath))
	}
	if strings.TrimSpace(samlCfg.LoginRedirectURL) == "" {
		errs = append(errs, errors.New("identity.saml.login_redirect_url is required"))
	} else if err := validateAbsoluteURL(samlCfg.LoginRedirectURL, prod); err != nil {
		errs = append(errs, fmt.Errorf("identity.saml.login_redirect_url: %w", err))
	}

	// 私钥在 prod 由托管源注入；样例与配置文件留空，运行时仍强校验密钥可用性。
	if _, _, keyErr := parseSAMLKeyPair(samlCfg.CertificatePEM, samlCfg.PrivateKey); keyErr != nil {
		errs = append(errs, fmt.Errorf("identity.saml.certificate_pem/private_key: %w", keyErr))
	}
	nextCert := strings.TrimSpace(samlCfg.CertificateNextPEM)
	nextKey := strings.TrimSpace(samlCfg.PrivateKeyNextPEM)
	if nextCert != "" || nextKey != "" {
		if nextCert == "" || nextKey == "" {
			errs = append(errs, errors.New("identity.saml.certificate_next_pem and private_key_next must be set together"))
		} else if _, _, nextErr := parseSAMLKeyPair(nextCert, nextKey); nextErr != nil {
			errs = append(errs, fmt.Errorf("identity.saml.certificate_next_pem/private_key_next: %w", nextErr))
		} else if nextCert == strings.TrimSpace(samlCfg.CertificatePEM) {
			errs = append(errs, errors.New("identity.saml.certificate_next_pem must differ from certificate_pem"))
		}
	}

	skew := effectiveSAMLDuration(samlCfg.ClockSkew, 2*time.Minute)
	assertionTTL := effectiveSAMLDuration(samlCfg.AssertionTTL, 5*time.Minute)
	requestTTL := effectiveSAMLDuration(samlCfg.RequestTTL, 10*time.Minute)
	replayTTL := samlCfg.ReplayTTL
	if replayTTL == 0 {
		replayTTL = assertionTTL + skew
	}
	ticketTTL := effectiveSAMLDuration(samlCfg.TicketTTL, time.Minute)
	if skew > 5*time.Minute {
		errs = append(errs, errors.New("identity.saml.clock_skew must be in (0, 5m]"))
	}
	if assertionTTL > 15*time.Minute {
		errs = append(errs, errors.New("identity.saml.assertion_ttl must be in (0, 15m]"))
	}
	if requestTTL > 15*time.Minute {
		errs = append(errs, errors.New("identity.saml.request_ttl must be in (0, 15m]"))
	}
	if replayTTL < assertionTTL+skew {
		errs = append(errs, errors.New("identity.saml.replay_ttl must be >= assertion_ttl + clock_skew"))
	}
	if ticketTTL < 30*time.Second || ticketTTL > 5*time.Minute {
		errs = append(errs, errors.New("identity.saml.ticket_ttl must be in [30s, 5m]"))
	}
	if samlCfg.Timeout != 0 && (samlCfg.Timeout < time.Second || samlCfg.Timeout > 30*time.Second) {
		errs = append(errs, errors.New("identity.saml.timeout must be in [1s, 30s]"))
	}
	if samlCfg.MetadataTTL != 0 && (samlCfg.MetadataTTL < time.Minute || samlCfg.MetadataTTL > 24*time.Hour) {
		errs = append(errs, errors.New("identity.saml.metadata_ttl must be in [1m, 24h]"))
	}
	if samlCfg.MetadataMinRefresh != 0 && (samlCfg.MetadataMinRefresh < 5*time.Second ||
		samlCfg.MetadataMinRefresh > effectiveSAMLDuration(samlCfg.MetadataTTL, 15*time.Minute)) {
		errs = append(errs, errors.New("identity.saml.metadata_min_refresh must be in [5s, metadata_ttl]"))
	}
	rate := samlCfg.RateLimit
	if rate.RPS < 0 {
		errs = append(errs, errors.New("identity.saml.rate_limit.rps must be >= 0"))
	}
	if rate.Burst < 0 {
		errs = append(errs, errors.New("identity.saml.rate_limit.burst must be >= 0"))
	}
	switch rate.FailureMode {
	case "", RateLimitFailureClosed, RateLimitFailureOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.saml.rate_limit.failure_mode: %s", rate.FailureMode))
	}
	if prod && rate.FailureMode == RateLimitFailureOpen {
		errs = append(errs, errors.New("identity.saml.rate_limit.failure_mode must be fail_closed in prod/staging"))
	}

	if len(providers) == 0 {
		errs = append(errs, errors.New("identity.saml.providers is required when identity.saml.enabled=true"))
	}
	seen := make(map[string]struct{}, len(providers))
	for i, provider := range providers {
		path := fmt.Sprintf("identity.saml.providers[%d]", i)
		name := strings.TrimSpace(provider.Name)
		if !isOAuthProviderName(name) {
			errs = append(errs, fmt.Errorf("%s.name must match ^[a-z][a-z0-9_-]{1,31}$", path))
		} else if _, dup := seen[name]; dup {
			errs = append(errs, fmt.Errorf("%s.name %q is duplicated", path, name))
		}
		seen[name] = struct{}{}
		metadataURL := strings.TrimSpace(provider.MetadataURL)
		metadataXML := strings.TrimSpace(provider.MetadataXML)
		if metadataURL == "" && metadataXML == "" {
			errs = append(errs, fmt.Errorf("%s requires one of metadata_url or metadata_xml", path))
		}
		if metadataURL != "" && metadataXML != "" {
			errs = append(errs, fmt.Errorf("%s must not set both metadata_url and metadata_xml", path))
		}
		if metadataURL != "" {
			if err := validateAbsoluteURL(metadataURL, prod); err != nil {
				errs = append(errs, fmt.Errorf("%s.metadata_url: %w", path, err))
			}
		}
		if strings.TrimSpace(provider.IDPEntityID) == "" {
			errs = append(errs, fmt.Errorf("%s.idp_entity_id is required", path))
		}
	}
	return errs
}

// effectiveSAMLDuration 返回生效时长：零值使用默认值。
func effectiveSAMLDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}
