package cache

import (
	"context"
	"fmt"
	"time"
)

// MultiConfig 多级缓存配置。
type MultiConfig struct {
	// Layers 从快到慢：layers[0] 为 L1（通常本地），其后为 L2/L3（如 Redis）。
	Layers []Store
	// L1TTL 从更深层回填 L1 时使用的 TTL；≤0 表示与写入时传入的 ttl 相同。
	L1TTL time.Duration
	// SoftFail 为 true 时，深层 Get/Set/Delete 错误被忽略（降级），仅本地失败才返回错误。
	SoftFail bool
}

// Multi 组合多层 Store：读 L1→Ln，写穿透所有层，删同步所有层。
type Multi struct {
	layers   []Store
	l1TTL    time.Duration
	softFail bool
}

// NewMulti 创建多级缓存；至少一层。
func NewMulti(cfg MultiConfig) (*Multi, error) {
	if len(cfg.Layers) == 0 {
		return nil, fmt.Errorf("%w: at least one layer required", ErrInvalidConfig)
	}
	for i, s := range cfg.Layers {
		if s == nil {
			return nil, fmt.Errorf("%w: layer %d is nil", ErrInvalidConfig, i)
		}
	}
	return &Multi{
		layers:   append([]Store(nil), cfg.Layers...),
		l1TTL:    cfg.L1TTL,
		softFail: cfg.SoftFail,
	}, nil
}

// Get 自 L1 起查找；命中更深层时回填更浅层。
func (m *Multi) Get(ctx context.Context, key string) ([]byte, error) {
	if m == nil || len(m.layers) == 0 {
		return nil, ErrInvalidConfig
	}
	var lastErr error
	for i, layer := range m.layers {
		val, err := layer.Get(ctx, key)
		if handled, out, outErr := m.handleLayerGet(ctx, key, i, val, err); handled {
			return out, outErr
		}
		if err != nil && err != ErrMiss {
			lastErr = err
		}
	}
	if lastErr != nil && m.softFail {
		return nil, ErrMiss
	}
	return nil, ErrMiss
}

// handleLayerGet 处理单层结果。
// handled=true 表示应立即返回（命中、空值命中、或不可降级错误）。
// handled=false 表示继续下一层（miss 或已降级的深层错误）。
func (m *Multi) handleLayerGet(ctx context.Context, key string, i int, val []byte, err error) (handled bool, out []byte, outErr error) {
	if err == nil {
		m.backfill(ctx, key, val, i)
		return true, val, nil
	}
	if err == ErrNegative {
		return true, nil, ErrNegative
	}
	if err == ErrMiss {
		return false, nil, nil
	}
	if m.softFail && i > 0 {
		return false, nil, nil
	}
	return true, nil, err
}

// backfill 将命中值写入 layers[0:upTo)（更浅层）。
func (m *Multi) backfill(ctx context.Context, key string, val []byte, upTo int) {
	if upTo <= 0 {
		return
	}
	ttl := m.l1TTL
	if ttl <= 0 {
		ttl = time.Minute
	}
	for j := 0; j < upTo; j++ {
		_ = m.layers[j].Set(ctx, key, val, ttl)
	}
}

// layerTTL 返回写入第 i 层使用的 TTL（L1 可更短）。
func (m *Multi) layerTTL(i int, ttl time.Duration) time.Duration {
	if i == 0 && m.l1TTL > 0 && m.l1TTL < ttl {
		return m.l1TTL
	}
	return ttl
}

// Set 写入所有层；浅层失败立即返回，深层在 SoftFail 下可忽略。
func (m *Multi) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if m == nil || len(m.layers) == 0 {
		return ErrInvalidConfig
	}
	if ttl <= 0 {
		return fmt.Errorf("%w: ttl must be > 0", ErrInvalidConfig)
	}
	for i, layer := range m.layers {
		if err := layer.Set(ctx, key, value, m.layerTTL(i, ttl)); err != nil {
			if m.ignoreLayerErr(i) {
				continue
			}
			return err
		}
	}
	return nil
}

// Delete 从所有层删除。
func (m *Multi) Delete(ctx context.Context, keys ...string) error {
	if m == nil || len(m.layers) == 0 {
		return ErrInvalidConfig
	}
	var first error
	for i, layer := range m.layers {
		if err := layer.Delete(ctx, keys...); err != nil {
			if m.ignoreLayerErr(i) {
				continue
			}
			if first == nil {
				first = err
			}
		}
	}
	return first
}

func (m *Multi) ignoreLayerErr(i int) bool {
	return m.softFail && i > 0
}

var _ Store = (*Multi)(nil)
