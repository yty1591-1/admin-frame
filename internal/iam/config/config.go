package config

import (
	"time"
)

// IAMConfig 是 RBAC 权限管理（IAM）运行参数（docs/rbac-iam.md §12.1）。
//
// 默认 mode=off：未显式启用时保持历史静态权限表行为，避免升级即切换鉴权来源。
type IAMConfig struct {
	// Mode off | shadow | enforce。
	Mode string `yaml:"mode"`
	// LoadTimeout 回源 DB 超时上限；必须 ≤ 1s。
	LoadTimeout time.Duration `yaml:"load_timeout"`
	// Cache 两级快照缓存。
	Cache IAMCacheConfig `yaml:"cache"`
	// Limits 快照与角色规模上限，防止角色膨胀拖垮鉴权。
	Limits IAMLimitsConfig `yaml:"limits"`
	// Security 安全开关。
	Security IAMSecurityConfig `yaml:"security"`
	// Breakglass 破玻璃（限时超级权限）开关与允许主体。
	Breakglass IAMBreakglassConfig `yaml:"breakglass"`
	// Approval 高风险直写强制转审批开关。
	Approval IAMApprovalConfig `yaml:"approval"`
	// Constraints RBAC2 约束（互斥 / 基数 / 前置）的落库行为。
	Constraints IAMConstraintsConfig `yaml:"constraints"`
	// Certification 访问复核（P1-7）开关；默认开启、默认不自动回收。
	Certification IAMCertificationConfig `yaml:"certification"`
}

// IAMCertificationConfig 是访问复核（Access Certification）运行参数。
//
// Enabled 使用指针以区分「未配置」与「显式 false」：默认开启，
// 需要下线功能时必须能显式关闭（否则 handler 仍会注册复核路由）。
type IAMCertificationConfig struct {
	// Enabled 是否启用访问复核 API 与到期扫描；默认 true。
	Enabled *bool `yaml:"enabled"`
	// AutoRevoke 到期后是否自动回收绑定；默认 false，只标记 expired。
	AutoRevoke bool `yaml:"auto_revoke"`
	// MaxTasksPerCampaign 单活动任务上限；0 表示沿用服务内置默认（100000）。
	// 复核规模属容量参数，按环境配置而不是固化进二进制——大租户的季度复核
	// 可能覆盖十万级主体，小租户按默认档位即可。
	MaxTasksPerCampaign int `yaml:"max_tasks_per_campaign"`
}

// MaxCertificationTasksPerCampaignCap 是单活动任务上限的配置封顶值。
//
// 依据 S7-2 10 万档位实测（docs/incident/drills/2026-11-08-s7-2-perf-100k.md）：
// 生成期累计分配约 3.6 KB/任务、堆驻留约 0.7 KB/任务。50 万任务意味着生成期
// 约 1.8 GB 累计分配与 340 MB 驻留，已接近单实例合理预算。超过该值属容量
// 设计问题——应拆成多个活动，而不是继续抬高单活动上限。
const MaxCertificationTasksPerCampaignCap = 500000

