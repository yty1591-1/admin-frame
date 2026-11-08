package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ApplyDefaults 填充 IAM 默认值；Mode 保持空串，由 Normalize 统一为 off。
func (c *IAMConfig) ApplyDefaults() {
	if c.LoadTimeout == 0 {
		c.LoadTimeout = 300 * time.Millisecond
	}
	if c.Cache.Driver == "" {
		c.Cache.Driver = IAMCacheDriverMemory
	}
	if c.Cache.L1TTL == 0 {
		c.Cache.L1TTL = 3 * time.Second
	}
	if c.Cache.L2TTL == 0 {
		c.Cache.L2TTL = 5 * time.Minute
	}
	if c.Cache.NegativeTTL == 0 {
		c.Cache.NegativeTTL = 30 * time.Second
	}
	if c.Cache.KeyPrefix == "" {
		c.Cache.KeyPrefix = "iam"
	}
	if c.Cache.L1MaxEntries == 0 {
		c.Cache.L1MaxEntries = 10000
	}
	if c.Limits.MaxRolesPerSubject == 0 {
		c.Limits.MaxRolesPerSubject = 20
	}
	if c.Limits.MaxPermissionsPerRole == 0 {
		c.Limits.MaxPermissionsPerRole = 200
	}
	if c.Limits.MaxBindingsPerSubject == 0 {
		c.Limits.MaxBindingsPerSubject = 50
	}
	if c.Limits.MaxCustomRolesPerMerchant == 0 {
		c.Limits.MaxCustomRolesPerMerchant = 50
	}
	if c.Certification.Enabled == nil {
		enabled := true
		c.Certification.Enabled = &enabled
	}
}

// Normalize 归一化 iam.mode 与 RBAC2 约束模式。
func (c *IAMConfig) Normalize() {
	c.Mode = normalizeMode(c.Mode)
	c.Constraints.Mode = normalizeConstraintMode(c.Constraints.Mode)
}

// normalizeConstraintMode 归一化 RBAC2 约束模式；空串视为 shadow（影子期默认）。
func normalizeConstraintMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return IAMConstraintsModeShadow
	}
	return mode
}

// normalizeMode 归一化 iam.mode；空串视为 off（保持历史静态权限表行为）。
func normalizeMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return IAMModeOff
	}
	return mode
}

