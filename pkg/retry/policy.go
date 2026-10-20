package retry

import (
	"context"
	"time"

	"github.com/admin-frame/admin-frame/pkg/errors"
)

const (
	defaultMaxAttempts    = 3
	defaultInitialBackoff = 100 * time.Millisecond
	defaultMaxBackoff     = 30 * time.Second
	defaultMultiplier     = 2.0
	defaultJitterFraction = 1.0 // full jitter：在 [0, delay] 均匀取样
)

// Policy 描述重试上限与退避参数。零值经 Normalize 后变为安全默认。
//
// Policy 值可复制；不要在并发中共享可变 OnRetry 闭包状态，除非调用方自行同步。
type Policy struct {
	// MaxAttempts 含首次调用的最大尝试次数；≤0 时默认为 3。
	MaxAttempts int
	// MaxElapsed 自首次尝试起的总时间预算；≤0 表示不按墙钟限制（仍受 ctx 约束）。
	MaxElapsed time.Duration
	// InitialBackoff 首次失败后的基准退避；≤0 时默认 100ms。
	InitialBackoff time.Duration
	// MaxBackoff 单次退避上限；≤0 时默认 30s。
	MaxBackoff time.Duration
	// Multiplier 指数倍数；<1 时默认 2.0。
	Multiplier float64
	// JitterFraction 抖动比例 [0,1]：1=full jitter（推荐），0=无抖动。
	// 负值在 Normalize 时回落为 1.0；>1 夹紧为 1。
	JitterFraction float64

	// RetryIf 判定 err 是否值得重试；nil 时使用 DefaultRetryIf。
	RetryIf func(error) bool
	// OnRetry 在决定重试、进入休眠前调用（attempt 从 1 起为刚失败的那次）。
	// 仅用于日志/指标；不得长时间阻塞。panic 会向上传播。
	OnRetry func(attempt int, err error, delay time.Duration)

	sleep func(ctx context.Context, d time.Duration) error // 测试可注入
}

// DefaultPolicy 返回符合 REL-03 基线的策略副本（3 次、指数 + full jitter）。
func DefaultPolicy() Policy {
	return Policy{}.Normalize()
}

// Normalize 返回补全默认值并夹紧非法字段后的副本。
func (p Policy) Normalize() Policy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = defaultMaxAttempts
	}
	if p.InitialBackoff <= 0 {
		p.InitialBackoff = defaultInitialBackoff
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = defaultMaxBackoff
	}
	if p.MaxBackoff < p.InitialBackoff {
		p.MaxBackoff = p.InitialBackoff
	}
	if p.Multiplier < 1 {
		p.Multiplier = defaultMultiplier
	}
	if p.JitterFraction < 0 {
		p.JitterFraction = defaultJitterFraction
	}
	if p.JitterFraction > 1 {
		p.JitterFraction = 1
	}
	if p.RetryIf == nil {
		p.RetryIf = DefaultRetryIf
	}
	return p
}

// DefaultRetryIf 使用 pkg/errors.RetryableOf；未知错误默认不重试（失败默认安全）。
func DefaultRetryIf(err error) bool {
	return errors.RetryableOf(err)
}

// Option 微调 Policy。
type Option func(*Policy)

// WithMaxAttempts 设置最大尝试次数（含首次）。
func WithMaxAttempts(n int) Option {
	return func(p *Policy) { p.MaxAttempts = n }
}

// WithMaxElapsed 设置墙钟总预算。
func WithMaxElapsed(d time.Duration) Option {
	return func(p *Policy) { p.MaxElapsed = d }
}

// WithBackoff 设置初始与最大退避。
func WithBackoff(initial, maxBackoff time.Duration) Option {
	return func(p *Policy) {
		p.InitialBackoff = initial
		p.MaxBackoff = maxBackoff
	}
}

// WithMultiplier 设置指数倍数（须 ≥1，否则 Normalize 回落默认）。
func WithMultiplier(m float64) Option {
	return func(p *Policy) { p.Multiplier = m }
}

// WithJitterFraction 设置抖动比例；0 关闭抖动，1 为 full jitter。
func WithJitterFraction(f float64) Option {
	return func(p *Policy) { p.JitterFraction = f }
}

// WithRetryIf 覆盖默认可重试判定。
func WithRetryIf(fn func(error) bool) Option {
	return func(p *Policy) {
		if fn != nil {
			p.RetryIf = fn
		}
	}
}

// WithOnRetry 设置重试前回调。
func WithOnRetry(fn func(attempt int, err error, delay time.Duration)) Option {
	return func(p *Policy) { p.OnRetry = fn }
}

// NewPolicy 基于默认策略应用 Option。
func NewPolicy(opts ...Option) Policy {
	p := Policy{}
	for _, opt := range opts {
		if opt != nil {
			opt(&p)
		}
	}
	return p.Normalize()
}