// IsEnabled 返回是否启用访问复核；未配置时按默认开启。
func (c IAMCertificationConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// IAMConstraintsConfig 是 RBAC2 约束的落库行为配置。
//
// 默认 shadow：只计算并记录违规，不拦截写入，便于在存量数据上先观测影子冲突；
// 确认无问题后切 enforce，违规写入返回 40900。
type IAMConstraintsConfig struct {
	// Mode off | shadow | enforce。
	Mode string `yaml:"mode"`
}

// IAMCacheConfig 是快照缓存参数。
type IAMCacheConfig struct {
	// Driver memory | redis；prod + enforce 必须 redis。
	Driver string `yaml:"driver"`
	// L1TTL 进程内 TTL（撤销最坏生效窗口）；≤ 5s。
	L1TTL time.Duration `yaml:"l1_ttl"`
	// L2TTL Redis TTL；≤ 15m 且必须大于 L1TTL。
	L2TTL time.Duration `yaml:"l2_ttl"`
	// NegativeTTL 空快照 TTL，防异常主体打穿 DB。
	NegativeTTL time.Duration `yaml:"negative_ttl"`
	// KeyPrefix 缓存域段，默认 iam。
	KeyPrefix string `yaml:"key_prefix"`
	// L1MaxEntries 进程内最大条目数；≤0 时使用 10000。
	L1MaxEntries int `yaml:"l1_max_entries"`
}

// IAMLimitsConfig 是快照与角色规模上限。
type IAMLimitsConfig struct {
	// MaxRolesPerSubject 单主体最大生效角色数。
	MaxRolesPerSubject int `yaml:"max_roles_per_subject"`
	// MaxPermissionsPerRole 单角色最大权限点数。
	MaxPermissionsPerRole int `yaml:"max_permissions_per_role"`
	// MaxBindingsPerSubject 单主体最大绑定数。
	MaxBindingsPerSubject int `yaml:"max_bindings_per_subject"`
	// MaxCustomRolesPerMerchant B 端自定义角色数上限。
	MaxCustomRolesPerMerchant int `yaml:"max_custom_roles_per_merchant"`
}

// IAMBreakglassConfig 是破玻璃开关；生产默认关闭。
type IAMBreakglassConfig struct {
	// Enabled 是否启用破玻璃。
	Enabled bool `yaml:"enabled"`
	// AllowedSubjects 允许申请破玻璃的主体白名单；prod 启用时必填。
	AllowedSubjects []string `yaml:"allowed_subjects"`
}

// IAMApprovalConfig 是「高风险直写强制转审批」开关；生产默认关闭。
//
// 启用后角色 / 绑定等直接写端点在 transport 层统一返回 40900，调用方必须改走
// change-requests；审批单应用由用例内部调用，不经过 HTTP，因此不受此开关影响。
type IAMApprovalConfig struct {
	// Enabled 是否要求高风险变更必须经审批单。
	Enabled bool `yaml:"enabled"`
}

// IAMSecurityConfig 是 IAM 安全开关。
type IAMSecurityConfig struct {
	// AllowWildcard 是否允许快照携带通配符；prod 强制 false。
	AllowWildcard bool `yaml:"allow_wildcard"`
	// RequireReason 高风险变更是否强制填写原因。
	RequireReason bool `yaml:"require_reason"`
}

// Enabled 报告 IAM 是否接管鉴权（shadow 与 enforce 都需要加载快照）。
func (c IAMConfig) Enabled() bool {
	return c.Mode == IAMModeShadow || c.Mode == IAMModeEnforce
}

// 鉴权数据来源模式（iam.mode，docs/rbac-iam.md §15.1）。
const (
	// IAMModeOff 使用装配期静态角色表（回滚位）。
	IAMModeOff = "off"
	// IAMModeShadow 计算快照但按静态表放行，仅记录差异。
	IAMModeShadow = "shadow"
	// IAMModeEnforce 以 IAM 快照为唯一真相。
	IAMModeEnforce = "enforce"
)

// 快照缓存驱动。
const (
	// IAMCacheDriverMemory 仅进程内 L1（local / dev）。
	IAMCacheDriverMemory = "memory"
	// IAMCacheDriverRedis L1 + Redis（staging / prod enforce 必须）。
	IAMCacheDriverRedis = "redis"
)

// RBAC2 约束模式。
const (
	// IAMConstraintsModeOff 关闭约束校验。
	IAMConstraintsModeOff = "off"
	// IAMConstraintsModeShadow 只记录违规，不拦截（默认）。
	IAMConstraintsModeShadow = "shadow"
	// IAMConstraintsModeEnforce 违规写入返回 40900。
	IAMConstraintsModeEnforce = "enforce"
)
