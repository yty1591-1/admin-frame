package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"
)

// Validate 校验必填项、枚举与生产互斥项（CFG-03/05）。
//
// 错误通过 errors.Join 聚合，启动日志可一次列出全部问题；
// 错误文本只包含字段路径，不回显取值。
func (c *Config) Validate() error {
	var errs []error
	// 密钥解析失败是根因，先于字段校验汇报，避免下游出现「DSN 为空」这类派生错误。
	if c.secretErr != nil {
		errs = append(errs, c.secretErr)
	}
	if c.Service == "" {
		errs = append(errs, errors.New("service is required"))
	}
	if c.Channel == "" {
		errs = append(errs, errors.New("channel is required"))
	} else {
		switch c.Channel {
		case "admin", "biz", "c", "worker":
		default:
			errs = append(errs, fmt.Errorf("unsupported channel: %s", c.Channel))
		}
	}
	if c.Addr == "" {
		errs = append(errs, errors.New("addr is required"))
	}
	if c.Auth.ExpectedChannel != c.Channel {
		errs = append(errs, fmt.Errorf("auth.expected_channel %q must equal channel %q", c.Auth.ExpectedChannel, c.Channel))
	}
	errs = append(errs, c.validateLog()...)
	errs = append(errs, c.validateHTTP()...)
	errs = append(errs, c.validateAuth()...)
	errs = append(errs, c.validateDB()...)
	if err := platformstorage.ValidateStorage(c.Env, c.Storage); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, c.validateEnv()...)
	errs = append(errs, c.validateTracing()...)
	errs = append(errs, c.validateID()...)
	errs = append(errs, c.validateIdempotency()...)
	errs = append(errs, c.validateRateLimit()...)
	errs = append(errs, c.validateIAM()...)
	errs = append(errs, c.validateIdentity()...)
	errs = append(errs, c.validateAudit()...)
	errs = append(errs, c.validateSecrets()...)
	errs = append(errs, c.validateSampleCredentials()...)
	return errors.Join(errs...)
}

// 已随仓库公开的演练密钥 / 连接串（configs/prod/env.example 与本地 prod-*.yaml）。
//
// 这些值已经进入版本库，等同于泄露；生产启动必须拒绝，避免「复制样例即上线」。
const (
	sampleIdentityHashKey      = "7a1ec0f4290851c052cee9fb3b972bc5a8c14297c65e4301bb320b3e6f426454"
	sampleIdentityMFASecretKey = "a0c1663bfcdb9f923a4d7171709b6b0d36ab2298053a36f4c26cf7a3b5c17c8a" //nolint:gosec // 公开演练值，仅用于拒绝匹配
	sampleSubjectKey           = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	sampleJWTSecret            = "ca24256120caa7c42ccaa14f43aa56c8a3d72b4b3cd2ac88b51ed2492101d753"
	sampleRedisPassword        = "fd51b4775c2cc81269accb4e1c511c7f4393d159927c95c8" //nolint:gosec // 公开演练值，仅用于拒绝匹配
	sampleS3AccessKey          = "AFPRODMNFN5QQZBGXY"
	sampleS3SecretKey          = "878d6f395e595e146fd395418bfe4d9cdf0a596cda8ecb88f85b4d95de402c77" //nolint:gosec // 公开演练值，仅用于拒绝匹配
	sampleDBDSNFragment        = "root:root@tcp(localhost:3306)"
)

// validateSampleCredentials 拒绝生产环境使用仓库内已公开的演练密钥（P1-1）。
//
// 只比对完整示例值，不做模糊匹配：误判会阻断合法启动，而部分泄露的密钥
// 仍应由密钥托管与扫描门禁负责。
func (c *Config) validateSampleCredentials() []error {
	if !isProdEnv(c.Env) {
		return nil
	}
	var errs []error
	reject := func(field, value, sample string) {
		if strings.TrimSpace(value) == sample {
			errs = append(errs, fmt.Errorf("%s uses a published sample secret; inject a real value from the secret provider", field))
		}
	}
	reject("identity.hash_key", c.Identity.HashKey, sampleIdentityHashKey)
	reject("identity.mfa.secret_key", c.Identity.MFA.SecretKey, sampleIdentityMFASecretKey)
	reject("auth.jwt.secret", c.Auth.JWT.Secret, sampleJWTSecret)
	reject("redis.password", c.Redis.Password, sampleRedisPassword)
	reject("storage.s3.access_key", c.Storage.S3.AccessKey, sampleS3AccessKey)
	reject("storage.s3.secret_key", c.Storage.S3.SecretKey, sampleS3SecretKey)
	for i, item := range c.Identity.Subject.Keys {
		if strings.TrimSpace(item.Key) == sampleSubjectKey {
			errs = append(errs, fmt.Errorf("identity.subject.keys[%d].key uses a published sample secret", i))
		}
	}
	if strings.Contains(c.DB.DSN, sampleDBDSNFragment) {
		errs = append(errs, errors.New("db.dsn uses the published local rehearsal DSN; inject a production DSN"))
	}
	return errs
}

