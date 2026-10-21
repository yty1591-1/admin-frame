// Package cache 提供 IAM 鉴权快照的两级缓存（L1 进程内 + L2 Redis）。
//
// 职责边界：
//   - 只做「字节缓存 + 版本化编解码 + 失效」，不做权限判定；
//   - 回源由 internal/iam/app.AuthorizeService 负责（含 singleflight 与超时）；
//   - L1 / L2 均为 pkg/cache.Store，便于测试注入与本地单层运行。
//
// 失败策略（docs/rbac-iam.md §8.4）：
//   - L2 读错误不阻断，降级回源 DB；
//   - 反序列化失败视为 miss 并删除坏 key，绝不 panic，也绝不放行；
//   - 删除失败必须可观测（Observer），由 L1 3s / L2 5m 过期兜底。
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/authz"
	pkgcache "github.com/admin-frame/admin-frame/pkg/cache"
)

// Layer 标识缓存层或回源来源，用于指标标签。
type Layer string

const (
	// LayerL1 进程内缓存。
	LayerL1 Layer = "l1"
	// LayerL2 分布式缓存（Redis）。
	LayerL2 Layer = "l2"
	// LayerDB 回源数据库。
	LayerDB Layer = "db"
)

// 缓存观测结果标签。
const (
	// ResultHit 命中。
	ResultHit = "hit"
	// ResultMiss 未命中。
	ResultMiss = "miss"
	// ResultError 读错误（已降级）。
	ResultError = "error"
)

// Observer 观测缓存命中与失效失败；实现须并发安全且不得阻塞。
type Observer interface {
	// OnCache 记录一次缓存读取结果（hit / miss / error）。
	OnCache(layer Layer, result string)
	// OnInvalidateFailure 记录一次缓存删除失败。
	OnInvalidateFailure(layer Layer)
}

// NopObserver 是不做任何观测的默认实现。
type NopObserver struct{}

// OnCache 实现 Observer。
func (NopObserver) OnCache(Layer, string) {}

// OnInvalidateFailure 实现 Observer。
func (NopObserver) OnInvalidateFailure(Layer) {}

// Config 是两级缓存配置；时间参数必须为正。
type Config struct {
	// L1 进程内缓存；nil 表示不使用 L1（仅测试 / 特殊部署允许）。
	L1 pkgcache.Store
	// L2 分布式缓存；nil 表示单层运行。
	L2 pkgcache.Store
	// L1TTL L1 过期时间（默认 3s），权限撤销的最坏生效窗口。
	L1TTL time.Duration
	// L2TTL L2 过期时间（默认 5m）。
	L2TTL time.Duration
	// NegativeTTL 空快照过期时间（默认 30s），防止异常主体打穿到 DB。
	NegativeTTL time.Duration
	// Observer 可选观测器。
	Observer Observer
}

// SnapshotCache 是 L1 / L2 组合的鉴权快照缓存。
type SnapshotCache struct {
	l1          pkgcache.Store
	l2          pkgcache.Store
	l1TTL       time.Duration
	l2TTL       time.Duration
	negativeTTL time.Duration
	observer    Observer
}

// NewSnapshotCache 校验配置并构造缓存；至少需要一层。
func NewSnapshotCache(cfg Config) (*SnapshotCache, error) {
	if cfg.L1 == nil && cfg.L2 == nil {
		return nil, fmt.Errorf("%w: at least one cache layer is required", ErrInvalidConfig)
	}
	if cfg.L1TTL <= 0 || cfg.L2TTL <= 0 || cfg.NegativeTTL <= 0 {
		return nil, fmt.Errorf("%w: l1_ttl, l2_ttl and negative_ttl must be > 0", ErrInvalidConfig)
	}
	if cfg.NegativeTTL > cfg.L2TTL {
		return nil, fmt.Errorf("%w: negative_ttl must be <= l2_ttl", ErrInvalidConfig)
	}
	observer := cfg.Observer
	if observer == nil {
		observer = NopObserver{}
	}
	return &SnapshotCache{
		l1:          cfg.L1,
		l2:          cfg.L2,
		l1TTL:       cfg.L1TTL,
		l2TTL:       cfg.L2TTL,
		negativeTTL: cfg.NegativeTTL,
		observer:    observer,
	}, nil
}

