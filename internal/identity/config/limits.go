package config

import (
	"regexp"
	"time"
)

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

// M2M 机器身份边界（docs/identity-authn.md §21.2.6 / AC-P16-M2M-*）。
const (
	// identityDefaultM2MTokenTTL 机器令牌默认有效期。
	identityDefaultM2MTokenTTL = 15 * time.Minute
	// identityMinM2MTokenTTL 令牌有效期下限。
	identityMinM2MTokenTTL = 5 * time.Minute
	// identityMaxM2MTokenTTL 令牌有效期上限（1h），限制泄露后的可利用窗口。
	identityMaxM2MTokenTTL = time.Hour
	// identityDefaultM2MRotateGrace 旧 secret 默认退役宽限。
	identityDefaultM2MRotateGrace = 24 * time.Hour
	// identityMaxM2MRotateGrace 退役宽限上限。
	identityMaxM2MRotateGrace = 168 * time.Hour
	// identityDefaultM2MMaxSecretAge secret 默认最长生命周期（90d）。
	identityDefaultM2MMaxSecretAge = 2160 * time.Hour
	// identityMinM2MMaxSecretAge secret 生命周期下限（30d）。
	identityMinM2MMaxSecretAge = 720 * time.Hour
	// identityMaxM2MMaxSecretAge secret 生命周期上限（365d）。
	identityMaxM2MMaxSecretAge = 8760 * time.Hour
	// identityDefaultM2MRPS 签发端点默认按 client_id 限流速率。
	identityDefaultM2MRPS = 5
	// identityDefaultM2MBurst 签发端点默认突发容量。
	identityDefaultM2MBurst = 10
)

// SCIM 2.0 边界（docs/identity-authn.md §21.1.7 / AC-P16-SCIM-*）。
const (
	// identityDefaultSCIMPageSize SCIM 列表默认每页条数。
	identityDefaultSCIMPageSize = 50
	// identityMinSCIMPageSize 允许配置的最小每页条数。
	identityMinSCIMPageSize = 1
	// identityMaxSCIMPageSize 允许配置的最大每页条数。
	identityMaxSCIMPageSize = 100
	// identityDefaultSCIMMaxPayload 单请求体默认上限（1 MiB）。
	identityDefaultSCIMMaxPayload = 1 << 20
	// identityMinSCIMMaxPayload 请求体下限（64 KiB）。
	identityMinSCIMMaxPayload = 64 << 10
	// identityMaxSCIMMaxPayload 请求体上限（2 MiB）。
	identityMaxSCIMMaxPayload = 2 << 20
	// identityDefaultSCIMRequestTimeout 单请求默认处理超时。
	identityDefaultSCIMRequestTimeout = 10 * time.Second
	// identityMinSCIMRequestTimeout 处理超时下限。
	identityMinSCIMRequestTimeout = time.Second
	// identityMaxSCIMRequestTimeout 处理超时上限。
	identityMaxSCIMRequestTimeout = 30 * time.Second
	// identityDefaultSCIMTokenGrace 旧 SCIM 令牌默认退役宽限（168h）。
	identityDefaultSCIMTokenGrace = 168 * time.Hour
	// identityMaxSCIMTokenGrace 退役宽限上限（720h）。
	identityMaxSCIMTokenGrace = 720 * time.Hour
	// identityDefaultSCIMRPS SCIM 端点默认按令牌限流速率。
	identityDefaultSCIMRPS = 10
	// identityDefaultSCIMBurst SCIM 端点默认突发容量。
	identityDefaultSCIMBurst = 20
	// identityMaxSCIMActiveTokens 每 provider 允许同时生效的令牌数（当前 + 待退役）。
	identityMaxSCIMActiveTokens = 2
)

// identitySCIMProviderPattern 约束 provider 命名：小写字母开头，允许 [a-z0-9_-]，2～32 字符。
var identitySCIMProviderPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// identitySCIMRoleCodePattern 约束 group_role_map 的角色 code：与 IAM 角色编码一致。
var identitySCIMRoleCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
