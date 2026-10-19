package db

import (
	"fmt"
	"time"
)

// Config 描述打开 *sql.DB 所需的驱动、DSN 与连接池参数。
//
// 各 cmd 应独立配置池大小（架构：三端连接池分别可配）；默认值偏保守，生产按压测调整。
type Config struct {
	// Driver 已注册的 database/sql 驱动名，如 postgres、mysql；不得为 memory。
	Driver string
	// DSN 连接串；禁止写入仓库真实口令（CFG-06 / SEC-01）。
	DSN string

	// MaxOpenConns 最大打开连接数；0 表示使用包默认（25）。
	MaxOpenConns int
	// MaxIdleConns 最大空闲连接；0 表示使用包默认（min(5, MaxOpenConns)）。
	MaxIdleConns int
	// ConnMaxLifetime 连接最大存活时间；0 表示使用包默认（30m）。
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime 空闲连接最大存活；0 表示使用包默认（5m）。
	ConnMaxIdleTime time.Duration

	// PingTimeout Open 时探测超时；0 表示 3s。
	PingTimeout time.Duration
	// SkipPing 为 true 时 Open 不 Ping（仅测试或特殊场景；生产勿开）。
	SkipPing bool
}

// applyDefaults 填充未显式设置的池与超时默认值。
func (c *Config) applyDefaults() {
	if c.MaxOpenConns == 0 {
		c.MaxOpenConns = 25
	}
	if c.MaxIdleConns == 0 {
		idle := 5
		if c.MaxOpenConns > 0 && idle > c.MaxOpenConns {
			idle = c.MaxOpenConns
		}
		c.MaxIdleConns = idle
	}
	if c.ConnMaxLifetime == 0 {
		c.ConnMaxLifetime = 30 * time.Minute
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
	if c.Driver == "" {
		return fmt.Errorf("%w: driver is required", ErrInvalidConfig)
	}
	if c.Driver == "memory" {
		return fmt.Errorf("%w: driver %q is not a database/sql driver", ErrInvalidConfig, c.Driver)
	}
	if c.DSN == "" {
		return fmt.Errorf("%w: dsn is required", ErrInvalidConfig)
	}
	if c.MaxOpenConns < 0 {
		return fmt.Errorf("%w: MaxOpenConns must be >= 0", ErrInvalidConfig)
	}
	if c.MaxIdleConns < 0 {
		return fmt.Errorf("%w: MaxIdleConns must be >= 0", ErrInvalidConfig)
	}
	if c.MaxOpenConns > 0 && c.MaxIdleConns > c.MaxOpenConns {
		return fmt.Errorf("%w: MaxIdleConns (%d) > MaxOpenConns (%d)", ErrInvalidConfig, c.MaxIdleConns, c.MaxOpenConns)
	}
	if c.PingTimeout < 0 {
		return fmt.Errorf("%w: PingTimeout must be >= 0", ErrInvalidConfig)
	}
	return nil
}
