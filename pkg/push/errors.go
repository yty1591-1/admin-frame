package push

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

var (
	// ErrInvalidConfig 表示驱动配置缺失或非法；应在启动期返回并终止进程。
	ErrInvalidConfig = pkgerrors.NewStd("push: invalid config")
	// ErrUnknownDriver 表示未注册该驱动；通常是对应驱动包未 blank-import。
	ErrUnknownDriver = pkgerrors.NewStd("push: unknown driver")
	// ErrInvalidMessage 表示消息结构、载荷或大小非法；属于调用方错误。
	ErrInvalidMessage = pkgerrors.NewStd("push: invalid message")
	// ErrInvalidRecipient 表示 Token 为空或格式非法；不触网、不重试。
	ErrInvalidRecipient = pkgerrors.NewStd("push: invalid recipient")
	// ErrTokenInvalid 表示 Token 已失效/不属于该应用，应清理设备绑定。
	ErrTokenInvalid = pkgerrors.NewStd("push: token invalid")
	// ErrRejected 表示策略或服务商永久拒绝。
	ErrRejected = pkgerrors.NewStd("push: rejected")
	// ErrNotSupported 表示驱动不支持该能力（如平台不匹配）。
	ErrNotSupported = pkgerrors.NewStd("push: not supported")
	// ErrClosed 表示 Sender 已关闭。
	ErrClosed = pkgerrors.NewStd("push: sender closed")
)

// ProviderError 是渠道错误的结构化视图。
//
// Err 必须包含 pkg/errors 语义错误，便于 pkg/retry 判定是否重试；
// Error 不包含 Token、标题、正文或凭据。
type ProviderError struct {
	// Provider 是驱动名，如 "apns"、"fcm"。
	Provider string
	// Code 是服务商状态或原因码，仅用于排障。
	Code string
	// RetryAfter 是服务商建议等待时间；0 表示未提供。
	RetryAfter time.Duration
	// Err 是底层语义错误；nil 时按 Internal 处理。
	Err error
}

// Error 实现 error；nil 接收者安全。
func (e *ProviderError) Error() string {
	if e == nil {
		return "push: <nil> provider error"
	}
	if e.Err == nil {
		return fmt.Sprintf("push: provider=%s code=%s", e.Provider, e.Code)
	}
	return fmt.Sprintf("push: provider=%s code=%s: %v", e.Provider, e.Code, e.Err)
}

// Unwrap 返回底层语义错误，支持 errors.Is / errors.As 穿透。
func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Retryable 报告该错误是否可安全重试。
func (e *ProviderError) Retryable() bool {
	return e != nil && pkgerrors.RetryableOf(e.Err)
}

// NewProviderError 构造驱动错误；驱动实现应通过本函数返回厂商失败。
func NewProviderError(provider, code string, retryAfter time.Duration, err error) error {
	if err == nil {
		err = pkgerrors.Internal("push: provider call failed")
	}
	return &ProviderError{Provider: provider, Code: code, RetryAfter: retryAfter, Err: err}
}

// Rejected 构造「永久拒绝」错误：包装 ErrRejected 与 FailedPrecondition 语义码。
func Rejected(format string, args ...any) error {
	return fmt.Errorf("%w: %w", ErrRejected, pkgerrors.FailedPrecondition(fmt.Sprintf(format, args...)))
}

// InvalidToken 构造「Token 失效」错误：包装 ErrTokenInvalid 与 FailedPrecondition。
func InvalidToken(format string, args ...any) error {
	return fmt.Errorf("%w: %w", ErrTokenInvalid, pkgerrors.FailedPrecondition(fmt.Sprintf(format, args...)))
}

// MapContextError 将 ctx 取消/超时转为带语义码的驱动错误。
func MapContextError(provider string, err error) error {
	var semantic *pkgerrors.Error
	if errors.Is(err, context.DeadlineExceeded) {
		semantic = pkgerrors.DeadlineExceeded("push: context deadline exceeded")
	} else {
		semantic = pkgerrors.Canceled("push: context canceled")
	}
	return NewProviderError(provider, "context", 0, fmt.Errorf("%w: %w", err, semantic))
}

// CodeOf 返回小写语义码，供 Observer 事件使用；未知错误返回 "internal"。
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	return strings.ToLower(string(pkgerrors.CodeOf(err)))
}

// RetryAfterOf 提取 ProviderError 上的服务商建议等待时间。
func RetryAfterOf(err error) time.Duration {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.RetryAfter
	}
	return 0
}

func invalidMessage(format string, args ...any) error {
	return fmt.Errorf("%w: %w", ErrInvalidMessage, pkgerrors.InvalidArgument(fmt.Sprintf(format, args...)))
}

func invalidRecipient(format string, args ...any) error {
	return fmt.Errorf("%w: %w", ErrInvalidRecipient, pkgerrors.InvalidArgument(fmt.Sprintf(format, args...)))
}
