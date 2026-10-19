package retry

import (
	stderrors "errors"
	"fmt"
)

// permanentError 标记错误为不可重试（即使 RetryIf 返回 true）。
type permanentError struct {
	err error
}

// Permanent 包装 err，使 Do / DoValue 立即停止重试并返回底层错误。
// err 为 nil 时返回 nil。
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// Error 实现 error。
func (e *permanentError) Error() string {
	if e == nil || e.err == nil {
		return "permanent error"
	}
	return e.err.Error()
}

// Unwrap 返回被包装错误。
func (e *permanentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// IsPermanent 报告 err 链是否含 Permanent 包装。
func IsPermanent(err error) bool {
	var pe *permanentError
	return stderrors.As(err, &pe)
}

// ExhaustedError 表示次数或时间预算耗尽后仍失败。
type ExhaustedError struct {
	// Attempts 已执行的尝试次数。
	Attempts int
	// Last 最后一次错误（未再包装 Permanent）。
	Last error
}

// Error 实现 error。
func (e *ExhaustedError) Error() string {
	if e == nil {
		return "retry exhausted"
	}
	if e.Last == nil {
		return fmt.Sprintf("retry exhausted after %d attempt(s)", e.Attempts)
	}
	return fmt.Sprintf("retry exhausted after %d attempt(s): %v", e.Attempts, e.Last)
}

// Unwrap 返回最后一次错误，供 errors.Is / As 使用。
func (e *ExhaustedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Last
}

// IsExhausted 报告是否为次数/预算耗尽错误。
func IsExhausted(err error) bool {
	var ee *ExhaustedError
	return stderrors.As(err, &ee)
}
