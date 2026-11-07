package config

import (
	"errors"
	"fmt"
	"strings"
)

// validateDB 校验驱动枚举、DSN 必填与连接池参数。
func (c *Config) validateDB() []error {
	var errs []error
	switch c.DB.Driver {
	case "memory", "mysql", "postgres":
	default:
		errs = append(errs, fmt.Errorf("unsupported db.driver: %s", c.DB.Driver))
		return errs
	}
	if c.DB.Driver != "memory" && c.DB.DSN == "" {
		errs = append(errs, fmt.Errorf("db.dsn is required when driver is %s", c.DB.Driver))
	}
	// 连接池参数：负值无效（database/sql 会把 0 当作「不限」，负值无意义）；
	// 显式给出的 idle 超过 open 时，多余的 idle 会被 database/sql 静默丢弃，属于配制错误。
	if c.DB.MaxOpenConns < 0 {
		errs = append(errs, errors.New("db.max_open_conns must not be negative"))
	}
	if c.DB.MaxIdleConns < 0 {
		errs = append(errs, errors.New("db.max_idle_conns must not be negative"))
	}
	if c.DB.ConnMaxLifetime < 0 {
		errs = append(errs, errors.New("db.conn_max_lifetime must not be negative"))
	}
	if c.DB.MaxOpenConns > 0 && c.DB.MaxIdleConns > c.DB.MaxOpenConns {
		errs = append(errs, fmt.Errorf(
			"db.max_idle_conns (%d) must not exceed db.max_open_conns (%d)",
			c.DB.MaxIdleConns, c.DB.MaxOpenConns,
		))
	}
	return errs
}

// validateEnv 校验环境枚举与生产互斥项。
func (c *Config) validateEnv() []error {
	switch strings.ToLower(strings.TrimSpace(c.Env)) {
	case "", "local", "dev", "test", "staging", "prod":
	default:
		return []error{fmt.Errorf("unsupported env: %s", c.Env)}
	}
	return nil
}

// validateTracing 校验 exporter、endpoint 与生产互斥项（OBS）。
func (c *Config) validateTracing() []error {
	var errs []error
	switch strings.ToLower(strings.TrimSpace(c.Tracing.Exporter)) {
	case "", "none", "otlp", "stdout", "noop":
	default:
		errs = append(errs, fmt.Errorf("unsupported tracing.exporter: %s", c.Tracing.Exporter))
	}
	if c.Tracing.IsEnabled() && strings.EqualFold(c.Tracing.Exporter, "otlp") && strings.TrimSpace(c.Tracing.Endpoint) == "" {
		errs = append(errs, errors.New("tracing.endpoint is required when exporter=otlp"))
	}
	if c.Tracing.SampleRatio != nil {
		if r := *c.Tracing.SampleRatio; r < 0 || r > 1 {
			errs = append(errs, errors.New("tracing.sample_ratio must be in [0,1]"))
		}
	}
	if isProdEnv(c.Env) && strings.EqualFold(c.Tracing.Exporter, "stdout") {
		errs = append(errs, errors.New("tracing.exporter=stdout is forbidden when env=prod"))
	}
	return errs
}

// validateID 校验 NodeID 来源、范围与生产必填项（ENT-02 T1）。
func (c *Config) validateID() []error {
	var errs []error
	source := strings.ToLower(strings.TrimSpace(c.ID.NodeIDSource))
	switch source {
	case "", IDSourceConfig, IDSourceEnv:
	case IDSourceStatefulSet:
		if c.ID.NodeID != nil {
			errs = append(errs, errors.New("id.node_id must not be set when id.node_id_source=statefulset"))
		}
	case "registry":
		errs = append(errs, errors.New("id.node_id_source=registry is not supported"))
	default:
		errs = append(errs, fmt.Errorf("unsupported id.node_id_source: %s", c.ID.NodeIDSource))
	}
	if c.ID.NodeID != nil && (*c.ID.NodeID < 0 || *c.ID.NodeID > MaxNodeID) {
		errs = append(errs, fmt.Errorf("id.node_id must be in [0, %d]", MaxNodeID))
	}
	if isProdLikeEnv(c.Env) && c.Channel != "worker" &&
		source != IDSourceStatefulSet && c.ID.NodeID == nil {
		errs = append(errs, errors.New("id.node_id is required in prod/staging when id.node_id_source is not statefulset"))
	}
	return errs
}

