package ratelimit

import (
	"fmt"
	"sync"
	"time"
)

// Params 是一次限流参数快照，对应配置键 `rate_limit.*` 中可热更的部分。
type Params struct {
	// RPS 每秒补充令牌数；须 > 0。
	RPS float64
	// Burst 桶容量；须 ≥ 1。
	Burst int
	// IdleTTL 空闲桶回收阈值；≤ 0 表示沿用注册表的默认值。
	IdleTTL time.Duration
	// DegradeOnFailure 连续故障后是否进入宽松降级窗口。
	DegradeOnFailure bool
}

// ParamsProvider 返回当前生效的限流参数与版本号。
//
// 版本号必须单调递增：相同版本表示参数未变化，调用方跳过重配置。
// 装配期或运行期取不到参数时返回 ok=false，调用方回落到上一份已知参数。
type ParamsProvider interface {
	Params() (params Params, version int64, ok bool)
}

// ParamsFunc 把函数适配为 ParamsProvider。
type ParamsFunc func() (Params, int64, bool)

// Params 实现 ParamsProvider。
func (f ParamsFunc) Params() (Params, int64, bool) { return f() }

// AdaptiveOption 配置 Adaptive。
type AdaptiveOption func(*Adaptive)

// WithAdaptiveClock 注入时钟（用于 IdleTTL 与令牌补充）。
func WithAdaptiveClock(c Clock) AdaptiveOption {
	return func(a *Adaptive) {
		if c != nil {
			a.clock = c
		}
	}
}

// WithAdaptiveIdleTTL 设置默认空闲回收阈值；参数快照带 IdleTTL 时以快照为准。
func WithAdaptiveIdleTTL(d time.Duration) AdaptiveOption {
	return func(a *Adaptive) { a.defaultIdleTTL = d }
}

// Adaptive 是按 key 分桶、参数可在运行期变更的令牌桶注册表。
//
// 与 Keyed 的差别只在参数来源：Keyed 的速率在构造期固定，Adaptive 每次请求
// 读取当前参数，参数版本变化时对**已存在的桶**调用 Configure。
// 因此热更不会清空配额，也不会丢弃已建立的桶状态（ADR-0015 决策 5）。
type Adaptive struct {
	mu             sync.Mutex
	entries        map[string]*adaptiveEntry
	provider       ParamsProvider
	defaultIdleTTL time.Duration
	clock          Clock

	// last 是最近一次成功读取的参数：provider 短暂不可用时沿用，
	// 避免一次读取失败把所有请求打成 429。
	last        Params
	lastVersion int64
	haveLast    bool
}

type adaptiveEntry struct {
	bucket   *TokenBucket
	version  int64
	lastSeen time.Time
}

// NewAdaptive 构造可热更的按 key 限流注册表；provider 为 nil 时返回错误。
func NewAdaptive(provider ParamsProvider, opts ...AdaptiveOption) (*Adaptive, error) {
	if provider == nil {
		return nil, fmt.Errorf("ratelimit: adaptive requires a params provider")
	}
	registry := &Adaptive{
		entries:  make(map[string]*adaptiveEntry),
		provider: provider,
		clock:    RealClock(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(registry)
		}
	}
	return registry, nil
}

// Allow 对 key 执行 AllowN(1)。
func (a *Adaptive) Allow(key string) Result { return a.AllowN(key, 1) }

// AllowN 对 key 执行 AllowN。
//
// 参数不可用（首次读取就失败）时拒绝并给出 1s 重试：限流器参数异常时放行
// 等于放弃保护，与 Keyed 的既有语义一致。
func (a *Adaptive) AllowN(key string, n int) Result {
	params, version, ok := a.provider.Params()
	if !ok || params.RPS <= 0 || params.Burst < 1 {
		params, version, ok = a.fallback()
		if !ok {
			return Result{Allowed: false, RetryAfter: time.Second}
		}
	}

	a.remember(params, version)

	a.mu.Lock()
	now := a.clock.Now()
	a.sweepLocked(now, params.IdleTTL)
	entry, exists := a.entries[key]
	if !exists {
		bucket, err := NewTokenBucket(params.RPS, params.Burst, WithClock(a.clock))
		if err != nil {
			a.mu.Unlock()
			return Result{Allowed: false, RetryAfter: time.Second}
		}
		entry = &adaptiveEntry{bucket: bucket, version: version}
		a.entries[key] = entry
	} else if entry.version != version {
		// 参数变更只调整既有桶，不重建：已消耗的配额不会被退还。
		if err := entry.bucket.Configure(params.RPS, params.Burst); err != nil {
			a.mu.Unlock()
			return Result{Allowed: false, RetryAfter: time.Second}
		}
		entry.version = version
	}
	entry.lastSeen = now
	bucket := entry.bucket
	a.mu.Unlock()

	// 桶自身的锁足以保证并发安全；这里不持注册表锁，避免全局限流串行化。
	return bucket.AllowN(n)
}

// Len 返回当前缓存的 key 数量。
func (a *Adaptive) Len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.entries)
}

// Sweep 立刻按当前空闲阈值淘汰过期条目。
func (a *Adaptive) Sweep() {
	ttl := a.defaultIdleTTL
	if params, _, ok := a.provider.Params(); ok && params.IdleTTL > 0 {
		ttl = params.IdleTTL
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked(a.clock.Now(), ttl)
}

// fallback 返回上一份已知参数；从未成功读取过时 ok=false。
func (a *Adaptive) fallback() (Params, int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last, a.lastVersion, a.haveLast
}

func (a *Adaptive) remember(params Params, version int64) {
	a.last, a.lastVersion, a.haveLast = params, version, true
}

func (a *Adaptive) sweepLocked(now time.Time, ttl time.Duration) {
	if ttl <= 0 {
		ttl = a.defaultIdleTTL
	}
	if ttl <= 0 {
		return
	}
	for key, entry := range a.entries {
		if now.Sub(entry.lastSeen) >= ttl {
			delete(a.entries, key)
		}
	}
}
