package email

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
	ErrInvalidConfig = pkgerrors.NewStd("email: invalid config")
	// ErrUnknownDriver 表示未注册该驱动；通常是对应驱动包未 blank-import。
	ErrUnknownDriver = pkgerrors.NewStd("email: unknown driver")
	// ErrInvalidMessage 表示邮件结构、Header 或大小非法；属于调用方错误。
	ErrInvalidMessage = pkgerrors.NewStd("email: invalid message")
	// ErrInvalidRecipient 表示收件人地址非法或数量超限；不触网、不重试。
	ErrInvalidRecipient = pkgerrors.NewStd("email: invalid recipient")
	// ErrRejected 表示策略或服务商永久拒绝（可执行附件、内容/域名被拒等）。
	ErrRejected = pkgerrors.NewStd("email: rejected")
	// ErrNotSupported 表示驱动不支持该能力。
	ErrNotSupported = pkgerrors.NewStd("email: not supported")
	// ErrClosed 表示 Sender 已关闭。
	ErrClosed = pkgerrors.NewStd("email: sender closed")
)

// ProviderError 是渠道错误的结构化视图。
//
// Err 必须包含 pkg/errors 语义错误，便于 pkg/retry 判定是否重试；
// Error 不包含收件人、正文、附件与认证凭据。
type ProviderError struct {
	// Provider 是驱动名，如 "smtp"。
	Provider string
	// Code 是服务商响应码或内部阶段名，仅用于排障。
	Code string
	// RetryAfter 是服务商建议等待时间；0 表示未提供。
	RetryAfter time.Duration
	// Err 是底层语义错误；nil 时按 Internal 处理。
	Err error
}

// Error 实现 error；nil 接收者安全。
func (e *ProviderError) Error() string {
	if e == nil {
		return "email: <nil> provider error"
	}
	if e.Err == nil {
		return fmt.Sprintf("email: provider=%s code=%s", e.Provider, e.Code)
	}
	return fmt.Sprintf("email: provider=%s code=%s: %v", e.Provider, e.Code, e.Err)
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
		err = pkgerrors.Internal("email: provider call failed")
	}
	return &ProviderError{Provider: provider, Code: code, RetryAfter: retryAfter, Err: err}
}

// Rejected 构造「永久拒绝」错误：包装 ErrRejected 与 FailedPrecondition 语义码。
func Rejected(format string, args ...any) error {
	return fmt.Errorf("%w: %w", ErrRejected, pkgerrors.FailedPrecondition(fmt.Sprintf(format, args...)))
}

// MapContextError 将 ctx 取消/超时转为带语义码的驱动错误。
func MapContextError(provider string, err error) error {
	var semantic *pkgerrors.Error
	if errors.Is(err, context.DeadlineExceeded) {
		semantic = pkgerrors.DeadlineExceeded("email: context deadline exceeded")
	} else {
		semantic = pkgerrors.Canceled("email: context canceled")
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

func rejected(format string, args ...any) error {
	return Rejected(format, args...)
}
