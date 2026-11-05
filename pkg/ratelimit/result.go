package ratelimit

import (
	stderrors "errors"
	"time"
)

// ErrLimited 表示请求被限流拒绝的哨兵，可用 errors.Is 匹配。
// platform/transport 应将其映射为业务码 42900（REL-05）。
var ErrLimited = stderrors.New("rate limit exceeded")

// Result 是一次 Allow / AllowN 的判定结果。
type Result struct {
	// Allowed 为 true 表示已扣除配额并放行。
	Allowed bool
	// Remaining 为判定后剩余可用量的近似值（令牌桶为 token 数；滑动窗口为剩余次数）。
	Remaining float64
	// RetryAfter 为建议等待时长（拒绝时 >0；放行时通常为 0）。
	// 可映射为 HTTP Retry-After（秒，向上取整由调用方负责）。
	RetryAfter time.Duration
	// Degraded 为 true 表示本次判定来自降级路径（共享存储不可用），
	// 放行但不具备跨副本精确配额；调用方应记录告警指标。
	Degraded bool
}

// Err 在 Allowed 时返回 nil，否则返回 ErrLimited。
func (r Result) Err() error {
	if r.Allowed {
		return nil
	}
	return ErrLimited
}
