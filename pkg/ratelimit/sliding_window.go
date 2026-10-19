package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SlidingWindow 使用「当前窗 + 上一窗加权」的滑动窗口计数器（并发安全、内存固定）。
//
// 在窗口边界处用上一窗计数按重叠比例加权，近似真滑动日志，避免存每次请求时间戳。
// Limit 为每个 Window 内允许的最大次数。
type SlidingWindow struct {
	mu sync.Mutex

	limit  int
	window time.Duration
	clock  Clock

	windowStart time.Time
	prevCount   int
	currCount   int

	sleep func(context.Context, time.Duration) error
}

// SlidingWindowOption 配置滑动窗口。
type SlidingWindowOption func(*SlidingWindow)

// WithSlidingClock 注入时钟。
func WithSlidingClock(c Clock) SlidingWindowOption {
	return func(w *SlidingWindow) {
		if c != nil {
			w.clock = c
		}
	}
}

// NewSlidingWindow 创建滑动窗口限流器。
// limit 为每 window 允许次数（须 ≥1）；window 须 >0。
func NewSlidingWindow(limit int, window time.Duration, opts ...SlidingWindowOption) (*SlidingWindow, error) {
	if limit < 1 {
		return nil, fmt.Errorf("ratelimit: limit must be >= 1")
	}
	if window <= 0 {
		return nil, fmt.Errorf("ratelimit: window must be > 0")
	}
	w := &SlidingWindow{
		limit:  limit,
		window: window,
		clock:  RealClock(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	w.windowStart = w.clock.Now()
	return w, nil
}

// MustSlidingWindow 同 NewSlidingWindow，参数非法时 panic。
func MustSlidingWindow(limit int, window time.Duration, opts ...SlidingWindowOption) *SlidingWindow {
	w, err := NewSlidingWindow(limit, window, opts...)
	if err != nil {
		panic(err)
	}
	return w
}

// Allow 尝试消耗 1 次配额。
func (w *SlidingWindow) Allow() Result { return w.AllowN(1) }

// AllowN 尝试消耗 n 次配额。
func (w *SlidingWindow) AllowN(n int) Result {
	if n <= 0 {
		return Result{Allowed: true, Remaining: float64(w.snapshotRemaining())}
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.clock.Now()
	w.rotateLocked(now)

	weight := w.weightedCountLocked(now)
	if weight+float64(n) <= float64(w.limit) {
		w.currCount += n
		remain := float64(w.limit) - w.weightedCountLocked(now)
		if remain < 0 {
			remain = 0
		}
		return Result{Allowed: true, Remaining: remain}
	}

	// 估算何时加权计数会降到 limit-n：简化为等到当前窗结束再重试一部分。
	elapsed := now.Sub(w.windowStart)
	retryAfter := w.window - elapsed
	if retryAfter < time.Nanosecond {
		retryAfter = time.Nanosecond
	}
	remain := float64(w.limit) - weight
	if remain < 0 {
		remain = 0
	}
	return Result{Allowed: false, Remaining: remain, RetryAfter: retryAfter}
}

// Wait 阻塞直到获得 1 次配额或 ctx 结束。
func (w *SlidingWindow) Wait(ctx context.Context) error {
	return w.WaitN(ctx, 1)
}

// WaitN 阻塞直到获得 n 次配额或 ctx 结束。
func (w *SlidingWindow) WaitN(ctx context.Context, n int) error {
	return waitLoop(ctx, w, n, w.sleep)
}

// Limit 返回窗口内最大次数。
func (w *SlidingWindow) Limit() int { return w.limit }

// Window 返回窗口长度。
func (w *SlidingWindow) Window() time.Duration { return w.window }

func (w *SlidingWindow) rotateLocked(now time.Time) {
	if now.Before(w.windowStart) {
		w.windowStart = now
		return
	}
	elapsed := now.Sub(w.windowStart)
	if elapsed < w.window {
		return
	}
	// 跨越多个窗口时中间窗视为空。
	steps := int(elapsed / w.window)
	if steps == 1 {
		w.prevCount = w.currCount
	} else {
		w.prevCount = 0
	}
	w.currCount = 0
	w.windowStart = w.windowStart.Add(time.Duration(steps) * w.window)
	// 若仍落后（时钟大跳），对齐到 now 所在窗起点。
	for now.Sub(w.windowStart) >= w.window {
		w.prevCount = w.currCount
		w.currCount = 0
		w.windowStart = w.windowStart.Add(w.window)
	}
}

func (w *SlidingWindow) weightedCountLocked(now time.Time) float64 {
	elapsed := now.Sub(w.windowStart)
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > w.window {
		elapsed = w.window
	}
	// 上一窗剩余比例 = 1 - elapsed/window
	frac := 1 - float64(elapsed)/float64(w.window)
	if frac < 0 {
		frac = 0
	}
	return float64(w.prevCount)*frac + float64(w.currCount)
}

func (w *SlidingWindow) snapshotRemaining() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.clock.Now()
	w.rotateLocked(now)
	remain := float64(w.limit) - w.weightedCountLocked(now)
	if remain < 0 {
		return 0
	}
	return int(remain)
}
