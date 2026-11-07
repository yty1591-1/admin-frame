package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
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

// validateIAM 校验 IAM 模式、缓存 TTL、生产互斥项与端别约束（docs/rbac-iam.md §12.2）。
//
// 错误只包含字段路径与约束，不回显取值（CFG-05）。
func (c *Config) validateIAM() []error {
	// 完全未配置 iam 段时保持历史行为（zero-value Config 直接调用 Validate 也不会误报）；
	// 生产路径统一经 LoadConfig → applyDefaults，因此段内字段必然已填充。
	prodAB := isProdEnv(c.Env) && (c.Channel == "admin" || c.Channel == "biz")
	if !prodAB && c.IAM.Mode == "" && c.IAM.Cache.Driver == "" && c.IAM.LoadTimeout == 0 && !c.IAM.Approval.Enabled {
		return nil
	}
	var errs []error
	mode := normalizeIAMMode(c.IAM.Mode)
	switch mode {
	case IAMModeOff, IAMModeShadow, IAMModeEnforce:
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.mode: %s", c.IAM.Mode))
	}

	// 生产 A/B 端禁止把鉴权降级为 shadow/off，也禁止关闭高风险变更审批：
	// 这两项一旦被误配，越权与 4-eyes 绕过都不会报错，只能在故障后才暴露。
	if prodAB {
		if mode != IAMModeEnforce {
			errs = append(errs, errors.New("iam.mode must be enforce when env=prod on channel=admin/biz"))
		}
		if !c.IAM.Approval.Enabled {
			errs = append(errs, errors.New("iam.approval.enabled must be true when env=prod on channel=admin/biz"))
		}
	}

	if c.Service != "" {
		// C 端与 worker 不挂载 IAM；显式开启会在无 transport 时静默失效，故启动即拒绝。
		if (c.Channel == "c" || c.Channel == "worker") && mode != IAMModeOff {
			errs = append(errs, fmt.Errorf("iam.mode must be off when channel is %s", c.Channel))
		}
	}

	if c.IAM.LoadTimeout <= 0 || c.IAM.LoadTimeout > time.Second {
		errs = append(errs, errors.New("iam.load_timeout must be in (0, 1s]"))
	}
	switch c.IAM.Cache.Driver {
	case IAMCacheDriverMemory:
	case IAMCacheDriverRedis:
		if !c.Redis.Enabled() {
			errs = append(errs, errors.New("redis.addr is required when iam.cache.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.cache.driver: %s", c.IAM.Cache.Driver))
	}
	if c.IAM.Cache.L1TTL <= 0 || c.IAM.Cache.L1TTL > 5*time.Second {
		errs = append(errs, errors.New("iam.cache.l1_ttl must be in (0, 5s]"))
	}
	if c.IAM.Cache.L2TTL <= 0 || c.IAM.Cache.L2TTL > 15*time.Minute {
		errs = append(errs, errors.New("iam.cache.l2_ttl must be in (0, 15m]"))
	}
	if c.IAM.Cache.L2TTL <= c.IAM.Cache.L1TTL {
		errs = append(errs, errors.New("iam.cache.l2_ttl must be greater than iam.cache.l1_ttl"))
	}
	if c.IAM.Cache.NegativeTTL <= 0 || c.IAM.Cache.NegativeTTL > c.IAM.Cache.L2TTL {
		errs = append(errs, errors.New("iam.cache.negative_ttl must be in (0, iam.cache.l2_ttl]"))
	}
	if c.IAM.Cache.L1MaxEntries < 0 {
		errs = append(errs, errors.New("iam.cache.l1_max_entries must be >= 0"))
	}
	if c.IAM.Limits.MaxRolesPerSubject <= 0 {
		errs = append(errs, errors.New("iam.limits.max_roles_per_subject must be > 0"))
	}
	if c.IAM.Limits.MaxPermissionsPerRole <= 0 {
		errs = append(errs, errors.New("iam.limits.max_permissions_per_role must be > 0"))
	}
	if c.IAM.Limits.MaxBindingsPerSubject <= 0 {
		errs = append(errs, errors.New("iam.limits.max_bindings_per_subject must be > 0"))
	}
	if c.Channel == "biz" && c.IAM.Limits.MaxCustomRolesPerMerchant <= 0 {
		errs = append(errs, errors.New("iam.limits.max_custom_roles_per_merchant must be > 0 when channel=biz"))
	}
	// 空串在 normalize 中会归一为 shadow；校验阶段同样视为合法，避免直接构造配置时误报。
	switch c.IAM.Constraints.Mode {
	case "", IAMConstraintsModeOff, IAMConstraintsModeShadow, IAMConstraintsModeEnforce:
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.constraints.mode: %s", c.IAM.Constraints.Mode))
	}

	if isProdLikeEnv(c.Env) && mode == IAMModeEnforce && c.IAM.Cache.Driver != IAMCacheDriverRedis {
		errs = append(errs, errors.New("iam.cache.driver must be redis when iam.mode=enforce in prod/staging"))
	}
	if isProdEnv(c.Env) && c.IAM.Security.AllowWildcard {
		errs = append(errs, errors.New("iam.security.allow_wildcard must be false when env=prod"))
	}
	if c.IAM.Breakglass.Enabled {
		if c.Channel != "admin" {
			errs = append(errs, errors.New("iam.breakglass.enabled is only supported on channel=admin"))
		}
		if mode != IAMModeEnforce {
			errs = append(errs, errors.New("iam.breakglass.enabled requires iam.mode=enforce"))
		}
		if isProdEnv(c.Env) && len(c.IAM.Breakglass.AllowedSubjects) == 0 {
			errs = append(errs, errors.New("iam.breakglass.allowed_subjects is required when env=prod"))
		}
		for i, subject := range c.IAM.Breakglass.AllowedSubjects {
			if strings.TrimSpace(subject) == "" {
				errs = append(errs, fmt.Errorf("iam.breakglass.allowed_subjects[%d] must not be empty", i))
			}
		}
	}
	if c.IAM.Approval.Enabled {
		// 审批单属于 A / B 端治理能力；C 端与 worker 没有 IAM transport。
		if c.Channel == "c" || c.Channel == "worker" {
			errs = append(errs, errors.New("iam.approval.enabled is only supported on channel=admin or biz"))
		}
		// 待审批单必须跨重启保留：内存仓储只用于 local / demo / 单测。
		if isProdEnv(c.Env) && c.DB.Driver == "memory" {
			errs = append(errs, errors.New("iam.approval.enabled requires a persistent db.driver when env=prod"))
		}
	}
	if isProdLikeEnv(c.Env) && mode == IAMModeEnforce && !c.Async.Enabled {
		errs = append(errs, errors.New("async.enabled must be true when iam.mode=enforce in prod/staging"))
	}
	// 自动回收必须以显式启用的访问复核为前提，否则到期扫描不会运行，
	// 配置看似生效却永远不回收，属于静默失效。
	if c.IAM.Certification.AutoRevoke && !c.IAM.Certification.IsEnabled() {
		errs = append(errs, errors.New("iam.certification.auto_revoke requires iam.certification.enabled=true"))
	}
	errs = append(errs, c.validateAsync()...)
	return errs
}

// validHashKeyID 报告 key id 是否仅含字母 / 数字 / 下划线 / 短横线且长度 ≤16。
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