// validateIdempotency 校验幂等驱动、TTL 与生产强制项（ENT-02 T3）。
func (c *Config) validateIdempotency() []error {
	// 生产 A/B 端是审批单、绑定、破玻璃、模板同步等写操作的入口，
	// 幂等键一旦可缺省，重复提交就会绕过 4-eyes 的「一次审批只生效一次」。
	prodAB := isProdEnv(c.Env) && (c.Channel == "admin" || c.Channel == "biz")
	if prodAB && !c.Idempotency.Enabled {
		return []error{errors.New("idempotency.enabled must be true when env=prod on channel=admin/biz")}
	}
	if !c.Idempotency.Enabled {
		return nil
	}
	var errs []error
	driver := strings.ToLower(strings.TrimSpace(c.Idempotency.Driver))
	switch driver {
	case "", IdempotencyDriverMemory:
	case IdempotencyDriverRedis:
		if !c.Redis.Enabled() {
			errs = append(errs, errors.New("redis.addr is required when idempotency.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported idempotency.driver: %s", c.Idempotency.Driver))
	}
	if c.Idempotency.TTL <= 0 {
		errs = append(errs, errors.New("idempotency.ttl must be > 0 when idempotency.enabled=true"))
	}
	if strings.TrimSpace(c.Idempotency.Header) == "" {
		errs = append(errs, errors.New("idempotency.header is required when idempotency.enabled=true"))
	}
	if c.Idempotency.MaxBodyBytes < 0 {
		errs = append(errs, errors.New("idempotency.max_body_bytes must be >= 0"))
	}
	if isProdLikeEnv(c.Env) && driver != IdempotencyDriverRedis {
		errs = append(errs, errors.New("idempotency.driver must be redis when enabled in prod/staging"))
	}
	if prodAB && !c.Idempotency.Required {
		errs = append(errs, errors.New("idempotency.required must be true when env=prod on channel=admin/biz"))
	}
	return errs
}

// validateRateLimit 校验限流驱动、分键、失败策略与参数（REL-05 / ENT-02 T5-T6）。
func (c *Config) validateRateLimit() []error {
	if !c.RateLimit.Enabled {
		return nil
	}
	var errs []error
	driver := strings.ToLower(strings.TrimSpace(c.RateLimit.Driver))
	switch driver {
	case "", RateLimitDriverMemory:
	case RateLimitDriverRedis:
		if !c.Redis.Enabled() {
			errs = append(errs, errors.New("redis.addr is required when rate_limit.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported rate_limit.driver: %s", c.RateLimit.Driver))
	}
	switch strings.ToLower(strings.TrimSpace(c.RateLimit.Key)) {
	case "", RateLimitKeyChannel, RateLimitKeyChannelUser, RateLimitKeyChannelTenantUser, RateLimitKeyClient:
	default:
		errs = append(errs, fmt.Errorf("unsupported rate_limit.key: %s", c.RateLimit.Key))
	}
	switch strings.ToLower(strings.TrimSpace(c.RateLimit.FailureMode)) {
	case "", RateLimitFailureOpen, RateLimitFailureClosed:
	default:
		errs = append(errs, fmt.Errorf("unsupported rate_limit.failure_mode: %s", c.RateLimit.FailureMode))
	}
	if c.RateLimit.RPS <= 0 {
		errs = append(errs, errors.New("rate_limit.rps must be > 0 when rate_limit.enabled=true"))
	}
	if c.RateLimit.Burst < 1 {
		errs = append(errs, errors.New("rate_limit.burst must be >= 1 when rate_limit.enabled=true"))
	}
	if c.RateLimit.IdleTTL < 0 {
		errs = append(errs, errors.New("rate_limit.idle_ttl must be >= 0"))
	}
	if isProdLikeEnv(c.Env) && driver != RateLimitDriverRedis {
		errs = append(errs, errors.New("rate_limit.driver must be redis when enabled in prod/staging"))
	}
	return errs
}
