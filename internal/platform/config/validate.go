package config

import (
	"errors"
	"fmt"
	"net"
	"strings"

	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"
)

// Validate 校验平台段必填项、枚举与生产互斥项（CFG-03/05）。
//
// 错误通过 errors.Join 聚合，启动日志可一次列出全部问题；
// 错误文本只包含字段路径，不回显取值。域配置校验由组合加载层追加。
func (c *Config) Validate() error {
	return errors.Join(c.ValidateErrors("")...)
}

// ValidateErrors 返回平台段聚合错误切片；iamMode 供 async 的跨段规则使用。
func (c *Config) ValidateErrors(iamMode string) []error {
	var errs []error
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
	errs = append(errs, c.validateAsync(iamMode)...)
	errs = append(errs, c.validateSecrets()...)
	errs = append(errs, c.validateSampleCredentials()...)
	return errs
}

// 已随仓库公开的演练密钥 / 连接串（configs/prod/env.example 与本地 prod-*.yaml）。
//
// 这些值已经进入版本库，等同于泄露；生产启动必须拒绝，避免「复制样例即上线」。
const (
	sampleJWTSecret     = "ca24256120caa7c42ccaa14f43aa56c8a3d72b4b3cd2ac88b51ed2492101d753"
	sampleRedisPassword = "fd51b4775c2cc81269accb4e1c511c7f4393d159927c95c8" //nolint:gosec // 公开演练值，仅用于拒绝匹配
	sampleS3AccessKey   = "AFPRODMNFN5QQZBGXY"
	sampleS3SecretKey   = "878d6f395e595e146fd395418bfe4d9cdf0a596cda8ecb88f85b4d95de402c77" //nolint:gosec // 公开演练值，仅用于拒绝匹配
	sampleDBDSNFragment = "root:root@tcp(localhost:3306)"
)

// validateSampleCredentials 拒绝生产环境使用仓库内已公开的演练密钥（P1-1）。
//
// 只比对完整示例值，不做模糊匹配：误判会阻断合法启动，而部分泄露的密钥
// 仍应由密钥托管与扫描门禁负责。身份域样例密钥由 internal/identity/config 负责。
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
	reject("auth.jwt.secret", c.Auth.JWT.Secret, sampleJWTSecret)
	reject("redis.password", c.Redis.Password, sampleRedisPassword)
	reject("storage.s3.access_key", c.Storage.S3.AccessKey, sampleS3AccessKey)
	reject("storage.s3.secret_key", c.Storage.S3.SecretKey, sampleS3SecretKey)
	if strings.Contains(c.DB.DSN, sampleDBDSNFragment) {
		errs = append(errs, errors.New("db.dsn uses the published local rehearsal DSN; inject a production DSN"))
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
