package webhook

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

const (
	defaultNonceCapacity = 100_000
	maxNonceCapacity     = 1_000_000
	defaultNonceShards   = 16
	maxNonceShards       = 256
	maxNonceBytes        = 512
)

// NonceStore 记录已消费的消息 ID，用于拒绝重放。
//
// Claim 必须原子且并发安全：同一 nonce 在 ttl 内首次调用返回 true，
// 后续调用返回 false。返回错误表示无法判定，调用方必须 fail closed
// （拒绝请求），不得放行。
//
// 多实例部署必须使用共享实现（如 Redis SET NX EX、数据库唯一键）；
// MemoryNonceStore 只覆盖单进程。同一实例应仅服务一个发送方（一套 Keys）；
// 若多个发送方共享存储，实现方需在 nonce 中追加来源命名空间，避免 ID 碰撞。
type NonceStore interface {
	Claim(ctx context.Context, nonce string, ttl time.Duration) (first bool, err error)
}

// MemoryNonceStoreConfig 配置 MemoryNonceStore；零值可用。
type MemoryNonceStoreConfig struct {
	// MaxEntries 是总容量；≤0 取 100000，上限 1000000。
	//
	// 容量应覆盖「峰值 QPS × 2 × Tolerance」并留出余量；写满后
	// Claim 返回 ErrNonceStoreFull，Verifier 将拒绝请求。
	MaxEntries int
	// Shards 是分片数；≤0 取 16，向上取整到 2 的幂，上限 256。
	Shards int
	// Clock 为注入时钟；nil 时使用系统时钟。
	Clock clock.Clock
}

// MemoryNonceStore 是单进程内的分片 TTL 去重存储。
//
// 零值不可用，必须通过 NewMemoryNonceStore 构造。并发安全；
// 过期记录在下次访问同分片或容量不足时惰性清理，无后台 goroutine。
type MemoryNonceStore struct {
	clock      clock.Clock
	shards     []nonceShard
	shardMask  uint32
	maxEntries int
	perShard   int
}

type nonceShard struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// NewMemoryNonceStore 构造内存重放存储，按配置补默认值并分片。
func NewMemoryNonceStore(cfg MemoryNonceStoreConfig) *MemoryNonceStore {
	shards := normalizeShardCount(cfg.Shards)
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultNonceCapacity
	}
	if maxEntries > maxNonceCapacity {
		maxEntries = maxNonceCapacity
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.RealClock()
	}
	store := &MemoryNonceStore{
		clock:      clk,
		shards:     make([]nonceShard, shards),
		shardMask:  uint32(shards - 1), //nolint:gosec // shards 已限制在 [1,256]，不会溢出。
		maxEntries: maxEntries,
		perShard:   (maxEntries + shards - 1) / shards,
	}
	for i := range store.shards {
		store.shards[i].seen = make(map[string]time.Time)
	}
	return store
}

// Claim 原子占用 nonce；已存在且未过期时返回 false。
//
// 同一分片写满且清理过期记录后仍无空间时返回 ErrNonceStoreFull。
func (s *MemoryNonceStore) Claim(ctx context.Context, nonce string, ttl time.Duration) (first bool, err error) {
	if s == nil || len(s.shards) == 0 {
		return false, pkgerrors.InvalidArgument("nonce store is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if nonce == "" {
		return false, pkgerrors.InvalidArgument("nonce must not be empty")
	}
	if len(nonce) > maxNonceBytes {
		return false, pkgerrors.InvalidArgument(fmt.Sprintf("nonce exceeds %d bytes", maxNonceBytes))
	}
	if ttl <= 0 {
		return false, pkgerrors.InvalidArgument("nonce ttl must be positive")
	}

	shard := &s.shards[shardIndex(nonce, s.shardMask)]
	now := s.clock.Now()
	shard.mu.Lock()
	defer shard.mu.Unlock()

	if expires, ok := shard.seen[nonce]; ok {
		if expires.After(now) {
			return false, nil
		}
		delete(shard.seen, nonce)
	}
	if len(shard.seen) >= s.perShard {
		sweepExpired(shard, now)
		if len(shard.seen) >= s.perShard {
			return false, fmt.Errorf("%w: %w", ErrNonceStoreFull,
				pkgerrors.ResourceExhausted(fmt.Sprintf("nonce store capacity %d exceeded", s.maxEntries)))
		}
	}
	shard.seen[nonce] = now.Add(ttl)
	return true, nil
}

// Len 返回当前记录数（含尚未清理的过期项）；用于观测与测试。
//
// 结果跨分片非原子，只保证逐分片一致。
func (s *MemoryNonceStore) Len() int {
	if s == nil {
		return 0
	}
	total := 0
	for i := range s.shards {
		shard := &s.shards[i]
		shard.mu.Lock()
		total += len(shard.seen)
		shard.mu.Unlock()
	}
	return total
}

// Reset 清空全部记录；用于测试或受控运维操作，会重置重放窗口。
func (s *MemoryNonceStore) Reset() {
	if s == nil {
		return
	}
	for i := range s.shards {
		shard := &s.shards[i]
		shard.mu.Lock()
		shard.seen = make(map[string]time.Time, s.perShard)
		shard.mu.Unlock()
	}
}

// sweepExpired 删除分片内已过期记录；调用方必须持有 shard.mu。
func sweepExpired(shard *nonceShard, now time.Time) {
	for nonce, expires := range shard.seen {
		if !expires.After(now) {
			delete(shard.seen, nonce)
		}
	}
}

// shardIndex 用 FNV-1a 选择分片，避免额外分配。
func shardIndex(nonce string, mask uint32) uint32 {
	var hash uint32 = 2166136261
	for i := 0; i < len(nonce); i++ {
		hash ^= uint32(nonce[i])
		hash *= 16777619
	}
	return hash & mask
}

func normalizeShardCount(n int) int {
	if n <= 0 {
		return defaultNonceShards
	}
	if n > maxNonceShards {
		n = maxNonceShards
	}
	shards := 1
	for shards < n {
		shards <<= 1
	}
	return shards
}
