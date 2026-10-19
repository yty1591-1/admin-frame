package ratelimit

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// TokenBucket 是经典令牌桶限流器（并发安全）。
//
// Rate 为每秒补充的令牌数；Burst 为桶容量（突发上限）。
// 适合平滑速率控制；跨进程一致性不在本类型职责内。
type TokenBucket struct {
	mu sync.Mutex

	rate   float64 // tokens / second
	burst  float64
	tokens float64
	last   time.Time
	clock  Clock

	sleep func(context.Context, time.Duration) error // 测试可注入
}

// TokenBucketOption 配置令牌桶。
type TokenBucketOption func(*TokenBucket)

// WithClock 注入时钟。
func WithClock(c Clock) TokenBucketOption {
	return func(b *TokenBucket) {
		if c != nil {
			b.clock = c
		}
	}
}

// NewTokenBucket 创建令牌桶。
// rate 为每秒令牌数（须 >0）；burst 为容量（须 ≥1）。
// 初始令牌数为满桶（允许启动突发）。
func NewTokenBucket(rate float64, burst int, opts ...TokenBucketOption) (*TokenBucket, error) {
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return nil, fmt.Errorf("ratelimit: rate must be > 0")
	}
	if burst < 1 {
		return nil, fmt.Errorf("ratelimit: burst must be >= 1")
	}
	b := &TokenBucket{
		rate:   rate,
		burst:  float64(burst),
		tokens: float64(burst),
		clock:  RealClock(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	b.last = b.clock.Now()
	return b, nil
}

// MustTokenBucket 同 NewTokenBucket，参数非法时 panic。
func MustTokenBucket(rate float64, burst int, opts ...TokenBucketOption) *TokenBucket {
	b, err := NewTokenBucket(rate, burst, opts...)
	if err != nil {
		panic(err)
	}
	return b
}

// Allow 尝试消耗 1 个令牌。
func (b *TokenBucket) Allow() Result { return b.AllowN(1) }

// AllowN 尝试消耗 n 个令牌。
func (b *TokenBucket) AllowN(n int) Result {
	if n <= 0 {
		return Result{Allowed: true, Remaining: b.snapshotRemaining()}
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock.Now()
	b.refillLocked(now)

	need := float64(n)
	if b.tokens >= need {
		b.tokens -= need
		return Result{Allowed: true, Remaining: b.tokens}
	}

	missing := need - b.tokens
	retryAfter := time.Duration(missing / b.rate * float64(time.Second))
	if retryAfter < time.Nanosecond {
		retryAfter = time.Nanosecond
	}
	return Result{Allowed: false, Remaining: b.tokens, RetryAfter: retryAfter}
}

// Wait 阻塞直到获得 1 个令牌或 ctx 结束。
func (b *TokenBucket) Wait(ctx context.Context) error {
	return b.WaitN(ctx, 1)
}

// WaitN 阻塞直到获得 n 个令牌或 ctx 结束。
func (b *TokenBucket) WaitN(ctx context.Context, n int) error {
	return waitLoop(ctx, b, n, b.sleep)
}

// Rate 返回每秒令牌补充速率。
func (b *TokenBucket) Rate() float64 { return b.rate }

// Burst 返回桶容量。
func (b *TokenBucket) Burst() int { return int(b.burst) }

func (b *TokenBucket) refillLocked(now time.Time) {
	if now.Before(b.last) {
		b.last = now
		return
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
}

func (b *TokenBucket) snapshotRemaining() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked(b.clock.Now())
	return b.tokens
}
