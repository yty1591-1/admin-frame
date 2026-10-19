package cache

import (
	"context"
	"time"
)

// SetNegative 向支持 NegativeSetter 的层写入空值占位（通常至少 L1 Memory）。
func (m *Multi) SetNegative(ctx context.Context, key string, ttl time.Duration) error {
	if m == nil || len(m.layers) == 0 {
		return ErrInvalidConfig
	}
	if ttl <= 0 {
		return ErrInvalidConfig
	}
	var first error
	for i, layer := range m.layers {
		if err := m.setNegativeOnLayer(ctx, layer, i, key, ttl); err != nil && first == nil {
			first = err
		}
	}
	return first // 无层支持 NegativeSetter 时为 nil，由调用方按 ErrMiss 处理
}

// setNegativeOnLayer 尝试在单层写入空值占位；不支持该接口时返回 nil。
func (m *Multi) setNegativeOnLayer(ctx context.Context, layer Store, i int, key string, ttl time.Duration) error {
	ns, supported := layer.(NegativeSetter)
	if !supported {
		return nil
	}
	if err := ns.SetNegative(ctx, key, m.layerTTL(i, ttl)); err != nil {
		if m.ignoreLayerErr(i) {
			return nil
		}
		return err
	}
	return nil
}

var _ NegativeSetter = (*Multi)(nil)
