package config

import (
	"strings"
	"time"
)

// ApplyDefaults 填充身份域默认值；Enabled 保持 false（默认关闭）。
//
// 只处理与其它字段无关的常量默认值；跨字段派生（如 SHA-256 摘要密钥 ID）
// 也在本函数内完成，由装载器在解码文件之前调用。
func (c *IdentityConfig) ApplyDefaults() {
	if c.AccessTTL == 0 {
		c.AccessTTL = 15 * time.Minute
	}
	if c.RefreshTTL == 0 {
		c.RefreshTTL = 168 * time.Hour
	}
	if c.Session.IdleTimeout == 0 {
		c.Session.IdleTimeout = 72 * time.Hour
	}
	if c.Session.AbsoluteTTL == 0 {
		c.Session.AbsoluteTTL = 720 * time.Hour
	}
	if c.Session.MaxPerAccount == 0 {
		c.Session.MaxPerAccount = 10
	}
	if c.Session.RefreshGrace == 0 {
		c.Session.RefreshGrace = 10 * time.Second
	}
	if c.Retention.SessionDays == 0 {
		c.Retention.SessionDays = 30
	}
	if c.Retention.LoginAttemptDays == 0 {
		c.Retention.LoginAttemptDays = 180
	}
	// 0 使用默认 365 天；负数表示显式关闭归档。
	if c.Retention.ArchiveDays == 0 {
		c.Retention.ArchiveDays = 365
	}
	if c.Retention.CleanupInterval == 0 {
		c.Retention.CleanupInterval = time.Hour
	}
	if c.Revocation.Driver == "" {
		c.Revocation.Driver = IdentityRevocationDriverRedis
	}
	if c.Revocation.FailureMode == "" {
		c.Revocation.FailureMode = failureModeClosed
	}
	if c.Revocation.StateTTL == 0 {
		// 默认 min(access_ttl + 1m, 60s)：常规 15m access_ttl 下即 60s。
		accessLeeway := c.AccessTTL + time.Minute
		if accessLeeway > identityMaxRevocationStateTTL {
			accessLeeway = identityMaxRevocationStateTTL
		}
		c.Revocation.StateTTL = accessLeeway
	}
	// 负数表示显式关闭 L1；0 使用默认 1s，避免「未配置」被误读为关闭。
	if c.Revocation.LocalCacheTTL == 0 {
		c.Revocation.LocalCacheTTL = identityDefaultRevocationLocalCacheTTL
	}
	if c.Revocation.LocalCacheCapacity <= 0 {
		c.Revocation.LocalCacheCapacity = identityDefaultRevocationLocalCacheCapacity
	}
	// jti 宽限：0 使用默认 30s；负数表示显式关闭。
	if c.Revocation.TokenGrace == 0 {
		c.Revocation.TokenGrace = identityDefaultRevocationTokenGrace
	}
	// 自助注销再认证窗口：0 使用默认 10m。
	if c.Consumer.DeleteReauthWindow == 0 {
		c.Consumer.DeleteReauthWindow = identityDefaultConsumerDeleteReauthWindow
	}
	if c.Lockout.MaxFailures == 0 {
		c.Lockout.MaxFailures = 5
	}
	if c.Lockout.Window == 0 {
		c.Lockout.Window = 15 * time.Minute
	}
	if c.Lockout.Duration == 0 {
		c.Lockout.Duration = 15 * time.Minute
	}
	if c.LoginRateLimit.RPS == 0 {
		c.LoginRateLimit.RPS = 1
	}
	if c.LoginRateLimit.Burst == 0 {
		c.LoginRateLimit.Burst = 5
	}
	if c.LoginRateLimit.FailureMode == "" {
		c.LoginRateLimit.FailureMode = failureModeClosed
	}
	if c.LoginRateLimit.IPRPS == 0 {
		c.LoginRateLimit.IPRPS = 20
	}
	if c.LoginRateLimit.IPBurst == 0 {
		c.LoginRateLimit.IPBurst = 40
	}
	if c.RefreshRateLimit.RPS == 0 {
		c.RefreshRateLimit.RPS = 10
	}
	if c.RefreshRateLimit.Burst == 0 {
		c.RefreshRateLimit.Burst = 20
	}
	if c.RefreshRateLimit.FailureMode == "" {
		c.RefreshRateLimit.FailureMode = failureModeClosed
	}
	if c.HashKeyID == "" && strings.TrimSpace(c.HashKey) != "" {
		c.HashKeyID = "v1"
	}
	if c.Password.WaitTimeout == 0 {
		c.Password.WaitTimeout = identityDefaultPasswordWaitTimeout
	}
	if c.Password.HistoryCount == 0 {
		c.Password.HistoryCount = 5
	}
	if c.Password.HistoryWindow == 0 {
		c.Password.HistoryWindow = 365 * 24 * time.Hour
	}
	if c.OAuth.RateLimit.RPS == 0 {
		c.OAuth.RateLimit.RPS = 1
	}
	if c.OAuth.RateLimit.Burst == 0 {
		c.OAuth.RateLimit.Burst = 5
	}
	if c.OAuth.RateLimit.FailureMode == "" {
		c.OAuth.RateLimit.FailureMode = failureModeClosed
	}
	if c.OAuth.Timeout == 0 {
		c.OAuth.Timeout = 5 * time.Second
	}
	if c.OAuth.BreakerFailures == 0 {
		c.OAuth.BreakerFailures = 5
	}
	if c.OAuth.BreakerTimeout == 0 {
		c.OAuth.BreakerTimeout = 60 * time.Second
	}
	if c.MFA.Issuer == "" {
		c.MFA.Issuer = identityDefaultMFAIssuer
	}
	if c.SMS.Driver == "" {
		c.SMS.Driver = IdentitySMSDriverMemory
	}
	if c.SMS.CodeTTL == 0 {
		c.SMS.CodeTTL = 5 * time.Minute
	}
	if c.SMS.MaxAttempts == 0 {
		c.SMS.MaxAttempts = 5
	}
	if c.SMS.ResendInterval == 0 {
		c.SMS.ResendInterval = time.Minute
	}
	if c.SMS.DailyPerPhone == 0 {
		c.SMS.DailyPerPhone = 10
	}
	if c.SMS.HourlyPerIP == 0 {
		c.SMS.HourlyPerIP = 30
	}
	if c.SMS.BreakerFailures == 0 {
		c.SMS.BreakerFailures = identityDefaultSMSBreakerFailures
	}
	if c.SMS.BreakerTimeout == 0 {
		c.SMS.BreakerTimeout = identityDefaultSMSBreakerTimeout
	}
	for i := range c.OAuth.Providers {
		provider := &c.OAuth.Providers[i]
		if strings.TrimSpace(provider.SubjectField) == "" {
			provider.SubjectField = "sub"
		}
		if provider.JWKSTTL == 0 {
			provider.JWKSTTL = 15 * time.Minute
		}
		if provider.JWKSMinRefreshInterval == 0 {
			provider.JWKSMinRefreshInterval = time.Minute
		}
		if provider.IDTokenLeeway == 0 {
			provider.IDTokenLeeway = 30 * time.Second
		}
	}
	c.ApplySAMLDefaults()
	c.ApplyM2MDefaults()
	c.ApplySCIMDefaults()
}

