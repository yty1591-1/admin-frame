package metrics

import (
	"context"
	"errors"
	"sync"
	"time"
)

// 快照缓存的不可恢复错误：缓存未初始化或缺少采集函数。
var (
	errSnapshotCacheNil      = errors.New("metrics: snapshot cache is nil")
	errSnapshotCacheNoLoader = errors.New("metrics: snapshot cache loader is required")
)

// SnapshotCache 缓存「需要访问外部依赖」的采集结果。
//
// 背景（ENT-06 T6 实测）：GaugeFunc 在 Prometheus 抓取时同步执行，若采集函数要访问
// 数据库，一个慢查询就会把整个 /metrics 响应拖住；抓取超时后该进程的**全部**指标
// 一起丢失，连带「依赖已耗尽」这类告警永远无法触发（DB 池打满 → 采集阻塞 →
// 抓取超时 → db_pool_* 指标消失 → DBPoolExhaustion 不响）。
//
// 语义：
//   - TTL 内的多次采集复用同一份快照，依赖变慢时每 TTL 只付一次超时成本；
//   - 采集失败时保留上一份成功快照（降级为旧值而非 0），避免把「采不到」误报成「没有积压」；
//   - 失败同样按 TTL 节流，依赖持续不可用时不会每个 Gauge 都去撞一次超时。
type SnapshotCache[T any] struct {
	mu   sync.Mutex
	ttl  time.Duration
	load func(context.Context) (T, error)

	value   T
	at      time.Time
	has     bool
	lastErr error
}

// NewSnapshotCache 创建快照缓存。
//
// ttl ≤ 0 时每次采集都会重新拉取（等价于无缓存）；load 为 nil 时 Get 返回错误。
func NewSnapshotCache[T any](ttl time.Duration, load func(context.Context) (T, error)) *SnapshotCache[T] {
	return &SnapshotCache[T]{ttl: ttl, load: load}
}

// Get 返回当前快照。
//
// 返回值依次为：快照值、是否来自成功的新鲜采集、采集错误。
//   - 命中 TTL 且上次成功：返回缓存值，fresh=true，err=nil。
//   - 命中 TTL 但上次失败：返回上一次成功快照，fresh=false，err 非空（不再重复采集）。
//   - 超过 TTL：调用 load；成功则刷新缓存并返回（fresh=true）；失败则返回上一次
//     成功快照（fresh=false，err 非空）；从未成功过则返回零值。
//
// 并发安全：同一时刻只有一个 goroutine 执行 load，其余调用方等待后复用其结果。
func (c *SnapshotCache[T]) Get(ctx context.Context) (T, bool, error) {
	if c == nil {
		var zero T
		return zero, false, errSnapshotCacheNil
	}
	if c.load == nil {
		var zero T
		return zero, false, errSnapshotCacheNoLoader
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// TTL 内直接复用上次采集结果（成功或失败都算）：失败也按 TTL 节流，
	// 避免依赖不可用时每个 Gauge 都去撞一次超时。
	if c.ttl > 0 && !c.at.IsZero() && time.Since(c.at) < c.ttl {
		return c.value, c.lastErr == nil, c.lastErr
	}
	value, err := c.load(ctx)
	c.at = time.Now()
	if err != nil {
		c.lastErr = err
		return c.value, false, err
	}
	c.value = value
	c.has = true
	c.lastErr = nil
	return value, true, nil
}
