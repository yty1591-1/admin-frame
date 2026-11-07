package dependency

import (
	"context"
	"errors"
	"time"

	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// caller 是登记产物的内部实现。
type caller struct {
	spec    Spec
	breaker *circuitbreaker.Breaker
	obs     Observer
}

// Do 实现 Caller：超时 → 熔断 → 重试。
//
// 语义细节：
//   - 单次尝试 = 超时 context 包 breaker.Do 包 fn（快速失败双保险）；
//   - fn 返回 retry.Permanent 包装错误时终止重试（非幂等 / 永久错误）；
//   - 熔断打开的快速失败计 breaker_open（区别于普通 error）。
func (c *caller) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if fn == nil {
		return errors.New("dependency: nil fn")
	}
	policy := c.spec.Retry
	if policy.MaxAttempts <= 1 {
		return c.attempt(ctx, fn)
	}
	return retry.Do(ctx, policy, func(attemptCtx context.Context) error {
		err := c.attempt(attemptCtx, fn)
		if err != nil {
			c.obs.RetryObserved(c.spec.Name)
		}
		return err
	})
}

// attempt 单次尝试：超时包熔断包 fn。
func (c *caller) attempt(ctx context.Context, fn func(ctx context.Context) error) error {
	timeout := orDefault(c.spec.Timeout, 5*time.Second)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	exec := func() error { return fn(callCtx) }
	if c.breaker != nil {
		inner := exec // 捕获旧值：闭包引用变量会自递归（Do 包 Do）。
		exec = func() error { return c.breaker.Do(inner) }
	}
	err := exec()
	switch {
	case err == nil:
		c.obs.CallObserved(c.spec.Name, "ok")
	case isBreakerOpen(err):
		c.obs.CallObserved(c.spec.Name, "breaker_open")
	default:
		c.obs.CallObserved(c.spec.Name, "error")
	}
	return err
}

// isBreakerOpen 判定熔断打开错误（pkg/circuitbreaker 哨兵）。
func isBreakerOpen(err error) bool {
	return errors.Is(err, circuitbreaker.ErrOpen)
}
