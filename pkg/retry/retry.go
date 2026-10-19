package retry

import (
	"context"
	stderrors "errors"
	"time"
)

// Do 按 Policy 执行 fn，直到成功、不可重试、ctx 取消或达到上限。
//
// 行为约定：
//   - fn 返回 nil 立即成功返回 nil
//   - ctx 在尝试前或退避中取消时返回 ctx.Err()
//   - Permanent(err) 或 RetryIf 为 false 时返回解开后的 err
//   - 次数或 MaxElapsed 耗尽时返回 *ExhaustedError
func Do(ctx context.Context, policy Policy, fn func(context.Context) error) error {
	_, err := DoValue(ctx, policy, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

// DoValue 与 Do 相同，但保留成功时的返回值。
func DoValue[T any](ctx context.Context, policy Policy, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	if fn == nil {
		return zero, errNilFunc
	}
	p := policy.Normalize()
	sleepFn := p.sleep
	if sleepFn == nil {
		sleepFn = sleepContext
	}

	start := time.Now()
	var last error
	attempts := 0

	for attempt := 1; attempt <= p.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if p.MaxElapsed > 0 && time.Since(start) >= p.MaxElapsed {
			if attempts == 0 {
				return zero, context.DeadlineExceeded
			}
			return zero, &ExhaustedError{Attempts: attempts, Last: last}
		}

		v, err := fn(ctx)
		attempts = attempt
		if err == nil {
			return v, nil
		}
		last = unwrapPermanent(err)

		if IsPermanent(err) || !p.RetryIf(last) {
			return zero, last
		}
		if attempt == p.MaxAttempts {
			break
		}

		delay := p.backoffDelay(attempt)
		if p.MaxElapsed > 0 {
			remain := p.MaxElapsed - time.Since(start)
			if remain <= 0 {
				break
			}
			if delay > remain {
				delay = remain
			}
		}

		if p.OnRetry != nil {
			p.OnRetry(attempt, last, delay)
		}

		if delay > 0 {
			if err := sleepFn(ctx, delay); err != nil {
				return zero, err
			}
		}
	}

	return zero, &ExhaustedError{Attempts: attempts, Last: last}
}

func unwrapPermanent(err error) error {
	var pe *permanentError
	if stderrors.As(err, &pe) {
		return pe.err
	}
	return err
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
