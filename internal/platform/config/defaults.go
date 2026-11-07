package config

import (
	"strings"
	"time"
)

// applyDefaults 在解码文件前填充默认值。
//
// 只处理与其它字段无关的常量默认值；跨字段派生放在 normalize，
// 以免被文件或环境变量覆盖后失去一致性。
func (c *Config) applyDefaults() {
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 15 * time.Second
	}
	if c.HTTP.ReadHeaderTimeout == 0 {
		c.HTTP.ReadHeaderTimeout = 5 * time.Second
	}
	if c.HTTP.ReadTimeout == 0 {
		c.HTTP.ReadTimeout = 15 * time.Second
	}
	if c.HTTP.WriteTimeout == 0 {
		c.HTTP.WriteTimeout = 30 * time.Second
	}
	if c.HTTP.IdleTimeout == 0 {
		c.HTTP.IdleTimeout = 60 * time.Second
	}
	if c.HTTP.MaxHeaderBytes == 0 {
		c.HTTP.MaxHeaderBytes = 1 << 20
	}
	if c.HTTP.Timeout == 0 {
		c.HTTP.Timeout = 30 * time.Second
	}
	if c.HTTP.BodyLimitBytes == 0 {
		c.HTTP.BodyLimitBytes = 32 << 20
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
	if c.DB.Driver == "" {
		c.DB.Driver = "memory"
	}
	if c.Storage.Driver == "" {
		c.Storage.Driver = "memory"
	}
	if c.Worker.PollInterval == 0 {
		c.Worker.PollInterval = 5 * time.Second
	}
	if c.Tracing.Exporter == "" {
		c.Tracing.Exporter = "none"
	}
	if c.ID.NodeIDSource == "" {
		c.ID.NodeIDSource = IDSourceEnv
	}
	if c.Idempotency.Driver == "" {
		c.Idempotency.Driver = IdempotencyDriverMemory
	}
	if c.Idempotency.Header == "" {
		c.Idempotency.Header = "Idempotency-Key"
	}
	if c.Idempotency.TTL == 0 {
		c.Idempotency.TTL = 24 * time.Hour
	}
	if c.Idempotency.MaxBodyBytes == 0 {
		c.Idempotency.MaxBodyBytes = 1 << 20
	}
	if c.RateLimit.Driver == "" {
		c.RateLimit.Driver = RateLimitDriverMemory
	}
	if c.RateLimit.Key == "" {
		c.RateLimit.Key = RateLimitKeyChannel
	}
	if c.RateLimit.FailureMode == "" {
		c.RateLimit.FailureMode = RateLimitFailureOpen
	}
	if c.RateLimit.IdleTTL == 0 {
		c.RateLimit.IdleTTL = 10 * time.Minute
	}
	c.applyAsyncDefaults()
	c.applyIAMDefaults()
	c.applyIdentityDefaults()
	c.applyAuditDefaults()
}

// applyAuditDefaults 填充审计默认值；缺省组合在 prod 仍会被校验拒绝。
func (c *Config) applyAuditDefaults() {
	if strings.TrimSpace(c.Audit.Store) == "" {
		c.Audit.Store = AuditStoreAuto
	}
	if c.Audit.RetentionDays == 0 {
		c.Audit.RetentionDays = AuditMinRetentionDays
	}
	if c.Audit.Query.MaxRangeDays == 0 {
		c.Audit.Query.MaxRangeDays = 31
	}
	if c.Audit.Query.DefaultPageSize == 0 {
		c.Audit.Query.DefaultPageSize = 20
	}
	if c.Audit.Query.MaxPageSize == 0 {
		c.Audit.Query.MaxPageSize = 100
	}
	if c.Audit.Export.MaxRangeDays == 0 {
		c.Audit.Export.MaxRangeDays = 31
	}
	if c.Audit.Export.MaxRows == 0 {
		c.Audit.Export.MaxRows = 50000
	}
	if c.Audit.Export.TTLDays == 0 {
		c.Audit.Export.TTLDays = 7
	}
	if c.Audit.Export.RetryMax == 0 {
		c.Audit.Export.RetryMax = 3
	}
	if c.Audit.Export.PollInterval == 0 {
		c.Audit.Export.PollInterval = 10 * time.Second
	}
	if c.Audit.Export.StaleTimeout == 0 {
		c.Audit.Export.StaleTimeout = 10 * time.Minute
	}
	if c.Audit.Retention.BatchSize == 0 {
		c.Audit.Retention.BatchSize = 5000
	}
	if c.Audit.Retention.CleanupInterval == 0 {
		c.Audit.Retention.CleanupInterval = 24 * time.Hour
	}
}