// Get 依次读取 L1 → L2；L2 命中时回填 L1。
//
// 返回：
//   - 命中：快照与来源层；
//   - 未命中 / 缓存降级：pkg/cache.ErrMiss；
//   - 配置错误：ErrInvalidConfig。
//
// 反序列化失败按 miss 处理并删除坏 key，避免持续命中脏数据。
func (c *SnapshotCache) Get(ctx context.Context, key string) (authz.Snapshot, Layer, error) {
	if err := c.ready(); err != nil {
		return authz.Snapshot{}, "", err
	}
	if key == "" {
		return authz.Snapshot{}, "", fmt.Errorf("%w: cache key is required", ErrInvalidConfig)
	}

	if c.l1 != nil {
		if raw, ok := c.readRaw(ctx, LayerL1, c.l1, key); ok {
			if snapshot, err := Decode(raw); err == nil {
				c.observer.OnCache(LayerL1, ResultHit)
				return snapshot, LayerL1, nil
			}
			c.discard(ctx, LayerL1, c.l1, key)
		}
	}
	if c.l2 != nil {
		if raw, ok := c.readRaw(ctx, LayerL2, c.l2, key); ok {
			if snapshot, err := Decode(raw); err == nil {
				c.observer.OnCache(LayerL2, ResultHit)
				c.backfillL1(ctx, key, raw)
				return snapshot, LayerL2, nil
			}
			c.discard(ctx, LayerL2, c.l2, key)
		}
	}
	return authz.Snapshot{}, "", pkgcache.ErrMiss
}

// readRaw 读取单层原始字节；未命中 / 读错误均返回 ok=false 并记录指标。
func (c *SnapshotCache) readRaw(ctx context.Context, layer Layer, store pkgcache.Store, key string) ([]byte, bool) {
	raw, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, pkgcache.ErrMiss) {
			c.observer.OnCache(layer, ResultMiss)
		} else {
			c.observer.OnCache(layer, ResultError)
		}
		return nil, false
	}
	return raw, true
}

// discard 删除无法解码的脏值，避免持续命中同一坏 key。
func (c *SnapshotCache) discard(ctx context.Context, layer Layer, store pkgcache.Store, key string) {
	c.observer.OnCache(layer, ResultError)
	if err := store.Delete(ctx, key); err != nil {
		c.observer.OnInvalidateFailure(layer)
	}
}

// backfillL1 把 L2 命中的原始值回填到 L1；失败不影响本次请求。
func (c *SnapshotCache) backfillL1(ctx context.Context, key string, raw []byte) {
	if c.l1 == nil {
		return
	}
	if err := c.l1.Set(ctx, key, raw, c.l1TTL); err != nil {
		c.observer.OnInvalidateFailure(LayerL1)
	}
}

// Set 写入 L1 与 L2；空快照使用 negative_ttl 缩短缓存时间。
//
// 返回错误供调用方记录 / 告警，但鉴权路径不得因缓存写失败而拒绝请求。
func (c *SnapshotCache) Set(ctx context.Context, key string, snapshot authz.Snapshot) error {
	if err := c.ready(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("%w: cache key is required", ErrInvalidConfig)
	}
	raw, err := Encode(snapshot)
	if err != nil {
		return err
	}
	l1TTL, l2TTL := c.ttls(snapshot)

	var first error
	if c.l1 != nil {
		if err := c.l1.Set(ctx, key, raw, l1TTL); err != nil {
			c.observer.OnInvalidateFailure(LayerL1)
			first = err
		}
	}
	if c.l2 != nil {
		if err := c.l2.Set(ctx, key, raw, l2TTL); err != nil {
			c.observer.OnInvalidateFailure(LayerL2)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// ttls 返回本次写入使用的 L1 / L2 TTL。
func (c *SnapshotCache) ttls(snapshot authz.Snapshot) (l1TTL, l2TTL time.Duration) {
	if snapshot.Empty() {
		return c.negativeTTL, c.negativeTTL
	}
	return c.l1TTL, c.l2TTL
}

// Delete 删除 L1 与 L2；删除失败必须可观测，且不阻断调用方。
func (c *SnapshotCache) Delete(ctx context.Context, key string) error {
	if err := c.ready(); err != nil {
		return err
	}
	if key == "" {
		return nil
	}
	var first error
	if c.l1 != nil {
		if err := c.l1.Delete(ctx, key); err != nil {
			c.observer.OnInvalidateFailure(LayerL1)
			first = err
		}
	}
	if c.l2 != nil {
		if err := c.l2.Delete(ctx, key); err != nil {
			c.observer.OnInvalidateFailure(LayerL2)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// DeleteSubject 按主体维度删除缓存，供授权变更后主动失效。
func (c *SnapshotCache) DeleteSubject(ctx context.Context, env, channel, merchantID, subjectType, subjectID string) error {
	key, err := SubjectKey(env, channel, merchantID, subjectType, subjectID)
	if err != nil {
		return err
	}
	return c.Delete(ctx, key)
}

func (c *SnapshotCache) ready() error {
	if c == nil {
		return ErrInvalidConfig
	}
	return nil
}