// validateAudit 校验审计存储、范围与保留策略（AUD-06 / AUD-07 / ENT-04）。
//
// 语义约定与其他配置段一致：零值 / 空串表示「未配置」，由 applyDefaults 填默认值，
// 因此这里只校验显式设定的值；负数与越界一律拒绝。
func (c *Config) validateAudit() []error {
	var errs []error
	store := strings.ToLower(strings.TrimSpace(c.Audit.Store))
	effective := c.Audit.EffectiveStore(c.DB.Driver)
	if store != "" {
		switch store {
		case AuditStoreAuto, AuditStoreDB, AuditStoreLog:
		default:
			errs = append(errs, fmt.Errorf("unsupported audit.store: %s", c.Audit.Store))
		}
	}
	if effective == AuditStoreLog && isProdEnv(c.Env) {
		errs = append(errs, errors.New("audit.store must resolve to db when env=prod (AUD-06)"))
	}
	// ENT-15：租户处置时间线边界——保留期不得短于 30 天（争议期底线），
	// 导出窗口不得短于 7 天（客户拿数据需要真实时间）。
	if c.Tenant.Retention.HoldDays < 0 || c.Tenant.Retention.HoldDays > 3650 {
		errs = append(errs, errors.New("tenant.retention.hold_days must be within [0, 3650]"))
	}
	if c.Tenant.Retention.ExportDays < 0 || c.Tenant.Retention.ExportDays > 3650 {
		errs = append(errs, errors.New("tenant.retention.export_days must be within [0, 3650]"))
	}
	if c.Tenant.Retention.PurgeBatchSize < 0 || c.Tenant.Retention.PurgeBatchSize > 100000 {
		errs = append(errs, errors.New("tenant.retention.purge_batch_size must be within [0, 100000]"))
	}
	if store == AuditStoreDB && strings.EqualFold(strings.TrimSpace(c.DB.Driver), "memory") {
		errs = append(errs, errors.New("audit.store=db requires a persistent db.driver"))
	}
	if days := c.Audit.RetentionDays; days != 0 && (days < AuditMinRetentionDays || days > AuditMaxRetentionDays) {
		errs = append(errs, fmt.Errorf(
			"audit.retention_days must be in [%d, %d] (AUD-07)",
			AuditMinRetentionDays, AuditMaxRetentionDays,
		))
	}
	if v := c.Audit.Query.MaxRangeDays; v != 0 && (v < 1 || v > 366) {
		errs = append(errs, errors.New("audit.query.max_range_days must be in [1, 366]"))
	}
	if v := c.Audit.Query.MaxPageSize; v != 0 && (v < 1 || v > 100) {
		errs = append(errs, errors.New("audit.query.max_page_size must be in [1, 100]"))
	}
	if v := c.Audit.Query.DefaultPageSize; v != 0 {
		upper := c.Audit.Query.MaxPageSize
		if upper == 0 {
			upper = 100
		}
		if v < 1 || v > upper {
			errs = append(errs, errors.New("audit.query.default_page_size must be in [1, max_page_size]"))
		}
	}
	if v := c.Audit.Export.MaxRangeDays; v != 0 && (v < 1 || v > 366) {
		errs = append(errs, errors.New("audit.export.max_range_days must be in [1, 366]"))
	}
	if v := c.Audit.Export.MaxRows; v != 0 && (v < 1 || v > 1000000) {
		errs = append(errs, errors.New("audit.export.max_rows must be in [1, 1000000]"))
	}
	if v := c.Audit.Export.TTLDays; v != 0 && (v < 1 || v > 365) {
		errs = append(errs, errors.New("audit.export.ttl_days must be in [1, 365]"))
	}
	if v := c.Audit.Export.RetryMax; v != 0 && (v < 1 || v > 10) {
		errs = append(errs, errors.New("audit.export.retry_max must be in [1, 10]"))
	}
	if v := c.Audit.Export.PollInterval; v != 0 && v < time.Second {
		errs = append(errs, errors.New("audit.export.poll_interval must be >= 1s"))
	}
	if v := c.Audit.Export.StaleTimeout; v != 0 && v < time.Minute {
		errs = append(errs, errors.New("audit.export.stale_timeout must be >= 1m"))
	}
	if v := c.Audit.Retention.BatchSize; v != 0 && (v < 100 || v > 50000) {
		errs = append(errs, errors.New("audit.retention.batch_size must be in [100, 50000]"))
	}
	if prefix := strings.TrimSpace(c.Audit.Retention.ArchivePrefix); prefix != "" {
		if strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "..") {
			errs = append(errs, errors.New("audit.retention.archive_prefix must be a relative object key prefix without '..'"))
		}
		// memory 存储随进程消失：归档落到这里等于「删了却查不到」。
		if c.Storage.Driver == "memory" {
			errs = append(errs, errors.New("audit.retention.archive_prefix requires a persistent storage.driver (memory loses archives on restart)"))
		}
	}
	if v := c.Audit.Retention.CleanupInterval; v != 0 && v < time.Minute {
		errs = append(errs, errors.New("audit.retention.cleanup_interval must be >= 1m"))
	}
	return errs
}

