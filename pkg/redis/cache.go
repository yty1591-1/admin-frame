package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// Cache 提供带前缀的 KV 缓存（Cache-Aside 友好）；值为原始字节，序列化由调用方负责。
//
// 必须设置 TTL；禁止缓存未脱敏敏感信息；Key 须含租户等隔离维度。
type Cache struct {
	client *Client
	group  *singleflight.Group
}

// Get 读取缓存；不存在返回 ErrMiss。
func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("%w: cache key is required", ErrInvalidConfig)
	}
	val, err := c.client.rdb.Get(ctx, c.full(key)).Bytes()
	if err == goredis.Nil {
		return nil, ErrMiss
	}
	if err != nil {
		return nil, fmt.Errorf("redis: cache get: %w", err)
	}
	return val, nil
}

// Set 写入缓存；ttl 必须 >0（防无过期键堆积）。
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := c.ready(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("%w: cache key is required", ErrInvalidConfig)
	}
	if ttl <= 0 {
		return fmt.Errorf("%w: cache ttl must be > 0", ErrInvalidConfig)
	}
	if err := c.client.rdb.Set(ctx, c.full(key), value, ttl).Err(); err != nil {
		return fmt.Errorf("redis: cache set: %w", err)
	}
	return nil
}

// SetNX 仅在 key 不存在时写入，用于一次性消费 / 互斥标记；返回是否写入成功。
//
// 与 Set 相同，ttl 必须 >0，避免无过期键堆积。
func (c *Cache) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if err := c.ready(); err != nil {
		return false, err
	}
	if key == "" {
		return false, fmt.Errorf("%w: cache key is required", ErrInvalidConfig)
	}
	if ttl <= 0 {
		return false, fmt.Errorf("%w: cache ttl must be > 0", ErrInvalidConfig)
	}
	ok, err := c.client.rdb.SetNX(ctx, c.full(key), value, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis: cache setnx: %w", err)
	}
	return ok, nil
}

// incrScript 在 key 首次创建时设置过期时间，保证计数窗口不会永不过期。
//
// INCR 与 PEXPIRE 在同一脚本内执行，避免进程中断导致无过期键。
var incrScript = goredis.NewScript(`
local v = redis.call('INCR', KEYS[1])
if v == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return v
`)

// Incr 原子自增计数；key 首次创建时设置 ttl（必须 >0），否则保留原 TTL。
func (c *Cache) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if err := c.ready(); err != nil {
		return 0, err
	}
	if key == "" {
		return 0, fmt.Errorf("%w: cache key is required", ErrInvalidConfig)
	}
	if ttl <= 0 {
		return 0, fmt.Errorf("%w: cache ttl must be > 0", ErrInvalidConfig)
	}
	val, err := incrScript.Run(ctx, c.client.rdb, []string{c.full(key)}, ttl.Milliseconds()).Int64()
	if err != nil {
		return 0, fmt.Errorf("redis: cache incr: %w", err)
	}
	return val, nil
}

// Delete 删除一个或多个业务 key（自动加前缀）。
func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	if err := c.ready(); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	full := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		full = append(full, c.full(k))
	}
	if len(full) == 0 {
		return nil
	}
	if err := c.client.rdb.Del(ctx, full...).Err(); err != nil {
		return fmt.Errorf("redis: cache delete: %w", err)
	}
	return nil
}

// Loader 在缓存未命中时加载真相数据（通常查 DB）。
type Loader func(ctx context.Context) ([]byte, error)

// GetOrLoad 先读缓存；未命中则调用 loader，成功后回填（含空值可选由 loader 返回）。
//
// 同一进程内对相同 key 的并发未命中会合并为一次 loader（缓解击穿）；
// 跨实例击穿需配合短 TTL 空值或锁（见 Locker）。
func (c *Cache) GetOrLoad(ctx context.Context, key string, ttl time.Duration, loader Loader) ([]byte, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	if loader == nil {
		return nil, fmt.Errorf("%w: nil loader", ErrInvalidConfig)
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("%w: cache ttl must be > 0", ErrInvalidConfig)
	}

	if val, err := c.Get(ctx, key); err == nil {
		return val, nil
	} else if err != ErrMiss {
		return nil, err
	}

	fk := c.full(key)
	v, err, _ := c.group.Do(fk, func() (any, error) {
		// double-check after winning the flight
		if val, err := c.Get(ctx, key); err == nil {
			return val, nil
		} else if err != ErrMiss {
			return nil, err
		}
		val, err := loader(ctx)
		if err != nil {
			return nil, err
		}
		if err := c.Set(ctx, key, val, ttl); err != nil {
			return nil, err
		}
		return val, nil
	})
	if err != nil {
		return nil, err
	}
	out, _ := v.([]byte)
	return out, nil
}

func (c *Cache) ready() error {
	if c == nil || c.client == nil || c.client.rdb == nil || c.client.closed.Load() {
		return ErrClosed
	}
	return nil
}

func (c *Cache) full(key string) string {
	return c.client.FullKey(JoinPrefix("cache", key))
}
