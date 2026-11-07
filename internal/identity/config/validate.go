package config

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/password"
)

// 已随仓库公开的演练密钥（configs/prod/env.example 与本地 prod-*.yaml）。
//
// 这些值已经进入版本库，等同于泄露；生产启动必须拒绝，避免「复制样例即上线」。
const (
	sampleIdentityHashKey      = "7a1ec0f4290851c052cee9fb3b972bc5a8c14297c65e4301bb320b3e6f426454"
	sampleIdentityMFASecretKey = "a0c1663bfcdb9f923a4d7171709b6b0d36ab2298053a36f4c26cf7a3b5c17c8a" //nolint:gosec // 公开演练值，仅用于拒绝匹配
	sampleSubjectKey           = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// authModeJWT 是身份域强依赖的鉴权模式取值（与平台 auth.mode 保持一致）。
const authModeJWT = "jwt"

// identitySAMLACSPath 是 ACS 端点固定路径；acs_url 的路径必须与之完全一致。
const identitySAMLACSPath = "/c/v1/auth/saml/acs"

// Validate 校验身份域开关、TTL、撤销存储与端别约束（docs/identity-authn.md §10.2）。
//
// 平台段信息（环境、端别、鉴权模式、数据库驱动、Redis/异步开关）由组合层显式传入，
// 域包不反向依赖 platform。未启用时只校验样例密钥，保持历史配置可直接启动。
func (c *IdentityConfig) Validate(env, channel, authMode, dbDriver string, redisEnabled, asyncEnabled bool) error {
	var errs []error
	if c.Enabled {
		errs = append(errs, c.validateEnabled(env, channel, authMode, dbDriver, redisEnabled, asyncEnabled)...)
	}
	errs = append(errs, c.validateSampleCredentials(env)...)
	return errors.Join(errs...)
}

func (c *IdentityConfig) validateEnabled(env, channel, authMode, dbDriver string, redisEnabled, asyncEnabled bool) []error {
	var errs []error
	if authMode != authModeJWT {
		errs = append(errs, errors.New("auth.mode must be jwt when identity.enabled=true"))
	}
	if channel != "admin" && channel != "biz" && channel != "c" {
		// worker 不提供认证 transport；C 端登录自阶段 4 起纳入。
		errs = append(errs, errors.New("identity.enabled is only supported on channel=admin, biz or c"))
	}
	if c.AccessTTL < 5*time.Minute || c.AccessTTL > time.Hour {
		errs = append(errs, errors.New("identity.access_ttl must be in [5m, 1h]"))
	}
	if c.RefreshTTL <= c.AccessTTL {
		errs = append(errs, errors.New("identity.refresh_ttl must be greater than identity.access_ttl"))
	}
	if c.Session.IdleTimeout <= 0 || c.Session.IdleTimeout > c.RefreshTTL {
		errs = append(errs, errors.New("identity.session.idle_timeout must be in (0, identity.refresh_ttl]"))
	}
	// 0 表示由默认值填充（直接构造 Config 的单测路径），不作为非法值处理。
	if c.Session.AbsoluteTTL != 0 && c.Session.AbsoluteTTL <= c.RefreshTTL {
		errs = append(errs, errors.New("identity.session.absolute_ttl must be greater than identity.refresh_ttl"))
	}
	if c.Session.MaxPerAccount < 1 {
		errs = append(errs, errors.New("identity.session.max_per_account must be >= 1"))
	}
	// 0 表示由默认值填充（10s）；宽限窗口必须有安全上限。
	if c.Session.RefreshGrace < 0 || c.Session.RefreshGrace > 60*time.Second {
		errs = append(errs, errors.New("identity.session.refresh_grace must be in [0, 60s]"))
	}
	// 身份域账号与会话必须跨重启保留并跨副本可见（IDN-P2-01）。
	if isProdLikeEnv(env) && dbDriver == "memory" {
		errs = append(errs, errors.New("db.driver must not be memory when identity.enabled=true in prod/staging"))
	}
	// 生产必须把认证审计落库（Outbox），禁止仅写进程日志（IDN-P2-01）。
	if isProdLikeEnv(env) && !asyncEnabled {
		errs = append(errs, errors.New("async.enabled must be true when identity.enabled=true in prod/staging"))
	}
	errs = append(errs, c.validatePassword()...)
	errs = append(errs, c.validateMFA()...)
	if c.Retention.SessionDays < 7 {
		errs = append(errs, errors.New("identity.retention.session_days must be >= 7"))
	}
	if c.Retention.LoginAttemptDays < 180 {
		errs = append(errs, errors.New("identity.retention.login_attempt_days must be >= 180"))
	}
	// 归档保留期必须覆盖在线保留期；负数表示关闭归档，不参与比较。
	if c.Retention.ArchiveDays > 0 && c.Retention.ArchiveDays < c.Retention.SessionDays {
		errs = append(errs, errors.New("identity.retention.archive_days must be >= session_days (negative disables)"))
	}
	if c.Retention.CleanupInterval < time.Minute {
		errs = append(errs, errors.New("identity.retention.cleanup_interval must be >= 1m"))
	}
	switch c.Revocation.Driver {
	case IdentityRevocationDriverRedis:
		if !redisEnabled {
			errs = append(errs, errors.New("redis.addr is required when identity.revocation.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.revocation.driver: %s", c.Revocation.Driver))
	}
	switch c.Revocation.FailureMode {
	case failureModeClosed, failureModeOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.revocation.failure_mode: %s", c.Revocation.FailureMode))
	}
	if isProdLikeEnv(env) && c.Revocation.FailureMode == failureModeOpen {
		errs = append(errs, errors.New("identity.revocation.failure_mode must be fail_closed in prod/staging"))
	}
	// 0 表示由默认值填充（直接构造 Config 的单测路径），不作为非法值处理。
	if c.Revocation.StateTTL != 0 {
		if c.Revocation.StateTTL < identityMinRevocationStateTTL {
			errs = append(errs, errors.New("identity.revocation.state_ttl must be >= 1s"))
		}
		if c.Revocation.StateTTL > identityMaxRevocationStateTTL {
			errs = append(errs, errors.New("identity.revocation.state_ttl must be <= 60s"))
		}
	}
	// 负数表示关闭 L1；正数必须落在 (0, 5s]，避免缓存放大撤销延迟。
	if c.Revocation.LocalCacheTTL > 0 &&
		c.Revocation.LocalCacheTTL > identityMaxRevocationLocalCacheTTL {
		errs = append(errs, errors.New("identity.revocation.local_cache_ttl must be <= 5s (negative disables)"))
	}
	if c.Revocation.LocalCacheCapacity > identityMaxRevocationLocalCacheCapacity {
		errs = append(errs, errors.New("identity.revocation.local_cache_capacity must be <= 100000"))
	}
	if c.Revocation.TokenGrace > identityMaxRevocationTokenGrace {
		errs = append(errs, errors.New("identity.revocation.token_grace must be <= 60s (negative disables)"))
	}
	if window := c.Consumer.DeleteReauthWindow; window > 0 &&
		(window < identityMinConsumerDeleteReauthWindow || window > identityMaxConsumerDeleteReauthWindow) {
		errs = append(errs, errors.New("identity.consumer.delete_reauth_window must be in [1m, 30m]"))
	}
	if c.Lockout.MaxFailures < 1 {
		errs = append(errs, errors.New("identity.lockout.max_failures must be >= 1"))
	}
	if c.Lockout.Window <= 0 {
		errs = append(errs, errors.New("identity.lockout.window must be > 0"))
	}
	if c.Lockout.Duration <= 0 {
		errs = append(errs, errors.New("identity.lockout.duration must be > 0"))
	}
	if c.LoginRateLimit.RPS <= 0 {
		errs = append(errs, errors.New("identity.login_rate_limit.rps must be > 0"))
	}
	if c.LoginRateLimit.Burst < 1 {
		errs = append(errs, errors.New("identity.login_rate_limit.burst must be >= 1"))
	}
	switch c.LoginRateLimit.FailureMode {
	// 空值表示由默认值填充（直接构造 Config 的单测路径）。
	case "", failureModeClosed, failureModeOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.login_rate_limit.failure_mode: %s", c.LoginRateLimit.FailureMode))
	}
	if isProdLikeEnv(env) && c.LoginRateLimit.FailureMode == failureModeOpen {
		errs = append(errs, errors.New("identity.login_rate_limit.failure_mode must be fail_closed in prod/staging"))
	}
	if c.LoginRateLimit.IPRPS < 0 {
		errs = append(errs, errors.New("identity.login_rate_limit.ip_rps must be >= 0"))
	}
	if c.LoginRateLimit.IPBurst < 0 {
		errs = append(errs, errors.New("identity.login_rate_limit.ip_burst must be >= 0"))
	}
	// 0 表示由默认值填充（10 rps / burst 20）；仅拒绝负值与非法枚举。
	if c.RefreshRateLimit.RPS < 0 {
		errs = append(errs, errors.New("identity.refresh_rate_limit.rps must be >= 0"))
	}
	if c.RefreshRateLimit.Burst < 0 {
		errs = append(errs, errors.New("identity.refresh_rate_limit.burst must be >= 0"))
	}
	switch c.RefreshRateLimit.FailureMode {
	case "", failureModeClosed, failureModeOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.refresh_rate_limit.failure_mode: %s", c.RefreshRateLimit.FailureMode))
	}
	if isProdLikeEnv(env) && c.RefreshRateLimit.FailureMode == failureModeOpen {
		errs = append(errs, errors.New("identity.refresh_rate_limit.failure_mode must be fail_closed in prod/staging"))
	}
	// 摘要主密钥：生产必须配置，轮换期的历史密钥只用于双读。
	if isProdLikeEnv(env) && strings.TrimSpace(c.HashKey) == "" {
		errs = append(errs, errors.New("identity.hash_key is required in prod/staging"))
	}
	if c.HashKey != "" || len(c.PreviousHashKeys) > 0 {
		active, keys, hashErr := c.DecodeHashKeys()
		if hashErr != nil {
			errs = append(errs, hashErr)
		} else {
			if !validHashKeyID(active) {
				errs = append(errs, fmt.Errorf("identity.hash_key_id is invalid: %q", active))
			}
			for _, item := range c.PreviousHashKeys {
				if !validHashKeyID(strings.TrimSpace(item.ID)) {
					errs = append(errs, fmt.Errorf("identity.previous_hash_keys id is invalid: %q", item.ID))
				}
			}
			for id := range keys {
				if !validHashKeyID(id) {
					errs = append(errs, fmt.Errorf("identity hash key id is invalid: %q", id))
				}
			}
		}
	}
	// BIZ-03：MFA 端别策略。Channels 为空时仅 admin；biz_required 必须同时启用 biz。
	mfaChannels := make(map[string]struct{}, len(c.MFA.Channels))
	for _, item := range c.MFA.Channels {
		name := strings.ToLower(strings.TrimSpace(item))
		switch name {
		case "admin", "biz":
		default:
			errs = append(errs, fmt.Errorf("unsupported identity.mfa.channels entry: %s", item))
			continue
		}
		if _, dup := mfaChannels[name]; dup {
			errs = append(errs, fmt.Errorf("identity.mfa.channels has duplicate entry: %s", name))
		}
		mfaChannels[name] = struct{}{}
	}
	if len(mfaChannels) == 0 {
		mfaChannels["admin"] = struct{}{}
	}
	_, bizEnabled := mfaChannels["biz"]
	if c.MFA.BizRequired && !bizEnabled {
		errs = append(errs, errors.New("identity.mfa.biz_required requires biz in identity.mfa.channels"))
	}
	if _, enabled := mfaChannels[channel]; !enabled && c.MFA.BizRequired && channel == "biz" {
		errs = append(errs, errors.New("identity.mfa.biz_required requires the current channel=biz to be enabled"))
	}
	switch channel {
	case "admin":
		if isProdLikeEnv(env) && !c.MFA.AdminRequired {
			errs = append(errs, errors.New("identity.mfa.admin_required must be true in prod/staging"))
		}
		if c.MFA.AdminRequired {
			keys, keysErr := c.MFA.DecodeKeys()
			if keysErr != nil {
				errs = append(errs, keysErr)
			}
			key, err := c.MFA.DecodeSecretKey()
			if err != nil {
				errs = append(errs, err)
			}
			if len(keys) == 0 && key == nil {
				errs = append(errs, errors.New("identity.mfa.secret_key or identity.mfa.keys is required when identity.mfa.admin_required=true"))
			}
		}
	case "biz":
		// 显式 fail fast：避免「配置写了强制 MFA，运行时却完全不校验」的假安全。
		if c.MFA.AdminRequired {
			errs = append(errs, errors.New("identity.mfa.admin_required must be false on channel=biz"))
		}
		if c.MFA.BizRequired {
			keys, keysErr := c.MFA.DecodeKeys()
			if keysErr != nil {
				errs = append(errs, keysErr)
			}
			key, err := c.MFA.DecodeSecretKey()
			if err != nil {
				errs = append(errs, err)
			}
			if len(keys) == 0 && key == nil {
				errs = append(errs, errors.New("identity.mfa.secret_key or identity.mfa.keys is required when identity.mfa.biz_required=true"))
			}
		}
	}
	if strings.TrimSpace(c.MFA.Issuer) == "" {
		errs = append(errs, errors.New("identity.mfa.issuer must not be empty"))
	}
	if len(c.MFA.Keys) > 0 {
		keys, keysErr := c.MFA.DecodeKeys()
		if keysErr != nil {
			errs = append(errs, keysErr)
		}
		active := strings.TrimSpace(c.MFA.ActiveKeyID)
		switch active {
		case "":
			errs = append(errs, errors.New("identity.mfa.active_key_id is required when identity.mfa.keys is set"))
		default:
			if _, ok := keys[active]; !ok {
				errs = append(errs, fmt.Errorf("identity.mfa.active_key_id %q is not in identity.mfa.keys", active))
			}
		}
	}
	// PII subject 加密：配置密钥环时必须显式指定 active key，且密钥可解码。
	if len(c.Subject.Keys) > 0 {
		keys, keysErr := c.Subject.DecodeKeys()
		if keysErr != nil {
			errs = append(errs, keysErr)
		}
		active := strings.TrimSpace(c.Subject.ActiveKeyID)
		switch active {
		case "":
			errs = append(errs, errors.New("identity.subject.active_key_id is required when identity.subject.keys is set"))
		default:
			if _, ok := keys[active]; !ok {
				errs = append(errs, fmt.Errorf("identity.subject.active_key_id %q is not in identity.subject.keys", active))
			}
		}
	}
	errs = append(errs, c.validateSMS(env, channel)...)
	errs = append(errs, c.validateOAuth(env, channel)...)
	errs = append(errs, c.validateSAML(env, channel)...)
	return errs
}

// validateSampleCredentials 拒绝生产环境使用仓库内已公开的演练密钥（P1-1）。
func (c *IdentityConfig) validateSampleCredentials(env string) []error {
	if !isProdEnv(env) {
		return nil
	}
	var errs []error
	reject := func(field, value, sample string) {
		if strings.TrimSpace(value) == sample {
			errs = append(errs, fmt.Errorf("%s uses a published sample secret; inject a real value from the secret provider", field))
		}
	}
	reject("identity.hash_key", c.HashKey, sampleIdentityHashKey)
	reject("identity.mfa.secret_key", c.MFA.SecretKey, sampleIdentityMFASecretKey)
	for i, item := range c.Subject.Keys {
		if strings.TrimSpace(item.Key) == sampleSubjectKey {
			errs = append(errs, fmt.Errorf("identity.subject.keys[%d].key uses a published sample secret", i))
		}
	}
	return errs
}

// validatePassword 校验密码策略范围（IDN-P2-01）。
//
// 零值字段按默认策略（12～128）处理；错误只包含字段路径与边界值。
func (c *IdentityConfig) validatePassword() []error {
	var errs []error
	minLength := c.Password.MinLength
	if minLength == 0 {
		minLength = identityDefaultPasswordMinLength
	}
	maxLength := c.Password.MaxLength
	if maxLength == 0 {
		maxLength = identityDefaultPasswordMaxLength
	}
	if minLength < identityMinPasswordLength {
		errs = append(errs, fmt.Errorf("identity.password.min_length must be >= %d", identityMinPasswordLength))
	}
	if maxLength < minLength {
		errs = append(errs, errors.New("identity.password.max_length must be >= identity.password.min_length"))
	}
	if maxLength > password.MaxPasswordBytes {
		errs = append(errs, fmt.Errorf("identity.password.max_length must be <= %d", password.MaxPasswordBytes))
	}
	if c.Password.MaxConcurrent < 0 {
		errs = append(errs, errors.New("identity.password.max_concurrent must be >= 0"))
	}
	// 0 表示由默认值填充（2s）。
	if c.Password.WaitTimeout != 0 {
		if c.Password.WaitTimeout < identityMinPasswordWaitTimeout {
			errs = append(errs, errors.New("identity.password.wait_timeout must be >= 100ms"))
		}
		if c.Password.WaitTimeout > identityMaxPasswordWaitTimeout {
			errs = append(errs, errors.New("identity.password.wait_timeout must be <= 30s"))
		}
	}
	if c.Password.HistoryCount < 0 || c.Password.HistoryCount > 20 {
		errs = append(errs, errors.New("identity.password.history_count must be in [0, 20]"))
	}
	if c.Password.HistoryWindow < 0 || c.Password.HistoryWindow > 5*365*24*time.Hour {
		errs = append(errs, errors.New("identity.password.history_window must be in [0, 5y]"))
	}
	if c.Password.TemporaryLength != 0 {
		if c.Password.TemporaryLength < 16 || c.Password.TemporaryLength > 32 {
			errs = append(errs, errors.New("identity.password.temporary_length must be in [16, 32]"))
		}
	}
	globalComplexity := true
	if c.Password.RequireComplexity != nil {
		globalComplexity = *c.Password.RequireComplexity
	}
	seen := make(map[string]struct{}, len(c.Password.TenantOverrides))
	for _, override := range c.Password.TenantOverrides {
		merchantID := strings.ToLower(strings.TrimSpace(override.MerchantID))
		if merchantID == "" {
			errs = append(errs, errors.New("identity.password.tenant_overrides.merchant_id is required"))
			continue
		}
		if _, dup := seen[merchantID]; dup {
			errs = append(errs, fmt.Errorf("identity.password.tenant_overrides has duplicate merchant_id: %s", merchantID))
			continue
		}
		seen[merchantID] = struct{}{}
		tenantMin := override.MinLength
		if tenantMin == 0 {
			tenantMin = minLength
		}
		tenantMax := override.MaxLength
		if tenantMax == 0 {
			tenantMax = maxLength
		}
		if tenantMin < minLength {
			errs = append(errs, fmt.Errorf("identity.password.tenant_overrides[%s].min_length must be >= global min_length", merchantID))
		}
		if tenantMax > maxLength || tenantMax < tenantMin {
			errs = append(errs, fmt.Errorf("identity.password.tenant_overrides[%s].max_length must be within [min_length, global max_length]", merchantID))
		}
		if override.RequireComplexity != nil && globalComplexity && !*override.RequireComplexity {
			errs = append(errs, fmt.Errorf("identity.password.tenant_overrides[%s] cannot relax require_complexity", merchantID))
		}
	}
	return errs
}

// validateMFA 校验 TOTP 与恢复码参数（MNT-05）。
func (c *IdentityConfig) validateMFA() []error {
	mfaCfg := c.MFA
	var errs []error
	// BIZ-03：因子白名单只接受已实现的 totp；空配置按默认 totp 处理。
	factors := make(map[string]struct{}, len(mfaCfg.Factors))
	for _, factor := range mfaCfg.Factors {
		name := strings.ToLower(strings.TrimSpace(factor))
		if name != "totp" {
			errs = append(errs, fmt.Errorf("unsupported identity.mfa.factors entry: %s", factor))
			continue
		}
		if _, dup := factors[name]; dup {
			errs = append(errs, fmt.Errorf("identity.mfa.factors has duplicate entry: %s", name))
		}
		factors[name] = struct{}{}
	}
	if len(factors) == 0 {
		factors["totp"] = struct{}{}
	}
	if mfaCfg.TOTPDigits != 0 && mfaCfg.TOTPDigits != 6 && mfaCfg.TOTPDigits != 8 {
		errs = append(errs, errors.New("identity.mfa.totp_digits must be 6 or 8"))
	}
	if mfaCfg.TOTPStep != 0 && (mfaCfg.TOTPStep < 15*time.Second || mfaCfg.TOTPStep > 120*time.Second) {
		errs = append(errs, errors.New("identity.mfa.totp_step must be in [15s, 120s]"))
	}
	if mfaCfg.TOTPWindow < 0 || mfaCfg.TOTPWindow > 2 {
		errs = append(errs, errors.New("identity.mfa.totp_window must be in [0, 2]"))
	}
	if mfaCfg.RecoveryCodeCount != 0 && (mfaCfg.RecoveryCodeCount < 5 || mfaCfg.RecoveryCodeCount > 20) {
		errs = append(errs, errors.New("identity.mfa.recovery_code_count must be in [5, 20]"))
	}
	if mfaCfg.StepUpReuseGrace < -1 || mfaCfg.StepUpReuseGrace > 10*time.Minute {
		errs = append(errs, errors.New("identity.mfa.step_up_reuse_grace must be in [-1, 10m]"))
	}
	return errs
}

// validateSMS 校验短信验证码参数（docs/identity-authn.md §8.1 / §10.2）。
func (c *IdentityConfig) validateSMS(env, channel string) []error {
	sms := c.SMS
	var errs []error
	if sms.Enabled && channel != "c" {
		errs = append(errs, errors.New("identity.sms.enabled is only supported on channel=c"))
	}
	// 空值等价于默认 memory（与 ApplyDefaults 保持一致）。
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
	if isProdLikeEnv(env) && sms.DailyBudget == 0 {
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
	if isProdLikeEnv(env) && driver == IdentitySMSDriverMemory {
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

// validateOAuth 校验 OAuth 提供方参数（docs/identity-authn.md §8.2 / §10.2）。
func (c *IdentityConfig) validateOAuth(env, channel string) []error {
	providers := c.OAuth.Providers
	var errs []error
	if len(providers) > 0 && channel != "c" {
		errs = append(errs, errors.New("identity.oauth.providers is only supported on channel=c"))
	}
	if channel == "c" && isProdLikeEnv(env) &&
		!c.SMS.Enabled && len(providers) == 0 &&
		(!c.SAML.Enabled || len(c.SAML.Providers) == 0) {
		errs = append(errs, errors.New("channel=c requires identity.sms.enabled, at least one identity.oauth.providers, or identity.saml providers in prod/staging"))
	}
	if len(providers) > 0 {
		rate := c.OAuth.RateLimit
		// 0 表示由默认值填充（1 rps / burst 5），直接构造 Config 的单测路径依赖该语义。
		if rate.RPS < 0 {
			errs = append(errs, errors.New("identity.oauth.rate_limit.rps must be >= 0"))
		}
		if rate.Burst < 0 {
			errs = append(errs, errors.New("identity.oauth.rate_limit.burst must be >= 0"))
		}
		switch rate.FailureMode {
		case "", failureModeClosed, failureModeOpen:
		default:
			errs = append(errs, fmt.Errorf("unsupported identity.oauth.rate_limit.failure_mode: %s", rate.FailureMode))
		}
		if isProdLikeEnv(env) && rate.FailureMode == failureModeOpen {
			errs = append(errs, errors.New("identity.oauth.rate_limit.failure_mode must be fail_closed in prod/staging"))
		}
		// 0 表示默认值；显式配置时校验范围。
		if c.OAuth.Timeout != 0 &&
			(c.OAuth.Timeout < time.Second || c.OAuth.Timeout > 30*time.Second) {
			errs = append(errs, errors.New("identity.oauth.timeout must be in [1s, 30s]"))
		}
		if c.OAuth.BreakerFailures < 0 {
			errs = append(errs, errors.New("identity.oauth.breaker_failures must be >= 0"))
		}
		if c.OAuth.BreakerTimeout != 0 &&
			(c.OAuth.BreakerTimeout < time.Second || c.OAuth.BreakerTimeout > 10*time.Minute) {
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
			if err := validateAbsoluteURL(raw, isProdLikeEnv(env)); err != nil {
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

// validateSAML 校验 SAML 参数（docs/identity-authn.md §8.3 / §10）。
//
// 零值时长按默认值处理，便于直接构造 Config 的单测路径；显式配置时校验范围。
func (c *IdentityConfig) validateSAML(env, channel string) []error {
	samlCfg := c.SAML
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
	if channel != "c" {
		errs = append(errs, errors.New("identity.saml is only supported on channel=c"))
	}

	prod := isProdLikeEnv(env)
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
	case "", failureModeClosed, failureModeOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.saml.rate_limit.failure_mode: %s", rate.FailureMode))
	}
	if prod && rate.FailureMode == failureModeOpen {
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

// validHashKeyID 校验密钥 ID：非空、≤16 字符、仅字母数字下划线短横线。
func validHashKeyID(id string) bool {
	if id == "" || len(id) > 16 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// isProdEnv 报告环境是否为生产。
func isProdEnv(env string) bool {
	return strings.EqualFold(strings.TrimSpace(env), "prod")
}

// isProdLikeEnv 报告环境是否按生产标准校验（prod / staging）。
func isProdLikeEnv(env string) bool {
	return isProdEnv(env) || strings.EqualFold(strings.TrimSpace(env), "staging")
}

// parseSAMLKeyPair 解析并校验 SP 证书 / 私钥：配对、RSA ≥ 2048、证书未过期。
func parseSAMLKeyPair(certPEM, keyPEM string) (*x509.Certificate, *rsa.PrivateKey, error) {
	if strings.TrimSpace(certPEM) == "" || strings.TrimSpace(keyPEM) == "" {
		return nil, nil, errors.New("certificate and private key are required")
	}
	certBlock, _ := pem.Decode([]byte(certPEM))
	if certBlock == nil {
		return nil, nil, errors.New("certificate must be PEM encoded")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse certificate: %w", err)
	}
	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		return nil, nil, errors.New("private key must be PEM encoded")
	}
	var key *rsa.PrivateKey
	if parsed, parseErr := x509.ParsePKCS1PrivateKey(keyBlock.Bytes); parseErr == nil {
		key = parsed
	} else {
		parsedPKCS8, parseErr8 := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if parseErr8 != nil {
			return nil, nil, errors.New("private key must be PKCS#1 or PKCS#8 RSA")
		}
		rsaKey, ok := parsedPKCS8.(*rsa.PrivateKey)
		if !ok {
			return nil, nil, errors.New("private key must be RSA")
		}
		key = rsaKey
	}
	if key.N.BitLen() < 2048 {
		return nil, nil, errors.New("private key must be RSA >= 2048 bits")
	}
	public, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || public.N.Cmp(key.N) != 0 || public.E != key.E {
		return nil, nil, errors.New("certificate does not match private key")
	}
	if time.Now().After(cert.NotAfter) {
		return nil, nil, fmt.Errorf("certificate expired at %s", cert.NotAfter.UTC().Format(time.RFC3339))
	}
	return cert, key, nil
}

// isOAuthProviderName 校验 provider 名：小写字母开头，允许 [a-z0-9_-]，长度 2-32。
func isOAuthProviderName(name string) bool {
	if len(name) < 2 || len(name) > 32 {
		return false
	}
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := 1; i < len(name); i++ {
		ch := name[i]
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

// validateAbsoluteURL 校验绝对 URL；requireHTTPS 为 true 时只接受 https。
func validateAbsoluteURL(raw string, requireHTTPS bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("must be an absolute URL")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if requireHTTPS {
			return errors.New("must use https in prod/staging")
		}
		return nil
	default:
		return errors.New("scheme must be http or https")
	}
}

// effectiveSAMLDuration 返回生效时长：零值使用默认值。
func effectiveSAMLDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}