// validateLog 校验日志级别与格式；prod 禁止 text（OBS-01）。
func (c *Config) validateLog() []error {
	var errs []error
	switch strings.ToLower(strings.TrimSpace(c.Log.Level)) {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("unsupported log.level: %s", c.Log.Level))
	}
	switch strings.ToLower(strings.TrimSpace(c.Log.Format)) {
	case "json", "text":
	default:
		// 必须在启动前拦截：NewLogger 使用 logger.Must，非法格式会 panic。
		errs = append(errs, fmt.Errorf("unsupported log.format: %s", c.Log.Format))
	}
	if isProdEnv(c.Env) && strings.EqualFold(strings.TrimSpace(c.Log.Format), "text") {
		errs = append(errs, errors.New("log.format=text is forbidden when env=prod"))
	}
	return errs
}

// validateHTTP 校验 Server 加固参数与请求链路参数（REL-01/02）。
func (c *Config) validateHTTP() []error {
	var errs []error
	if c.HTTP.ReadHeaderTimeout < 0 {
		errs = append(errs, errors.New("http.read_header_timeout must be >= 0"))
	}
	if c.HTTP.MaxHeaderBytes < 0 {
		errs = append(errs, errors.New("http.max_header_bytes must be >= 0"))
	}
	if c.HTTP.Timeout < 0 {
		errs = append(errs, errors.New("http.timeout must be >= 0"))
	}
	if isProdEnv(c.Env) && c.HTTP.Timeout <= 0 {
		errs = append(errs, errors.New("http.timeout must be > 0 when env=prod"))
	}
	if c.HTTP.BodyLimitBytes < 0 {
		errs = append(errs, errors.New("http.body_limit_bytes must be >= 0"))
	}
	for _, raw := range c.HTTP.TrustedProxies {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(strings.TrimSpace(raw)); err != nil {
			errs = append(errs, errors.New("http.trusted_proxies must be valid CIDR"))
		}
	}
	return errs
}

// validateAuth 校验鉴权模式、端别隔离与 JWT 配置（SEC-04 / CFG-05）。
func (c *Config) validateAuth() []error {
	var errs []error
	mode := c.Auth.effectiveMode(c.Env)
	switch mode {
	case AuthModeDemo, AuthModeJWT:
	default:
		errs = append(errs, fmt.Errorf("unsupported auth.mode: %s", c.Auth.Mode))
	}
	if mode == AuthModeDemo && isProdEnv(c.Env) {
		errs = append(errs, errors.New("auth.mode=demo is forbidden when env=prod"))
	}
	if mode == AuthModeJWT {
		if err := c.Auth.JWT.Validate(c.Channel); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
