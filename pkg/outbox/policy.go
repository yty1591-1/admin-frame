package outbox

import "time"

// Backoff 是重试退避算法。
type Backoff string

const (
	// BackoffExponential 指数退避（默认）。
	BackoffExponential Backoff = "exponential"
	// BackoffFixed 固定间隔重试。
	BackoffFixed Backoff = "fixed"
)

// RetryPolicy 是 Outbox 投递的退避策略。
type RetryPolicy struct {
	// Base 首次重试间隔；≤0 时取 1s。
	Base time.Duration
	// Max 单次重试间隔上限；≤0 时取 5m。
	Max time.Duration
	// MaxAttempts 最大投递次数（含首次）；≤0 时取 10。
	MaxAttempts int
	// Backoff exponential | fixed；空值取 exponential。
	Backoff Backoff
}

// DefaultRetryPolicy 返回默认退避策略（1s 起，封顶 5m，最多 10 次，指数退避）。
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		Base:        time.Second,
		Max:         5 * time.Minute,
		MaxAttempts: 10,
		Backoff:     BackoffExponential,
	}
}

// Normalize 补齐非法 / 零值字段，避免漏配置导致无限快速重试。
func (p RetryPolicy) Normalize() RetryPolicy {
	if p.Base <= 0 {
		p.Base = time.Second
	}
	if p.Max <= 0 {
		p.Max = 5 * time.Minute
	}
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 10
	}
	switch p.Backoff {
	case BackoffFixed, BackoffExponential:
	default:
		p.Backoff = BackoffExponential
	}
	return p
}

// Exhausted 报告已用次数是否达到上限。
func (p RetryPolicy) Exhausted(attempts int) bool {
	p = p.Normalize()
	return attempts >= p.MaxAttempts
}

// NextRetry 返回第 attempts 次失败后的退避间隔。
func (p RetryPolicy) NextRetry(attempts int) time.Duration {
	p = p.Normalize()
	if attempts <= 1 {
		return p.Base
	}
	if p.Backoff == BackoffFixed {
		return p.Base
	}
	delay := p.Base
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= p.Max {
			return p.Max
		}
	}
	if delay > p.Max {
		return p.Max
	}
	return delay
}
