package ratelimit

import (
	"fmt"
	"sync"
	"time"
)

// KeyFunc 由调用方根据请求上下文生成限流键（如 "c:"+userID）。
// 空字符串合法，表示全局共享桶；高频唯一键可能导致内存增长，须配合 IdleTTL 回收。
type KeyFunc func() string

// Keyed 按字符串 key 持有独立 Limiter（并发安全）。
//
// 工厂 newLimiter 在首次访问 key 时调用；IdleTTL>0 时后台不自动扫描，
// 调用 Sweep 或在 Allow 路径上惰性淘汰过期项。
type Keyed struct {
	mu      sync.Mutex
	entries map[string]*keyedEntry
	newFn   func() (Limiter, error)
	idleTTL time.Duration
	clock   Clock
}

type keyedEntry struct {
	lim      Limiter
	lastSeen time.Time
}

// KeyedOption 配置 Keyed 注册表。
type KeyedOption func(*Keyed)

// WithKeyedClock 注入时钟（用于 IdleTTL）。
func WithKeyedClock(c Clock) KeyedOption {
	return func(k *Keyed) {
		if c != nil {
			k.clock = c
		}
	}
}

// WithIdleTTL 设置条目空闲回收阈值；≤0 表示不回收。
func WithIdleTTL(d time.Duration) KeyedOption {
	return func(k *Keyed) { k.idleTTL = d }
}

// NewKeyed 创建按 key 分桶的限流注册表。
// newLimiter 不得返回 nil Limiter；每次新 key 调用一次。
func NewKeyed(newLimiter func() (Limiter, error), opts ...KeyedOption) (*Keyed, error) {
	if newLimiter == nil {
		return nil, fmt.Errorf("ratelimit: newLimiter is required")
	}
	k := &Keyed{
		entries: make(map[string]*keyedEntry),
		newFn:   newLimiter,
		clock:   RealClock(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(k)
		}
	}
	return k, nil
}

// MustKeyed 同 NewKeyed，失败时 panic。
func MustKeyed(newLimiter func() (Limiter, error), opts ...KeyedOption) *Keyed {
	k, err := NewKeyed(newLimiter, opts...)
	if err != nil {
		panic(err)
	}
	return k
}

// Allow 对 key 执行 Allow（1 次）。
func (k *Keyed) Allow(key string) Result {
	return k.AllowN(key, 1)
}

// AllowN 对 key 执行 AllowN。
func (k *Keyed) AllowN(key string, n int) Result {
	lim, err := k.Get(key)
	if err != nil {
		// 工厂失败时失败开放会导致击穿；生产应保证工厂稳定。此处拒绝并 RetryAfter=0。
		return Result{Allowed: false, RetryAfter: time.Second}
	}
	return lim.AllowN(n)
}

// Get 返回 key 对应的 Limiter（不存在则创建）。
func (k *Keyed) Get(key string) (Limiter, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	now := k.clock.Now()
	k.sweepLocked(now)

	if e, ok := k.entries[key]; ok {
		e.lastSeen = now
		return e.lim, nil
	}
	lim, err := k.newFn()
	if err != nil {
		return nil, err
	}
	if lim == nil {
		return nil, fmt.Errorf("ratelimit: newLimiter returned nil")
	}
	k.entries[key] = &keyedEntry{lim: lim, lastSeen: now}
	return lim, nil
}

// Len 返回当前缓存的 key 数量。
func (k *Keyed) Len() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.entries)
}

// Sweep 立刻按 IdleTTL 淘汰过期条目。
func (k *Keyed) Sweep() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sweepLocked(k.clock.Now())
}

func (k *Keyed) sweepLocked(now time.Time) {
	if k.idleTTL <= 0 {
		return
	}
	for key, e := range k.entries {
		if now.Sub(e.lastSeen) >= k.idleTTL {
			delete(k.entries, key)
		}
	}
}