// applyIdentityDefaults 填充身份域默认值；Enabled 保持 false（默认关闭）。
func (c *Config) applyIdentityDefaults() {
	if c.Identity.AccessTTL == 0 {
		c.Identity.AccessTTL = 15 * time.Minute
	}
	if c.Identity.RefreshTTL == 0 {
		c.Identity.RefreshTTL = 168 * time.Hour
	}
	if c.Identity.Session.IdleTimeout == 0 {
		c.Identity.Session.IdleTimeout = 72 * time.Hour
	}
	if c.Identity.Session.AbsoluteTTL == 0 {
		c.Identity.Session.AbsoluteTTL = 720 * time.Hour
	}
	if c.Identity.Session.MaxPerAccount == 0 {
		c.Identity.Session.MaxPerAccount = 10
	}
	if c.Identity.Session.RefreshGrace == 0 {
		c.Identity.Session.RefreshGrace = 10 * time.Second
	}
	if c.Identity.Retention.SessionDays == 0 {
		c.Identity.Retention.SessionDays = 30
	}
	if c.Identity.Retention.LoginAttemptDays == 0 {
		c.Identity.Retention.LoginAttemptDays = 180
	}
	// 0 使用默认 365 天；负数表示显式关闭归档。
	if c.Identity.Retention.ArchiveDays == 0 {
		c.Identity.Retention.ArchiveDays = 365
	}
	if c.Identity.Retention.CleanupInterval == 0 {
		c.Identity.Retention.CleanupInterval = time.Hour
	}
	if c.Identity.Revocation.Driver == "" {
		c.Identity.Revocation.Driver = IdentityRevocationDriverRedis
	}
	if c.Identity.Revocation.FailureMode == "" {
		c.Identity.Revocation.FailureMode = RateLimitFailureClosed
	}
	if c.Identity.Revocation.StateTTL == 0 {
		// 默认 min(access_ttl + 1m, 60s)：常规 15m access_ttl 下即 60s。
		accessLeeway := c.Identity.AccessTTL + time.Minute
		if accessLeeway > identityMaxRevocationStateTTL {
			accessLeeway = identityMaxRevocationStateTTL
		}
		c.Identity.Revocation.StateTTL = accessLeeway
	}
	// 负数表示显式关闭 L1；0 使用默认 1s，避免「未配置」被误读为关闭。
	if c.Identity.Revocation.LocalCacheTTL == 0 {
		c.Identity.Revocation.LocalCacheTTL = identityDefaultRevocationLocalCacheTTL
	}
	if c.Identity.Revocation.LocalCacheCapacity <= 0 {
		c.Identity.Revocation.LocalCacheCapacity = identityDefaultRevocationLocalCacheCapacity
	}
	// jti 宽限：0 使用默认 30s；负数表示显式关闭。
	if c.Identity.Revocation.TokenGrace == 0 {
		c.Identity.Revocation.TokenGrace = identityDefaultRevocationTokenGrace
	}
	// 自助注销再认证窗口：0 使用默认 10m。
	if c.Identity.Consumer.DeleteReauthWindow == 0 {
		c.Identity.Consumer.DeleteReauthWindow = identityDefaultConsumerDeleteReauthWindow
	}
	if c.Identity.Lockout.MaxFailures == 0 {
		c.Identity.Lockout.MaxFailures = 5
	}
	if c.Identity.Lockout.Window == 0 {
		c.Identity.Lockout.Window = 15 * time.Minute
	}
	if c.Identity.Lockout.Duration == 0 {
		c.Identity.Lockout.Duration = 15 * time.Minute
	}
	if c.Identity.LoginRateLimit.RPS == 0 {
		c.Identity.LoginRateLimit.RPS = 1
	}
	if c.Identity.LoginRateLimit.Burst == 0 {
		c.Identity.LoginRateLimit.Burst = 5
	}
	if c.Identity.LoginRateLimit.FailureMode == "" {
		c.Identity.LoginRateLimit.FailureMode = RateLimitFailureClosed
	}
	if c.Identity.LoginRateLimit.IPRPS == 0 {
		c.Identity.LoginRateLimit.IPRPS = 20
	}
	if c.Identity.LoginRateLimit.IPBurst == 0 {
		c.Identity.LoginRateLimit.IPBurst = 40
	}
	if c.Identity.RefreshRateLimit.RPS == 0 {
		c.Identity.RefreshRateLimit.RPS = 10
	}
	if c.Identity.RefreshRateLimit.Burst == 0 {
		c.Identity.RefreshRateLimit.Burst = 20
	}
	if c.Identity.RefreshRateLimit.FailureMode == "" {
		c.Identity.RefreshRateLimit.FailureMode = RateLimitFailureClosed
	}
	if c.Identity.HashKeyID == "" && strings.TrimSpace(c.Identity.HashKey) != "" {
		c.Identity.HashKeyID = "v1"
	}
	if c.Identity.Password.WaitTimeout == 0 {
		c.Identity.Password.WaitTimeout = identityDefaultPasswordWaitTimeout
	}
	if c.Identity.Password.HistoryCount == 0 {
		c.Identity.Password.HistoryCount = 5
	}
	if c.Identity.Password.HistoryWindow == 0 {
		c.Identity.Password.HistoryWindow = 365 * 24 * time.Hour
	}
	if c.Identity.OAuth.RateLimit.RPS == 0 {
		c.Identity.OAuth.RateLimit.RPS = 1
	}
	if c.Identity.OAuth.RateLimit.Burst == 0 {
		c.Identity.OAuth.RateLimit.Burst = 5
	}
	if c.Identity.OAuth.RateLimit.FailureMode == "" {
		c.Identity.OAuth.RateLimit.FailureMode = RateLimitFailureClosed
	}
	if c.Identity.OAuth.Timeout == 0 {
		c.Identity.OAuth.Timeout = 5 * time.Second
	}
	if c.Identity.OAuth.BreakerFailures == 0 {
		c.Identity.OAuth.BreakerFailures = 5
	}
	if c.Identity.OAuth.BreakerTimeout == 0 {
		c.Identity.OAuth.BreakerTimeout = 60 * time.Second
	}
	if c.Identity.MFA.Issuer == "" {
		c.Identity.MFA.Issuer = identityDefaultMFAIssuer
	}
	if c.Identity.SMS.Driver == "" {
		c.Identity.SMS.Driver = IdentitySMSDriverMemory
	}
	if c.Identity.SMS.CodeTTL == 0 {
		c.Identity.SMS.CodeTTL = 5 * time.Minute
	}
	if c.Identity.SMS.MaxAttempts == 0 {
		c.Identity.SMS.MaxAttempts = 5
	}
	if c.Identity.SMS.ResendInterval == 0 {
		c.Identity.SMS.ResendInterval = time.Minute
	}
	if c.Identity.SMS.DailyPerPhone == 0 {
		c.Identity.SMS.DailyPerPhone = 10
	}
	if c.Identity.SMS.HourlyPerIP == 0 {
		c.Identity.SMS.HourlyPerIP = 30
	}
	if c.Identity.SMS.BreakerFailures == 0 {
		c.Identity.SMS.BreakerFailures = identityDefaultSMSBreakerFailures
	}
	if c.Identity.SMS.BreakerTimeout == 0 {
		c.Identity.SMS.BreakerTimeout = identityDefaultSMSBreakerTimeout
	}
	for i := range c.Identity.OAuth.Providers {
		provider := &c.Identity.OAuth.Providers[i]
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
	c.applySAMLDefaults()
}

// applySAMLDefaults 填充 SAML 默认值；replay_ttl 依赖已填充的断言有效期与时钟偏差。
func (c *Config) applySAMLDefaults() {
	samlCfg := &c.Identity.SAML
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
		samlCfg.RateLimit.FailureMode = RateLimitFailureClosed
	}
}

