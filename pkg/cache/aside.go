package cache

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"golang.org/x/sync/singleflight"
)

// Option 配置 Cache-Aside 门面。
type Option func(*Cache)

// WithTTL 设置 GetOrLoad 默认 TTL（必须 >0 才能 GetOrLoad）。
func WithTTL(ttl time.Duration) Option {
	return func(c *Cache) { c.ttl = ttl }
}

// WithJitter 为 TTL 增加 [0, jitter) 随机增量，缓解雪崩。
func WithJitter(jitter time.Duration) Option {
	return func(c *Cache) { c.jitter = jitter }
}

// WithNegativeTTL 未命中且 loader 返回 (nil, nil) 时写入短 TTL 空值占位。
// 仅当 Store 为 *Memory 或实现 NegativeSetter 时生效；否则跳过占位。
func WithNegativeTTL(ttl time.Duration) Option {
	return func(c *Cache) { c.negativeTTL = ttl }
}

// WithClock 注入时钟（影响 jitter 与测试）。
func WithClock(clock Clock) Option {
	return func(c *Cache) {
		if clock != nil {
			c.clock = clock
		}
	}
}

// NegativeSetter 支持空值占位的 Store（如 Memory）。
type NegativeSetter interface {
	SetNegative(ctx context.Context, key string, ttl time.Duration) error
}

// Cache 是 Cache-Aside 门面：委托 Store，并提供 GetOrLoad（单飞、抖动、可选空值缓存）。
type Cache struct {
	store       Store
	group       singleflight.Group
	ttl         time.Duration
	jitter      time.Duration
	negativeTTL time.Duration
	clock       Clock
}

// New 基于 Store 创建门面。
func New(store Store, opts ...Option) (*Cache, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalidConfig)
	}
	c := &Cache{store: store, clock: RealClock()}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c, nil
}

// Store 返回底层 Store。
func (c *Cache) Store() Store {
	if c == nil {
		return nil
	}
	return c.store
}

// Get 委托 Store.Get。
func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.store == nil {
		return nil, ErrClosed
	}
	return c.store.Get(ctx, key)
}

// Set 委托 Store.Set；应用 jitter。
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if c == nil || c.store == nil {
		return ErrClosed
	}
	return c.store.Set(ctx, key, value, c.applyJitter(ttl))
}

// Delete 委托 Store.Delete。
func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	if c == nil || c.store == nil {
		return ErrClosed
	}
	return c.store.Delete(ctx, keys...)
}

// GetOrLoad 先读缓存；未命中则 loader 并回填。
//
//   - loader 返回 error → 不写缓存
//   - loader 返回 (nil, nil) 且配置了 NegativeTTL → 写空值占位，本次返回 ErrNegative
//   - 同进程同 key 并发合并为一次 loader
func (c *Cache) GetOrLoad(ctx context.Context, key string, loader Loader) ([]byte, error) {
	if err := c.validateGetOrLoad(key, loader); err != nil {
		return nil, err
	}

	if val, err := c.lookup(ctx, key); err != ErrMiss {
		return val, err
	}

	v, err, _ := c.group.Do(key, func() (any, error) {
		return c.loadAndFill(ctx, key, loader)
	})
	if err != nil {
		return nil, err
	}
	out, _ := v.([]byte)
	return out, nil
}

func (c *Cache) validateGetOrLoad(key string, loader Loader) error {
	if c == nil || c.store == nil {
		return ErrClosed
	}
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidConfig)
	}
	if loader == nil {
		return fmt.Errorf("%w: nil loader", ErrInvalidConfig)
	}
	if c.ttl <= 0 {
		return fmt.Errorf("%w: default TTL required (WithTTL)", ErrInvalidConfig)
	}
	return nil
}

// lookup 读取并归一化：实值 / ErrNegative / ErrMiss / 其他错误。
func (c *Cache) lookup(ctx context.Context, key string) ([]byte, error) {
	val, err := c.store.Get(ctx, key)
	switch {
	case err == nil:
		return val, nil
	case err == ErrNegative, err == ErrMiss:
		return nil, err
	default:
		return nil, err
	}
}

// loadAndFill 在单飞内二次查找，未命中则 loader 并回填（含可选空值占位）。
func (c *Cache) loadAndFill(ctx context.Context, key string, loader Loader) (any, error) {
	if val, err := c.lookup(ctx, key); err != ErrMiss {
		if err != nil {
			return nil, err
		}
		return val, nil
	}

	val, err := loader(ctx)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, c.storeNegative(ctx, key)
	}
	if err := c.store.Set(ctx, key, val, c.applyJitter(c.ttl)); err != nil {
		return nil, err
	}
	return val, nil
}

func (c *Cache) storeNegative(ctx context.Context, key string) error {
	if c.negativeTTL <= 0 {
		return ErrMiss
	}
	if ns, ok := c.store.(NegativeSetter); ok {
		if err := ns.SetNegative(ctx, key, c.applyJitter(c.negativeTTL)); err != nil {
			return err
		}
	}
	return ErrNegative
}

func (c *Cache) applyJitter(ttl time.Duration) time.Duration {
	if ttl <= 0 || c.jitter <= 0 {
		return ttl
	}
	j := c.jitter
	if j > ttl {
		j = ttl
	}
	extra := time.Duration(randUint64() % uint64(j))
	if ttl > time.Duration(math.MaxInt64)-extra {
		return ttl
	}
	return ttl + extra
}

func randUint64() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint64(time.Now().UnixNano())
	}
	return binary.LittleEndian.Uint64(b[:])
}
