package config

import (
	"time"
)

// IDConfig 是分布式 ID 生成参数（多副本必须使用不同节点号）。
type IDConfig struct {
	// NodeID 节点号 [0, 1023]；未配置表示未显式声明。
	NodeID *int64 `yaml:"node_id"`
	// NodeIDSource config | env | statefulset。
	NodeIDSource string `yaml:"node_id_source"`
}

// 幂等存储驱动。
const (
	// IdempotencyDriverMemory 进程内存储（单副本 / 本地）。
	IdempotencyDriverMemory = "memory"
	// IdempotencyDriverRedis 跨副本共享（prod/staging 启用时强制）。
	IdempotencyDriverRedis = "redis"
)

// IdempotencyConfig 是写请求幂等去重参数（internal/platform 统一挂载）。
type IdempotencyConfig struct {
	// Enabled 是否启用；默认关闭。
	Enabled bool `yaml:"enabled"`
	// Driver memory | redis；prod/staging 启用时必须 redis。
	Driver string `yaml:"driver"`
	// Header 幂等键请求头，默认 Idempotency-Key。
	Header string `yaml:"header"`
	// TTL 记录存活时间，默认 24h。
	TTL time.Duration `yaml:"ttl"`
	// Required 为 true 时写方法缺少幂等键直接拒绝。
	Required bool `yaml:"required"`
	// MaxBodyBytes 参与指纹与缓存的请求体上限，默认 1 MiB。
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
	// DegradeOnError 幂等存储不可用时是否放行并标记（P1-4）；
	// 默认 false（失败关闭），生产建议 true 以避免 Redis 单点放大为全站写不可用。
	DegradeOnError bool `yaml:"degrade_on_error"`
}

// 限流驱动与失败策略。
const (
	// RateLimitDriverMemory 进程内令牌桶（单副本）。
	RateLimitDriverMemory = "memory"
	// RateLimitDriverRedis 跨副本共享令牌桶（prod/staging 启用时强制）。
	RateLimitDriverRedis = "redis"
	// RateLimitFailureOpen Redis 故障时放行。
	RateLimitFailureOpen = "fail_open"
	// RateLimitFailureClosed Redis 故障时拒绝。
	RateLimitFailureClosed = "fail_closed"
)

// 限流分键策略。
const (
	// RateLimitKeyChannel 按端别分桶（默认，键数量有界）。
	RateLimitKeyChannel = "channel"
	// RateLimitKeyChannelUser 按端别 + 用户分桶，匿名时回退 IP。
	RateLimitKeyChannelUser = "channel_user"
	// RateLimitKeyChannelTenantUser 按端别 + 商户 + 用户分桶，匿名时回退 IP。
	RateLimitKeyChannelTenantUser = "channel_tenant_user"
	// RateLimitKeyClient 按客户端 IP 分桶。
	RateLimitKeyClient = "client"
)

// MaxNodeID 与 pkg/id 的雪花节点号上限一致。
const MaxNodeID = 1023

// 异步驱动与退避策略（async.driver / async.consumer.retry_backoff）。
const (
	// AsyncDriverDB 使用数据库事务性 Outbox（默认）。
	AsyncDriverDB = "db"
	// AsyncDriverNATS 在 DB Outbox 之上把事件发布到 NATS JetStream。
	AsyncDriverNATS = "nats"
	// AsyncBackoffExponential 指数退避。
	AsyncBackoffExponential = "exponential"
	// AsyncBackoffFixed 固定间隔重试。
	AsyncBackoffFixed = "fixed"
)
