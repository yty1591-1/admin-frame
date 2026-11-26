package config

import (
	"time"
)

// AsyncConfig 是平台级异步基础设施参数（ENT-03）。
//
// 默认关闭；启用后由 Relay 消费 async_outbox，消费方须幂等。
// driver=nats 时 Relay 仍以 DB Outbox 为事实源，额外把事件发布到 JetStream。
type AsyncConfig struct {
	// Enabled 是否启用异步运行时；默认 false。
	Enabled bool `yaml:"enabled"`
	// Driver 驱动；db（默认）| nats。
	Driver string `yaml:"driver"`
	// Broker 外部消息驱动参数；driver=nats 时生效。
	Broker AsyncBrokerConfig `yaml:"broker"`
	// Outbox 投递参数。
	Outbox AsyncOutboxConfig `yaml:"outbox"`
	// Consumer 消费运行时参数。
	Consumer AsyncConsumerConfig `yaml:"consumer"`
}

// AsyncBrokerConfig 是 NATS JetStream 连接与投递参数。
//
// URL 可包含用户名/口令，登记为配置中心敏感键 async.broker.url（密钥子域）。
type AsyncBrokerConfig struct {
	// URL NATS 服务地址；driver=nats 时必填，支持 nats:// / tls:// / ws:// / wss://。
	URL string `yaml:"url"`
	// Stream JetStream 流名称；空值取 async。
	Stream string `yaml:"stream"`
	// SubjectPrefix 主题前缀；空值取 async，最终主题为 <prefix>.<topic>。
	SubjectPrefix string `yaml:"subject_prefix"`
	// AckWait 单条消息确认超时；[1s, 30m]，空值取 30s。
	AckWait time.Duration `yaml:"ack_wait"`
	// MaxDeliver 最大投递次数；超过后进入 DLQ；[1, 100]，空值取 10。
	MaxDeliver int `yaml:"max_deliver"`
}

// AsyncOutboxConfig 是 Outbox Relay 参数。
type AsyncOutboxConfig struct {
	// PublishInAPI 是否在 API 进程内启动 Relay；生产强制 false，由 worker 独占。
	PublishInAPI bool `yaml:"publish_in_api"`
	// PollInterval 无到期事件时的休眠；[100ms, 1m]。
	PollInterval time.Duration `yaml:"poll_interval"`
	// BatchSize 单 worker 每轮认领上限；[1, 500]。
	BatchSize int `yaml:"batch_size"`
	// Lease processing 租约；[5s, 10m]。
	Lease time.Duration `yaml:"lease"`
	// MaxAttempts 最大投递次数；重试耗尽进入死信；[1, 20]。
	MaxAttempts int `yaml:"max_attempts"`
	// Concurrency Relay 并发 worker 数；[1, 16]。
	Concurrency int `yaml:"concurrency"`
}

// AsyncConsumerConfig 是消费运行时参数。
type AsyncConsumerConfig struct {
	// Concurrency 消费并发度；[1, 32]。
	Concurrency int `yaml:"concurrency"`
	// VisibilityTimeout 消费幂等记录的处理中接管窗口；[5s, 30m]。
	VisibilityTimeout time.Duration `yaml:"visibility_timeout"`
	// RetryBackoff exponential | fixed。
	RetryBackoff string `yaml:"retry_backoff"`
}