// ApplySCIMDefaults 填充 SCIM 2.0 默认值（docs/identity-authn.md §21.1.7）。
//
// auto_provision 的「未配置=true」由 IdentitySCIMConfig.AutoProvision 解释，
// 这里不写指针，避免默认值把「显式 false」覆盖成 true。
func (c *IdentityConfig) ApplySCIMDefaults() {
	scimCfg := &c.SCIM
	if scimCfg.PageSize == 0 {
		scimCfg.PageSize = identityDefaultSCIMPageSize
	}
	if scimCfg.MaxPayload == 0 {
		scimCfg.MaxPayload = identityDefaultSCIMMaxPayload
	}
	if scimCfg.RequestTimeout == 0 {
		scimCfg.RequestTimeout = identityDefaultSCIMRequestTimeout
	}
	if scimCfg.TokenGrace == 0 {
		scimCfg.TokenGrace = identityDefaultSCIMTokenGrace
	}
	if scimCfg.RateLimit.RPS == 0 {
		scimCfg.RateLimit.RPS = identityDefaultSCIMRPS
	}
	if scimCfg.RateLimit.Burst == 0 {
		scimCfg.RateLimit.Burst = identityDefaultSCIMBurst
	}
	if scimCfg.RateLimit.FailureMode == "" {
		scimCfg.RateLimit.FailureMode = failureModeClosed
	}
}

// ApplyM2MDefaults 填充 M2M 默认值（docs/identity-authn.md §21.2.6）。
//
// max_secret_age 需大于 rotate_grace；未配置时按比例取默认值，避免
// 「宽限期未结束即判定超龄」的无意义告警。
func (c *IdentityConfig) ApplyM2MDefaults() {
	m2m := &c.M2M
	if m2m.TokenTTL == 0 {
		m2m.TokenTTL = identityDefaultM2MTokenTTL
	}
	if m2m.RotateGrace == 0 {
		m2m.RotateGrace = identityDefaultM2MRotateGrace
	}
	if m2m.MaxSecretAge == 0 {
		m2m.MaxSecretAge = identityDefaultM2MMaxSecretAge
	}
	if m2m.RateLimit.RPS == 0 {
		m2m.RateLimit.RPS = identityDefaultM2MRPS
	}
	if m2m.RateLimit.Burst == 0 {
		m2m.RateLimit.Burst = identityDefaultM2MBurst
	}
	if m2m.RateLimit.FailureMode == "" {
		m2m.RateLimit.FailureMode = failureModeClosed
	}
}

// ApplySAMLDefaults 填充 SAML 默认值；replay_ttl 依赖断言有效期与时钟偏差。
func (c *IdentityConfig) ApplySAMLDefaults() {
	samlCfg := &c.SAML
	if samlCfg.Timeout == 0 {
		samlCfg.Timeout = 5 * time.Second
	}
	if samlCfg.MetadataTTL == 0 {
		samlCfg.MetadataTTL = 15 * time.Minute
	}
	if samlCfg.MetadataMinRefresh == 0 {
		samlCfg.MetadataMinRefresh = time.Minute
	}
	if samlCfg.ClockSkew == 0 {
		samlCfg.ClockSkew = 2 * time.Minute
	}
	if samlCfg.AssertionTTL == 0 {
		samlCfg.AssertionTTL = 5 * time.Minute
	}
	if samlCfg.RequestTTL == 0 {
		samlCfg.RequestTTL = 10 * time.Minute
	}
	if samlCfg.TicketTTL == 0 {
		samlCfg.TicketTTL = time.Minute
	}
	if samlCfg.ReplayTTL == 0 {
		samlCfg.ReplayTTL = samlCfg.AssertionTTL + samlCfg.ClockSkew
	}
	if samlCfg.RateLimit.RPS == 0 {
		samlCfg.RateLimit.RPS = 1
	}
	if samlCfg.RateLimit.Burst == 0 {
		samlCfg.RateLimit.Burst = 5
	}
	if samlCfg.RateLimit.FailureMode == "" {
		samlCfg.RateLimit.FailureMode = failureModeClosed
	}
}
