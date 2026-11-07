package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/password"
)

// validateIdentity 校验身份域开关、TTL、撤销存储与端别约束（docs/identity-authn.md §10.2）。
//
// 未启用时不校验派生字段，保持历史配置可直接启动；错误只包含字段路径。
func (c *Config) validateIdentity() []error {
	if !c.Identity.Enabled {
		return nil
	}
	var errs []error
	if c.Auth.effectiveMode(c.Env) != AuthModeJWT {
		errs = append(errs, errors.New("auth.mode must be jwt when identity.enabled=true"))
	}
	if c.Channel != "admin" && c.Channel != "biz" && c.Channel != "c" {
		// worker 不提供认证 transport；C 端登录自阶段 4 起纳入。
		errs = append(errs, errors.New("identity.enabled is only supported on channel=admin, biz or c"))
	}
	if c.Identity.AccessTTL < 5*time.Minute || c.Identity.AccessTTL > time.Hour {
		errs = append(errs, errors.New("identity.access_ttl must be in [5m, 1h]"))
	}
	if c.Identity.RefreshTTL <= c.Identity.AccessTTL {
		errs = append(errs, errors.New("identity.refresh_ttl must be greater than identity.access_ttl"))
	}
	if c.Identity.Session.IdleTimeout <= 0 || c.Identity.Session.IdleTimeout > c.Identity.RefreshTTL {
		errs = append(errs, errors.New("identity.session.idle_timeout must be in (0, identity.refresh_ttl]"))
	}
	// 0 表示由默认值填充（直接构造 Config 的单测路径），不作为非法值处理。
	if c.Identity.Session.AbsoluteTTL != 0 && c.Identity.Session.AbsoluteTTL <= c.Identity.RefreshTTL {
		errs = append(errs, errors.New("identity.session.absolute_ttl must be greater than identity.refresh_ttl"))
	}
	if c.Identity.Session.MaxPerAccount < 1 {
		errs = append(errs, errors.New("identity.session.max_per_account must be >= 1"))
	}
	// 0 表示由默认值填充（10s）；宽限窗口必须有安全上限。
	if c.Identity.Session.RefreshGrace < 0 || c.Identity.Session.RefreshGrace > 60*time.Second {
		errs = append(errs, errors.New("identity.session.refresh_grace must be in [0, 60s]"))
	}
	// 身份域账号与会话必须跨重启保留并跨副本可见（IDN-P2-01）。
	if isProdLikeEnv(c.Env) && c.DB.Driver == "memory" {
		errs = append(errs, errors.New("db.driver must not be memory when identity.enabled=true in prod/staging"))
	}
	// 生产必须把认证审计落库（Outbox），禁止仅写进程日志（IDN-P2-01）。
	if isProdLikeEnv(c.Env) && !c.Async.Enabled {
		errs = append(errs, errors.New("async.enabled must be true when identity.enabled=true in prod/staging"))
	}
	errs = append(errs, c.validateIdentityPassword()...)
	errs = append(errs, c.validateIdentityMFA()...)
	if c.Identity.Retention.SessionDays < 7 {
		errs = append(errs, errors.New("identity.retention.session_days must be >= 7"))
	}
	if c.Identity.Retention.LoginAttemptDays < 180 {
		errs = append(errs, errors.New("identity.retention.login_attempt_days must be >= 180"))
	}
	// 归档保留期必须覆盖在线保留期；负数表示关闭归档，不参与比较。
	if c.Identity.Retention.ArchiveDays > 0 && c.Identity.Retention.ArchiveDays < c.Identity.Retention.SessionDays {
		errs = append(errs, errors.New("identity.retention.archive_days must be >= session_days (negative disables)"))
	}
	if c.Identity.Retention.CleanupInterval < time.Minute {
		errs = append(errs, errors.New("identity.retention.cleanup_interval must be >= 1m"))
	}
	switch c.Identity.Revocation.Driver {
	case IdentityRevocationDriverRedis:
		if !c.Redis.Enabled() {
			errs = append(errs, errors.New("redis.addr is required when identity.revocation.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.revocation.driver: %s", c.Identity.Revocation.Driver))
	}
	switch c.Identity.Revocation.FailureMode {
	case RateLimitFailureClosed, RateLimitFailureOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.revocation.failure_mode: %s", c.Identity.Revocation.FailureMode))
	}
	if isProdLikeEnv(c.Env) && c.Identity.Revocation.FailureMode == RateLimitFailureOpen {
		errs = append(errs, errors.New("identity.revocation.failure_mode must be fail_closed in prod/staging"))
	}
	// 0 表示由默认值填充（直接构造 Config 的单测路径），不作为非法值处理。
	if c.Identity.Revocation.StateTTL != 0 {
		if c.Identity.Revocation.StateTTL < identityMinRevocationStateTTL {
			errs = append(errs, errors.New("identity.revocation.state_ttl must be >= 1s"))
		}
		if c.Identity.Revocation.StateTTL > identityMaxRevocationStateTTL {
			errs = append(errs, errors.New("identity.revocation.state_ttl must be <= 60s"))
		}
	}
	// 负数表示关闭 L1；正数必须落在 (0, 5s]，避免缓存放大撤销延迟。
	if c.Identity.Revocation.LocalCacheTTL > 0 &&
		c.Identity.Revocation.LocalCacheTTL > identityMaxRevocationLocalCacheTTL {
		errs = append(errs, errors.New("identity.revocation.local_cache_ttl must be <= 5s (negative disables)"))
	}
	if c.Identity.Revocation.LocalCacheCapacity > identityMaxRevocationLocalCacheCapacity {
		errs = append(errs, errors.New("identity.revocation.local_cache_capacity must be <= 100000"))
	}
	if c.Identity.Revocation.TokenGrace > identityMaxRevocationTokenGrace {
		errs = append(errs, errors.New("identity.revocation.token_grace must be <= 60s (negative disables)"))
	}
	if window := c.Identity.Consumer.DeleteReauthWindow; window > 0 &&
		(window < identityMinConsumerDeleteReauthWindow || window > identityMaxConsumerDeleteReauthWindow) {
		errs = append(errs, errors.New("identity.consumer.delete_reauth_window must be in [1m, 30m]"))
	}
	if c.Identity.Lockout.MaxFailures < 1 {
		errs = append(errs, errors.New("identity.lockout.max_failures must be >= 1"))
	}
	if c.Identity.Lockout.Window <= 0 {
		errs = append(errs, errors.New("identity.lockout.window must be > 0"))
	}
	if c.Identity.Lockout.Duration <= 0 {
		errs = append(errs, errors.New("identity.lockout.duration must be > 0"))
	}
	if c.Identity.LoginRateLimit.RPS <= 0 {
		errs = append(errs, errors.New("identity.login_rate_limit.rps must be > 0"))
	}
	if c.Identity.LoginRateLimit.Burst < 1 {
		errs = append(errs, errors.New("identity.login_rate_limit.burst must be >= 1"))
	}
	switch c.Identity.LoginRateLimit.FailureMode {
	// 空值表示由默认值填充（直接构造 Config 的单测路径）。
	case "", RateLimitFailureClosed, RateLimitFailureOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.login_rate_limit.failure_mode: %s", c.Identity.LoginRateLimit.FailureMode))
	}
	if isProdLikeEnv(c.Env) && c.Identity.LoginRateLimit.FailureMode == RateLimitFailureOpen {
		errs = append(errs, errors.New("identity.login_rate_limit.failure_mode must be fail_closed in prod/staging"))
	}
	if c.Identity.LoginRateLimit.IPRPS < 0 {
		errs = append(errs, errors.New("identity.login_rate_limit.ip_rps must be >= 0"))
	}
	if c.Identity.LoginRateLimit.IPBurst < 0 {
		errs = append(errs, errors.New("identity.login_rate_limit.ip_burst must be >= 0"))
	}
	// 0 表示由默认值填充（10 rps / burst 20）；仅拒绝负值与非法枚举。
	if c.Identity.RefreshRateLimit.RPS < 0 {
		errs = append(errs, errors.New("identity.refresh_rate_limit.rps must be >= 0"))
	}
	if c.Identity.RefreshRateLimit.Burst < 0 {
		errs = append(errs, errors.New("identity.refresh_rate_limit.burst must be >= 0"))
	}
	switch c.Identity.RefreshRateLimit.FailureMode {
	case "", RateLimitFailureClosed, RateLimitFailureOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.refresh_rate_limit.failure_mode: %s", c.Identity.RefreshRateLimit.FailureMode))
	}
	if isProdLikeEnv(c.Env) && c.Identity.RefreshRateLimit.FailureMode == RateLimitFailureOpen {
		errs = append(errs, errors.New("identity.refresh_rate_limit.failure_mode must be fail_closed in prod/staging"))
	}
	// 摘要主密钥：生产必须配置，轮换期的历史密钥只用于双读。
	if isProdLikeEnv(c.Env) && strings.TrimSpace(c.Identity.HashKey) == "" {
		errs = append(errs, errors.New("identity.hash_key is required in prod/staging"))
	}
	if c.Identity.HashKey != "" || len(c.Identity.PreviousHashKeys) > 0 {
		active, keys, hashErr := c.Identity.DecodeHashKeys()
		if hashErr != nil {
			errs = append(errs, hashErr)
		} else {
			if !validHashKeyID(active) {
				errs = append(errs, fmt.Errorf("identity.hash_key_id is invalid: %q", active))
			}
			for _, item := range c.Identity.PreviousHashKeys {
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
	mfaChannels := make(map[string]struct{}, len(c.Identity.MFA.Channels))
	for _, channel := range c.Identity.MFA.Channels {
		name := strings.ToLower(strings.TrimSpace(channel))
		switch name {
		case "admin", "biz":
		default:
			errs = append(errs, fmt.Errorf("unsupported identity.mfa.channels entry: %s", channel))
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
	if c.Identity.MFA.BizRequired && !bizEnabled {
		errs = append(errs, errors.New("identity.mfa.biz_required requires biz in identity.mfa.channels"))
	}
	if _, enabled := mfaChannels[c.Channel]; !enabled && c.Identity.MFA.BizRequired && c.Channel == "biz" {
		errs = append(errs, errors.New("identity.mfa.biz_required requires the current channel=biz to be enabled"))
	}
	switch c.Channel {
	case "admin":
		if isProdLikeEnv(c.Env) && !c.Identity.MFA.AdminRequired {
			errs = append(errs, errors.New("identity.mfa.admin_required must be true in prod/staging"))
		}
		if c.Identity.MFA.AdminRequired {
			keys, keysErr := c.Identity.MFA.DecodeKeys()
			if keysErr != nil {
				errs = append(errs, keysErr)
			}
			key, err := c.Identity.MFA.DecodeSecretKey()
			if err != nil {
				errs = append(errs, err)
			}
			if len(keys) == 0 && key == nil {
				errs = append(errs, errors.New("identity.mfa.secret_key or identity.mfa.keys is required when identity.mfa.admin_required=true"))
			}
		}
	case "biz":
		// 显式 fail fast：避免「配置写了强制 MFA，运行时却完全不校验」的假安全。
		if c.Identity.MFA.AdminRequired {
			errs = append(errs, errors.New("identity.mfa.admin_required must be false on channel=biz"))
		}
		if c.Identity.MFA.BizRequired {
			keys, keysErr := c.Identity.MFA.DecodeKeys()
			if keysErr != nil {
				errs = append(errs, keysErr)
			}
			key, err := c.Identity.MFA.DecodeSecretKey()
			if err != nil {
				errs = append(errs, err)
			}
			if len(keys) == 0 && key == nil {
				errs = append(errs, errors.New("identity.mfa.secret_key or identity.mfa.keys is required when identity.mfa.biz_required=true"))
			}
		}
	}
	if strings.TrimSpace(c.Identity.MFA.Issuer) == "" {
		errs = append(errs, errors.New("identity.mfa.issuer must not be empty"))
	}
	if len(c.Identity.MFA.Keys) > 0 {
		keys, keysErr := c.Identity.MFA.DecodeKeys()
		if keysErr != nil {
			errs = append(errs, keysErr)
		}
		active := strings.TrimSpace(c.Identity.MFA.ActiveKeyID)
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
	if len(c.Identity.Subject.Keys) > 0 {
		keys, keysErr := c.Identity.Subject.DecodeKeys()
		if keysErr != nil {
			errs = append(errs, keysErr)
		}
		active := strings.TrimSpace(c.Identity.Subject.ActiveKeyID)
		switch active {
		case "":
			errs = append(errs, errors.New("identity.subject.active_key_id is required when identity.subject.keys is set"))
		default:
			if _, ok := keys[active]; !ok {
				errs = append(errs, fmt.Errorf("identity.subject.active_key_id %q is not in identity.subject.keys", active))
			}
		}
	}
	errs = append(errs, c.validateIdentitySMS()...)
	errs = append(errs, c.validateIdentityOAuth()...)
	errs = append(errs, c.validateIdentitySAML()...)
	return errs
}

// validateIdentityPassword 校验密码策略范围（IDN-P2-01）。
//
// 零值字段按默认策略（12～128）处理；错误只包含字段路径与边界值。
func (c *Config) validateIdentityPassword() []error {
	var errs []error
	minLength := c.Identity.Password.MinLength
	if minLength == 0 {
		minLength = identityDefaultPasswordMinLength
	}
	maxLength := c.Identity.Password.MaxLength
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
	if c.Identity.Password.MaxConcurrent < 0 {
		errs = append(errs, errors.New("identity.password.max_concurrent must be >= 0"))
	}
	// 0 表示由默认值填充（2s）。
	if c.Identity.Password.WaitTimeout != 0 {
		if c.Identity.Password.WaitTimeout < identityMinPasswordWaitTimeout {
			errs = append(errs, errors.New("identity.password.wait_timeout must be >= 100ms"))
		}
		if c.Identity.Password.WaitTimeout > identityMaxPasswordWaitTimeout {
			errs = append(errs, errors.New("identity.password.wait_timeout must be <= 30s"))
		}
	}
	if c.Identity.Password.HistoryCount < 0 || c.Identity.Password.HistoryCount > 20 {
		errs = append(errs, errors.New("identity.password.history_count must be in [0, 20]"))
	}
	if c.Identity.Password.HistoryWindow < 0 || c.Identity.Password.HistoryWindow > 5*365*24*time.Hour {
		errs = append(errs, errors.New("identity.password.history_window must be in [0, 5y]"))
	}
	if c.Identity.Password.TemporaryLength != 0 {
		if c.Identity.Password.TemporaryLength < 16 || c.Identity.Password.TemporaryLength > 32 {
			errs = append(errs, errors.New("identity.password.temporary_length must be in [16, 32]"))
		}
	}
	globalComplexity := true
	if c.Identity.Password.RequireComplexity != nil {
		globalComplexity = *c.Identity.Password.RequireComplexity
	}
	seen := make(map[string]struct{}, len(c.Identity.Password.TenantOverrides))
	for _, override := range c.Identity.Password.TenantOverrides {
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

// validateIdentityMFA 校验 TOTP 与恢复码参数（MNT-05）。
func (c *Config) validateIdentityMFA() []error {
	mfaCfg := c.Identity.MFA
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
