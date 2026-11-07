package config

import "time"

// applyDefaults 在解码文件前填充平台段默认值。
//
// 只处理与其它字段无关的常量默认值；跨字段派生放在 normalize，
// 以免被文件或环境变量覆盖后失去一致性。域配置默认值由各域包自行填充。
func (c *Config) applyDefaults() {
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 15 * time.Second
	}
	if c.HTTP.ReadHeaderTimeout == 0 {
		c.HTTP.ReadHeaderTimeout = 5 * time.Second
	}
	if c.HTTP.ReadTimeout == 0 {
		c.HTTP.ReadTimeout = 15 * time.Second
	}
	if c.HTTP.WriteTimeout == 0 {
		c.HTTP.WriteTimeout = 30 * time.Second
	}
	if c.HTTP.IdleTimeout == 0 {
		c.HTTP.IdleTimeout = 60 * time.Second
	}
	if c.HTTP.MaxHeaderBytes == 0 {
		c.HTTP.MaxHeaderBytes = 1 << 20
	}
	if c.HTTP.Timeout == 0 {
		c.HTTP.Timeout = 30 * time.Second
	}
	if c.HTTP.BodyLimitBytes == 0 {
		c.HTTP.BodyLimitBytes = 32 << 20
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
	if c.DB.Driver == "" {
		c.DB.Driver = "memory"
	}
	if c.Storage.Driver == "" {
		c.Storage.Driver = "memory"
	}
	if c.Worker.PollInterval == 0 {
		c.Worker.PollInterval = 5 * time.Second
	}
	if c.Tracing.Exporter == "" {
		c.Tracing.Exporter = "none"
	}
	if c.ID.NodeIDSource == "" {
		c.ID.NodeIDSource = IDSourceEnv
	}
	if c.Idempotency.Driver == "" {
		c.Idempotency.Driver = IdempotencyDriverMemory
	}
	if c.Idempotency.Header == "" {
		c.Idempotency.Header = "Idempotency-Key"
	}
	if c.Idempotency.TTL == 0 {
		c.Idempotency.TTL = 24 * time.Hour
	}
	if c.Idempotency.MaxBodyBytes == 0 {
		c.Idempotency.MaxBodyBytes = 1 << 20
	}
	if c.RateLimit.Driver == "" {
		c.RateLimit.Driver = RateLimitDriverMemory
	}
	if c.RateLimit.Key == "" {
		c.RateLimit.Key = RateLimitKeyChannel
	}
	if c.RateLimit.FailureMode == "" {
		c.RateLimit.FailureMode = RateLimitFailureOpen
	}
	if c.RateLimit.IdleTTL == 0 {
		c.RateLimit.IdleTTL = 10 * time.Minute
	}
	c.applyAsyncDefaults()
}

// ApplyDefaults 是 applyDefaults 的导出入口，供组合加载层（cmd/internal/appconfig）复用。
func (c *Config) ApplyDefaults() {
	c.applyDefaults()
}

// applyAsyncDefaults 填充异步运行时默认值（默认关闭，保持现有行为）。
func (c *Config) applyAsyncDefaults() {
	if c.Async.Driver == "" {
		c.Async.Driver = AsyncDriverDB
	}
	if c.Async.Outbox.PollInterval == 0 {
		c.Async.Outbox.PollInterval = time.Second
	}
	if c.Async.Outbox.BatchSize == 0 {
		c.Async.Outbox.BatchSize = 100
	}
	if c.Async.Outbox.Lease == 0 {
		c.Async.Outbox.Lease = 30 * time.Second
	}
	if c.Async.Outbox.MaxAttempts == 0 {
		c.Async.Outbox.MaxAttempts = 10
	}
	if c.Async.Outbox.Concurrency == 0 {
		c.Async.Outbox.Concurrency = 2
	}
	if c.Async.Consumer.Concurrency == 0 {
		c.Async.Consumer.Concurrency = 4
	}
	if c.Async.Consumer.VisibilityTimeout == 0 {
		c.Async.Consumer.VisibilityTimeout = 60 * time.Second
	}
	if c.Async.Consumer.RetryBackoff == "" {
		c.Async.Consumer.RetryBackoff = AsyncBackoffExponential
	}
	if c.Async.Broker.Stream == "" {
		c.Async.Broker.Stream = "async"
	}
	if c.Async.Broker.SubjectPrefix == "" {
		c.Async.Broker.SubjectPrefix = "async"
	}
	if c.Async.Broker.AckWait == 0 {
		c.Async.Broker.AckWait = 30 * time.Second
	}
	if c.Async.Broker.MaxDeliver == 0 {
		c.Async.Broker.MaxDeliver = 10
	}
}

// normalize 在环境变量覆盖后、Validate 前补齐跨字段派生值并解析托管密钥。
//
// auth.expected_channel 未显式声明时跟随 channel，保证环境变量改写
// channel 后两者仍然一致；auth.mode 未声明时按环境回退（CFG-04）。
func (c *Config) normalize() error {
	if err := c.NormalizeCore(); err != nil {
		return err
	}
	// 密钥解析放在最后：它覆盖的是其它字段的取值，必须在派生逻辑稳定之后执行。
	return c.resolveSecrets()
}

// NormalizeCore 补齐平台段跨字段派生值，不解析托管密钥。
//
// 组合加载层需要把域配置的密钥一并交给同一个 Provider 解析，因此把密钥解析
// 与派生逻辑拆开，由调用方决定解析时机。
func (c *Config) NormalizeCore() error {
	if c.Auth.ExpectedChannel == "" {
		c.Auth.ExpectedChannel = c.Channel
	}
	c.Auth.Mode = c.Auth.effectiveMode(c.Env)
	return nil
}
