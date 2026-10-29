package app

import (
	"math"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// RateLimitedError 是携带建议重试时长的 42900 错误。
//
// transport 通过 RetryAfterCoder 接口写入 Retry-After 响应头；
// 其余行为与 *apperr.AppError 一致（错误码、errors.Is 判定不变）。
type RateLimitedError struct {
	*apperr.AppError
	// RetryAfter 建议等待时长；≤0 时响应头退化为 1 秒。
	RetryAfter time.Duration
}

// NewRateLimitedError 构造限流错误。
func NewRateLimitedError(message string, retryAfter time.Duration) *RateLimitedError {
	return &RateLimitedError{
		AppError:   apperr.TooManyRequests(message),
		RetryAfter: retryAfter,
	}
}

// Unwrap 暴露内嵌 AppError，保证 errors.As(*apperr.AppError) 与 errors.Is 语义不变。
func (e *RateLimitedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.AppError
}

// RetryAfterSeconds 实现 pkg/httpx.RetryAfterCoder。
func (e *RateLimitedError) RetryAfterSeconds() int {
	if e == nil || e.RetryAfter <= 0 {
		return 1
	}
	seconds := int(math.Ceil(e.RetryAfter.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}
