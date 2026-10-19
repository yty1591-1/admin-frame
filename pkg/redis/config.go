package redis

import (
	"fmt"
	"time"
)

// Config 描述 Redis 连接与连接池参数。
//
// 各 cmd 可独立配置池大小；KeyPrefix 用于环境隔离（如 prod:admin）。
type Config struct {
	// Addr 地址，如 127.0.0.1:6379；必填。
	Addr string
	// Username ACL 用户名；可空（Redis <6 或默认用户）。
	Username string
	// Password 口令；禁止写入仓库真实值（SEC-01 / CFG-06）。
	Password string
	// DB 逻辑库编号；默认 0。
	DB int

	// PoolSize 连接池大小；0 表示使用包默认（10 * GOMAXPROCS）。
	PoolSize int
	// MinIdleConns 最小空闲连接；0 表示使用包默认（min(2, PoolSize)）。
	MinIdleConns int
	// DialTimeout 建连超时；0 表示 5s。
	DialTimeout time.Duration
	// ReadTimeout 读超时；0 表示 3s。
	ReadTimeout time.Duration
	// WriteTimeout 写超时；0 表示 3s。
	WriteTimeout time.Duration
	// PoolTimeout 从池取连接超时；0 表示 ReadTimeout+1s。
	PoolTimeout time.Duration
	// ConnMaxIdleTime 空闲连接回收；0 表示 5m。
	ConnMaxIdleTime time.Duration

	// KeyPrefix 自动加在业务 Key 前（Lock/Cache）；可空。勿含尾部冒号。
	KeyPrefix string
	// PingTimeout Open 时探测超时；0 表示 3s。
	PingTimeout time.Duration
	// SkipPing 为 true 时 Open 不 Ping（仅测试；生产勿开）。
	SkipPing bool
}

func (c *Config) applyDefaults() {
	if c.PoolSize == 0 {
		c.PoolSize = 10
	}
	if c.MinIdleConns == 0 {
		idle := 2
		if idle > c.PoolSize {
			idle = c.PoolSize
		}
		c.MinIdleConns = idle
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 3 * time.Second
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = 3 * time.Second
	}
	if c.PoolTimeout == 0 {
		c.PoolTimeout = c.ReadTimeout + time.Second
	}
	if c.ConnMaxIdleTime == 0 {
		c.ConnMaxIdleTime = 5 * time.Minute
	}
	if c.PingTimeout == 0 {
		c.PingTimeout = 3 * time.Second
	}
}

// Validate 校验必填项；不修改接收者。
func (c Config) Validate() error {
	if c.Addr == "" {
		return fmt.Errorf("%w: Addr is required", ErrInvalidConfig)
	}
	if c.DB < 0 {
		return fmt.Errorf("%w: DB must be >= 0", ErrInvalidConfig)
	}
	if c.PoolSize < 0 || c.MinIdleConns < 0 {
		return fmt.Errorf("%w: pool sizes must be >= 0", ErrInvalidConfig)
	}
	if c.PoolSize > 0 && c.MinIdleConns > c.PoolSize {
		return fmt.Errorf("%w: MinIdleConns (%d) > PoolSize (%d)", ErrInvalidConfig, c.MinIdleConns, c.PoolSize)
	}
	if c.PingTimeout < 0 {
		return fmt.Errorf("%w: PingTimeout must be >= 0", ErrInvalidConfig)
	}
	return nil
}
