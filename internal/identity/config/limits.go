package config

import "time"

// 限流故障策略取值（与平台 rate_limit.failure_mode 语义一致）。
const (
	failureModeClosed = "fail_closed"
	failureModeOpen   = "fail_open"
)

// SMS 供应商熔断默认值（IDN-P2-05）。
const (
	// identityDefaultSMSBreakerFailures 连续失败阈值。
	identityDefaultSMSBreakerFailures = 5
	// identityDefaultSMSBreakerTimeout 熔断打开时长。
	identityDefaultSMSBreakerTimeout = 60 * time.Second
	// identityMinSMSBreakerTimeout 允许配置的最小熔断时长。
	identityMinSMSBreakerTimeout = time.Second
	// identityMaxSMSBreakerTimeout 允许配置的最大熔断时长。
	identityMaxSMSBreakerTimeout = 10 * time.Minute
)

// identityDefaultMFAIssuer 与 internal/identity/app.DefaultMFAIssuer 保持一致。
const identityDefaultMFAIssuer = "AdminFrame"

// 账号撤销快照陈旧度边界，与 internal/identity/app 保持一致（docs/identity-authn.md §6.1）。
const (
	// identityMinRevocationStateTTL 快照最小陈旧度，避免每请求回源。
	identityMinRevocationStateTTL = time.Second
	// identityMaxRevocationStateTTL 快照硬上限，对应「≤60s 生效」的安全承诺。
	identityMaxRevocationStateTTL = 60 * time.Second
)

// 撤销本地缓存参数（IDN-GAP-P1-02）。
const (
	// identityDefaultRevocationLocalCacheTTL 默认 L1 TTL，与 ≤1s 撤销可见承诺一致。
	identityDefaultRevocationLocalCacheTTL = time.Second
	// identityMaxRevocationLocalCacheTTL L1 TTL 硬上限，避免配置过大放大撤销延迟。
	identityMaxRevocationLocalCacheTTL = 5 * time.Second
	// identityDefaultRevocationLocalCacheCapacity 默认 L1 容量上限。
	identityDefaultRevocationLocalCacheCapacity = 4096
	// identityMaxRevocationLocalCacheCapacity 允许配置的 L1 容量上限。
	identityMaxRevocationLocalCacheCapacity = 100000
	// identityDefaultRevocationTokenGrace 默认 jti 本地宽限。
	identityDefaultRevocationTokenGrace = 30 * time.Second
	// identityMaxRevocationTokenGrace 宽限硬上限，避免放大撤销延迟。
	identityMaxRevocationTokenGrace = 60 * time.Second
	// identityDefaultConsumerDeleteReauthWindow 默认自助注销再认证窗口。
	identityDefaultConsumerDeleteReauthWindow = 10 * time.Minute
	// identityMinConsumerDeleteReauthWindow 再认证窗口下限。
	identityMinConsumerDeleteReauthWindow = time.Minute
	// identityMaxConsumerDeleteReauthWindow 再认证窗口上限。
	identityMaxConsumerDeleteReauthWindow = 30 * time.Minute
)

// 密码策略边界，与 internal/identity/app 默认值保持一致（docs/identity-authn.md §10）。
const (
	// identityDefaultPasswordMinLength 默认最小字符数。
	identityDefaultPasswordMinLength = 12
	// identityDefaultPasswordMaxLength 默认最大字符数。
	identityDefaultPasswordMaxLength = 128
	// identityMinPasswordLength 允许配置的最小长度下限。
	identityMinPasswordLength = 8
)

// 密码校验并发保护默认值（IDN-P2-05）。
const (
	// identityDefaultPasswordWaitTimeout 获取并发槽位的默认等待上限。
	identityDefaultPasswordWaitTimeout = 2 * time.Second
	// identityMinPasswordWaitTimeout 允许配置的最小等待上限。
	identityMinPasswordWaitTimeout = 100 * time.Millisecond
	// identityMaxPasswordWaitTimeout 允许配置的最大等待上限。
	identityMaxPasswordWaitTimeout = 30 * time.Second
)