// applyAsyncDefaults 填充异步运行时默认值（默认关闭，保持现有行为）。
func (c *Config) applyAsyncDefaults() {
	if c.Async.Driver == "" {
		c.Async.Driver = AsyncDriverDB
	}
	if c.Async.Outbox.PollInterval == 0 {
		c.Async.Outbox.PollInterval = time.Second
	}
	if c.Async.Outbox.BatchSize == 0 {
		c.Async.Outbox.BatchSize = 100
	}
	if c.Async.Outbox.Lease == 0 {
		c.Async.Outbox.Lease = 30 * time.Second
	}
	if c.Async.Outbox.MaxAttempts == 0 {
		c.Async.Outbox.MaxAttempts = 10
	}
	if c.Async.Outbox.Concurrency == 0 {
		c.Async.Outbox.Concurrency = 2
	}
	if c.Async.Consumer.Concurrency == 0 {
		c.Async.Consumer.Concurrency = 4
	}
	if c.Async.Consumer.VisibilityTimeout == 0 {
		c.Async.Consumer.VisibilityTimeout = 60 * time.Second
	}
	if c.Async.Consumer.RetryBackoff == "" {
		c.Async.Consumer.RetryBackoff = AsyncBackoffExponential
	}
	if c.Async.Broker.Stream == "" {
		c.Async.Broker.Stream = "async"
	}
	if c.Async.Broker.SubjectPrefix == "" {
		c.Async.Broker.SubjectPrefix = "async"
	}
	if c.Async.Broker.AckWait == 0 {
		c.Async.Broker.AckWait = 30 * time.Second
	}
	if c.Async.Broker.MaxDeliver == 0 {
		c.Async.Broker.MaxDeliver = 10
	}
}