// Validate 校验 IAM 模式、缓存 TTL、生产互斥项与端别约束（docs/rbac-iam.md §12.2）。
//
// 平台段信息（环境、端别、数据库驱动、Redis/异步开关）由组合层显式传入，
// 域包不反向依赖 platform。错误只包含字段路径与约束，不回显取值（CFG-05）。
func (c *IAMConfig) Validate(env, channel, dbDriver string, redisEnabled, asyncEnabled bool) error {
	var errs []error
	// 完全未配置 iam 段时保持历史行为（zero-value Config 直接调用 Validate 也不会误报）；
	// 生产路径统一经加载器 → ApplyDefaults，因此段内字段必然已填充。
	prodAB := isProdEnv(env) && (channel == "admin" || channel == "biz")
	if !prodAB && c.Mode == "" && c.Cache.Driver == "" && c.LoadTimeout == 0 && !c.Approval.Enabled {
		return nil
	}
	mode := normalizeMode(c.Mode)
	switch mode {
	case IAMModeOff, IAMModeShadow, IAMModeEnforce:
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.mode: %s", c.Mode))
	}

	// 生产 A/B 端禁止把鉴权降级为 shadow/off，也禁止关闭高风险变更审批：
	// 这两项一旦被误配，越权与 4-eyes 绕过都不会报错，只能在故障后才暴露。
	if prodAB {
		if mode != IAMModeEnforce {
			errs = append(errs, errors.New("iam.mode must be enforce when env=prod on channel=admin/biz"))
		}
		if !c.Approval.Enabled {
			errs = append(errs, errors.New("iam.approval.enabled must be true when env=prod on channel=admin/biz"))
		}
	}

	if channel != "" {
		// C 端与 worker 不挂载 IAM；显式开启会在无 transport 时静默失效，故启动即拒绝。
		if (channel == "c" || channel == "worker") && mode != IAMModeOff {
			errs = append(errs, fmt.Errorf("iam.mode must be off when channel is %s", channel))
		}
	}

	if c.LoadTimeout <= 0 || c.LoadTimeout > time.Second {
		errs = append(errs, errors.New("iam.load_timeout must be in (0, 1s]"))
	}
	switch c.Cache.Driver {
	case IAMCacheDriverMemory:
	case IAMCacheDriverRedis:
		if !redisEnabled {
			errs = append(errs, errors.New("redis.addr is required when iam.cache.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.cache.driver: %s", c.Cache.Driver))
	}
	if c.Cache.L1TTL <= 0 || c.Cache.L1TTL > 5*time.Second {
		errs = append(errs, errors.New("iam.cache.l1_ttl must be in (0, 5s]"))
	}
	if c.Cache.L2TTL <= 0 || c.Cache.L2TTL > 15*time.Minute {
		errs = append(errs, errors.New("iam.cache.l2_ttl must be in (0, 15m]"))
	}
	if c.Cache.L2TTL <= c.Cache.L1TTL {
		errs = append(errs, errors.New("iam.cache.l2_ttl must be greater than iam.cache.l1_ttl"))
	}
	if c.Cache.NegativeTTL <= 0 || c.Cache.NegativeTTL > c.Cache.L2TTL {
		errs = append(errs, errors.New("iam.cache.negative_ttl must be in (0, iam.cache.l2_ttl]"))
	}
	if c.Cache.L1MaxEntries < 0 {
		errs = append(errs, errors.New("iam.cache.l1_max_entries must be >= 0"))
	}
	if c.Limits.MaxRolesPerSubject <= 0 {
		errs = append(errs, errors.New("iam.limits.max_roles_per_subject must be > 0"))
	}
	if c.Limits.MaxPermissionsPerRole <= 0 {
		errs = append(errs, errors.New("iam.limits.max_permissions_per_role must be > 0"))
	}
	if c.Limits.MaxBindingsPerSubject <= 0 {
		errs = append(errs, errors.New("iam.limits.max_bindings_per_subject must be > 0"))
	}
	if channel == "biz" && c.Limits.MaxCustomRolesPerMerchant <= 0 {
		errs = append(errs, errors.New("iam.limits.max_custom_roles_per_merchant must be > 0 when channel=biz"))
	}
	// 空串在 Normalize 中会归一为 shadow；校验阶段同样视为合法，避免直接构造配置时误报。
	switch c.Constraints.Mode {
	case "", IAMConstraintsModeOff, IAMConstraintsModeShadow, IAMConstraintsModeEnforce:
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.constraints.mode: %s", c.Constraints.Mode))
	}

	if isProdLikeEnv(env) && mode == IAMModeEnforce && c.Cache.Driver != IAMCacheDriverRedis {
		errs = append(errs, errors.New("iam.cache.driver must be redis when iam.mode=enforce in prod/staging"))
	}
	if isProdEnv(env) && c.Security.AllowWildcard {
		errs = append(errs, errors.New("iam.security.allow_wildcard must be false when env=prod"))
	}
	if c.Breakglass.Enabled {
		if channel != "admin" {
			errs = append(errs, errors.New("iam.breakglass.enabled is only supported on channel=admin"))
		}
		if mode != IAMModeEnforce {
			errs = append(errs, errors.New("iam.breakglass.enabled requires iam.mode=enforce"))
		}
		if isProdEnv(env) && len(c.Breakglass.AllowedSubjects) == 0 {
			errs = append(errs, errors.New("iam.breakglass.allowed_subjects is required when env=prod"))
		}
		for i, subject := range c.Breakglass.AllowedSubjects {
			if strings.TrimSpace(subject) == "" {
				errs = append(errs, fmt.Errorf("iam.breakglass.allowed_subjects[%d] must not be empty", i))
			}
		}
	}
	if c.Approval.Enabled {
		// 审批单属于 A / B 端治理能力；C 端与 worker 没有 IAM transport。
		if channel == "c" || channel == "worker" {
			errs = append(errs, errors.New("iam.approval.enabled is only supported on channel=admin or biz"))
		}
		// 待审批单必须跨重启保留：内存仓储只用于 local / demo / 单测。
		if isProdEnv(env) && dbDriver == "memory" {
			errs = append(errs, errors.New("iam.approval.enabled requires a persistent db.driver when env=prod"))
		}
	}
	if isProdLikeEnv(env) && mode == IAMModeEnforce && !asyncEnabled {
		errs = append(errs, errors.New("async.enabled must be true when iam.mode=enforce in prod/staging"))
	}
	// 自动回收必须以显式启用的访问复核为前提，否则到期扫描不会运行，
	// 配置看似生效却永远不回收，属于静默失效。
	if c.Certification.AutoRevoke && !c.Certification.IsEnabled() {
		errs = append(errs, errors.New("iam.certification.auto_revoke requires iam.certification.enabled=true"))
	}
	// 单活动上限：0 表示沿用服务内置默认，负值没有语义，超过封顶值属容量设计
	// 问题（应拆活动而不是抬上限），两者都在装配期拒绝。
	if c.Certification.MaxTasksPerCampaign < 0 {
		errs = append(errs, errors.New(
			"iam.certification.max_tasks_per_campaign must be >= 0 (0 means built-in default 10000)"))
	}
	if c.Certification.MaxTasksPerCampaign > MaxCertificationTasksPerCampaignCap {
		errs = append(errs, fmt.Errorf(
			"iam.certification.max_tasks_per_campaign=%d exceeds cap %d",
			c.Certification.MaxTasksPerCampaign, MaxCertificationTasksPerCampaignCap))
	}
	return errors.Join(errs...)
}

// isProdEnv 报告环境是否为生产。
func isProdEnv(env string) bool {
	return strings.EqualFold(strings.TrimSpace(env), "prod")
}

// isProdLikeEnv 报告环境是否按生产标准校验（prod / staging）。
func isProdLikeEnv(env string) bool {
	return isProdEnv(env) || strings.EqualFold(strings.TrimSpace(env), "staging")
}
