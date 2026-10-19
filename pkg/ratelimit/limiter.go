package ratelimit

import (
	"context"
	"time"
)

// Limiter 是限流器接口；实现须并发安全。
type Limiter interface {
	// Allow 尝试消耗 1 个配额。
	Allow() Result
	// AllowN 尝试消耗 n 个配额；n≤0 时视为放行且不扣减。
	AllowN(n int) Result
}

// Waiter 可选：阻塞直到获得配额或 ctx 结束。
type Waiter interface {
	Limiter
	// Wait 阻塞直到允许 1 次或 ctx 取消/超时。
	Wait(ctx context.Context) error
	// WaitN 阻塞直到允许 n 次或 ctx 取消/超时；n≤0 立即返回 nil。
	WaitN(ctx context.Context, n int) error
}

// waitLoop 通用等待实现：轮询 AllowN，按 RetryAfter 休眠并尊重 ctx。
func waitLoop(ctx context.Context, lim Limiter, n int, sleep func(context.Context, time.Duration) error) error {
	if n <= 0 {
		return nil
	}
	if sleep == nil {
		sleep = sleepContext
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r := lim.AllowN(n)
		if r.Allowed {
			return nil
		}
		delay := r.RetryAfter
		if delay <= 0 {
			delay = time.Millisecond
		}
		if err := sleep(ctx, delay); err != nil {
			return err
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
