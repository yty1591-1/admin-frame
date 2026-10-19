package redis

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// Client 包装 go-redis 客户端，统一装配、关闭与就绪探测。
//
// 并发安全：底层客户端可并发使用；Close 至多生效一次。
type Client struct {
	rdb    *goredis.Client
	prefix string
	sf     singleflight.Group
	closed atomic.Bool
}

// Open 创建客户端并（默认）Ping 验证连通性。
func Open(ctx context.Context, cfg Config) (*Client, error) {
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr:            cfg.Addr,
		Username:        cfg.Username,
		Password:        cfg.Password,
		DB:              cfg.DB,
		PoolSize:        cfg.PoolSize,
		MinIdleConns:    cfg.MinIdleConns,
		DialTimeout:     cfg.DialTimeout,
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		PoolTimeout:     cfg.PoolTimeout,
		ConnMaxIdleTime: cfg.ConnMaxIdleTime,
	})

	c := &Client{rdb: rdb, prefix: strings.Trim(cfg.KeyPrefix, ":")}
	if !cfg.SkipPing {
		pingCtx := ctx
		var cancel context.CancelFunc
		if cfg.PingTimeout > 0 {
			pingCtx, cancel = context.WithTimeout(ctx, cfg.PingTimeout)
			defer cancel()
		}
		if err := c.Ping(pingCtx); err != nil {
			_ = rdb.Close()
			return nil, err
		}
	}
	return c, nil
}

// Redis 返回底层 *redis.Client；调用方勿 Close（应通过 Client.Close）。
func (c *Client) Redis() *goredis.Client {
	if c == nil {
		return nil
	}
	return c.rdb
}

// Prefix 返回配置的 Key 前缀。
func (c *Client) Prefix() string {
	if c == nil {
		return ""
	}
	return c.prefix
}

// FullKey 将业务 key 加上客户端前缀。
func (c *Client) FullKey(key string) string {
	if c == nil {
		return key
	}
	return JoinPrefix(c.prefix, key)
}

// Ping 探测连通性。
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil || c.closed.Load() {
		return ErrClosed
	}
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: ping: %w", err)
	}
	return nil
}

// Checker 返回就绪检查函数，可交给 pkg/health.Handler.Register。
func (c *Client) Checker() func() error {
	return func() error {
		if c == nil || c.rdb == nil || c.closed.Load() {
			return ErrClosed
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return c.Ping(ctx)
	}
}

// Lock 返回基于本客户端的分布式锁工厂。
func (c *Client) Lock() *Locker {
	return &Locker{client: c}
}

// Cache 返回基于本客户端的 KV 缓存（字节值；调用方自行序列化）。
func (c *Client) Cache() *Cache {
	return &Cache{client: c, group: &c.sf}
}

// Close 关闭连接池；可重复调用。
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if err := c.rdb.Close(); err != nil {
		return fmt.Errorf("redis: close: %w", err)
	}
	return nil
}
