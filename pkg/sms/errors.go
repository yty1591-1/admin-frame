package sms

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
	ErrInvalidConfig = pkgerrors.NewStd("sms: invalid config")
	// ErrUnknownDriver 表示未注册该驱动；通常是对应驱动包未 blank-import。
	ErrUnknownDriver = pkgerrors.NewStd("sms: unknown driver")
	// ErrInvalidMessage 表示消息结构或长度非法；属于调用方错误，不应重试。
	ErrInvalidMessage = pkgerrors.NewStd("sms: invalid message")
	// ErrInvalidRecipient 表示手机号格式非法；不触网、不重试。
	ErrInvalidRecipient = pkgerrors.NewStd("sms: invalid recipient")
	// ErrRejected 表示策略或厂商永久拒绝（裸内容未开启、模板/签名非法等）。
	ErrRejected = pkgerrors.NewStd("sms: rejected")
	// ErrNotSupported 表示驱动不支持该能力（如批量）。
	ErrNotSupported = pkgerrors.NewStd("sms: not supported")
	// ErrClosed 表示 Sender 已关闭。
	ErrClosed = pkgerrors.NewStd("sms: sender closed")
)

// ProviderError 是渠道错误的结构化视图。
//
// Err 必须包含 pkg/errors 语义错误，从而可被 pkg/retry 正确判定是否重试：
//
//	errors.Is(err, sms.ErrRejected)                  // 永久拒绝
//	errors.Is(err, errors.CodeUnavailable)           // 语义码判定用 IsCode
//	var pe *sms.ProviderError; errors.As(err, &pe)   // 取厂商原始码与 RetryAfter
//
// Error 只输出驱动名、厂商码与语义描述，不包含手机号、正文或请求参数。
type ProviderError struct {
	// Provider 是驱动名，如 "aliyun"。
	Provider string
	// Code 是厂商原始码，仅用于排障；禁止作为指标标签。
	Code string
	// RetryAfter 是厂商建议的等待时间；0 表示未提供。
	RetryAfter time.Duration
	// Err 是底层语义错误；nil 时按 Internal 处理。
	Err error
}

// Error 实现 error；nil 接收者安全。
func (e *ProviderError) Error() string {
	if e == nil {
		return "sms: <nil> provider error"
	}
	if e.Err == nil {
		return fmt.Sprintf("sms: provider=%s code=%s", e.Provider, e.Code)
	}
	return fmt.Sprintf("sms: provider=%s code=%s: %v", e.Provider, e.Code, e.Err)
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
//
// code 为厂商原始码（仅排障），retryAfter 为厂商建议等待时间（未提供传 0）。
func NewProviderError(provider, code string, retryAfter time.Duration, err error) error {
	if err == nil {
		err = pkgerrors.Internal("sms: provider call failed")
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
		semantic = pkgerrors.DeadlineExceeded("sms: context deadline exceeded")
	} else {
		semantic = pkgerrors.Canceled("sms: context canceled")
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

// RetryAfterOf 提取 ProviderError 上的厂商建议等待时间。
func RetryAfterOf(err error) time.Duration {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.RetryAfter
	}
	return 0
}

// TruncateRunes 按 Unicode 码点截断，避免切断多字节字符；limit<=0 返回空串。
func TruncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
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

func codeOf(err error) string { return CodeOf(err) }

func retryAfterOf(err error) time.Duration { return RetryAfterOf(err) }