// applyIAMDefaults 填充 IAM 默认值；Mode 保持空串，由 normalize 统一为 off。
func (c *Config) applyIAMDefaults() {
	if c.IAM.LoadTimeout == 0 {
		c.IAM.LoadTimeout = 300 * time.Millisecond
	}
	if c.IAM.Cache.Driver == "" {
		c.IAM.Cache.Driver = IAMCacheDriverMemory
	}
	if c.IAM.Cache.L1TTL == 0 {
		c.IAM.Cache.L1TTL = 3 * time.Second
	}
	if c.IAM.Cache.L2TTL == 0 {
		c.IAM.Cache.L2TTL = 5 * time.Minute
	}
	if c.IAM.Cache.NegativeTTL == 0 {
		c.IAM.Cache.NegativeTTL = 30 * time.Second
	}
	if c.IAM.Cache.KeyPrefix == "" {
		c.IAM.Cache.KeyPrefix = "iam"
	}
	if c.IAM.Cache.L1MaxEntries == 0 {
		c.IAM.Cache.L1MaxEntries = 10000
	}
	if c.IAM.Limits.MaxRolesPerSubject == 0 {
		c.IAM.Limits.MaxRolesPerSubject = 20
	}
	if c.IAM.Limits.MaxPermissionsPerRole == 0 {
		c.IAM.Limits.MaxPermissionsPerRole = 200
	}
	if c.IAM.Limits.MaxBindingsPerSubject == 0 {
		c.IAM.Limits.MaxBindingsPerSubject = 50
	}
	if c.IAM.Limits.MaxCustomRolesPerMerchant == 0 {
		c.IAM.Limits.MaxCustomRolesPerMerchant = 50
	}
	if c.IAM.Certification.Enabled == nil {
		enabled := true
		c.IAM.Certification.Enabled = &enabled
	}
}

// normalize 在环境变量覆盖后、Validate 前补齐跨字段派生值。
//
// auth.expected_channel 未显式声明时跟随 channel，保证环境变量改写
// channel 后两者仍然一致；auth.mode 未声明时按环境回退（CFG-04）。
func (c *Config) normalize() {
	if c.Auth.ExpectedChannel == "" {
		c.Auth.ExpectedChannel = c.Channel
	}
	c.Auth.Mode = c.Auth.effectiveMode(c.Env)
	c.IAM.Mode = normalizeIAMMode(c.IAM.Mode)
	c.IAM.Constraints.Mode = normalizeIAMConstraintMode(c.IAM.Constraints.Mode)
	// 密钥解析放在最后：它覆盖的是其它字段的取值，必须在派生逻辑稳定之后执行。
	c.resolveSecrets()
}

// normalizeIAMConstraintMode 归一化 RBAC2 约束模式；空串视为 shadow（影子期默认）。
func normalizeIAMConstraintMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return IAMConstraintsModeShadow
	}
	return mode
}

// normalizeIAMMode 归一化 iam.mode；空串视为 off（保持历史静态权限表行为）。
func normalizeIAMMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return IAMModeOff
	}
	return mode
}
