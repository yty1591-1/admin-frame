package mq

import (
	"context"
	stderrors "errors"
	"fmt"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// Disposition 消费结果处置。
type Disposition int

const (
	// DispositionAck 确认成功，不再投递。
	DispositionAck Disposition = iota
	// DispositionRetry 可重试，重新入队。
	DispositionRetry
	// DispositionDeadLetter 永久失败或超次，进入死信。
	DispositionDeadLetter
)

// ConsumeOptions 控制重试与死信。
type ConsumeOptions struct {
	// MaxAttempts 最大投递次数（含首次）；≤0 表示 5。
	MaxAttempts int
	// RetryIf 判定是否可重试；nil 时使用默认：非 Permanent 且 pkg/errors.RetryableOf。
	RetryIf func(error) bool
	// DeadLetter 可选死信队列；nil 时 DispositionDeadLetter 仅丢弃（仍 Ack，防阻塞）。
	DeadLetter DeadLetter
}

func (o ConsumeOptions) withDefaults() ConsumeOptions {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 5
	}
	if o.RetryIf == nil {
		o.RetryIf = defaultRetryIf
	}
	return o
}

func defaultRetryIf(err error) bool {
	if err == nil {
		return false
	}
	if retry.IsPermanent(err) {
		return false
	}
	if stderrors.Is(err, ErrHandlerPanic) {
		return false
	}
	return pkgerrors.RetryableOf(err)
}

// Classify 根据错误与 Attempt 决定处置。
func Classify(err error, attempt int, opts ConsumeOptions) Disposition {
	opts = opts.withDefaults()
	if err == nil {
		return DispositionAck
	}
	if attempt < 1 {
		attempt = 1
	}
	if !opts.RetryIf(err) {
		return DispositionDeadLetter
	}
	if attempt >= opts.MaxAttempts {
		return DispositionDeadLetter
	}
	return DispositionRetry
}

// DeadLetter 死信投递接口。
type DeadLetter interface {
	Send(ctx context.Context, msg Message, cause error) error
}

// DeadLetterFunc 函数适配 DeadLetter。
type DeadLetterFunc func(ctx context.Context, msg Message, cause error) error

// Send 实现 DeadLetter。
func (f DeadLetterFunc) Send(ctx context.Context, msg Message, cause error) error {
	if f == nil {
		return nil
	}
	return f(ctx, msg, cause)
}

// Wrap 包装业务 Handler：捕获 panic、按 Classify 重试或死信。
//
// 返回的 Handler 在需要重试时返回 *retrySignal（供 Memory 等实现识别）；
// 死信后返回 nil（Ack），避免毒消息阻塞。
func Wrap(h Handler, opts ConsumeOptions) Handler {
	opts = opts.withDefaults()
	return func(ctx context.Context, msg Message) (err error) {
		if h == nil {
			return retry.Permanent(ErrNoHandler)
		}
		if msg.Attempt < 1 {
			msg.Attempt = 1
		}
		defer func() {
			if r := recover(); r != nil {
				err = retry.Permanent(fmtPanic(r))
			}
		}()

		err = h(ctx, msg)
		switch Classify(err, msg.Attempt, opts) {
		case DispositionAck:
			return nil
		case DispositionRetry:
			return &retrySignal{cause: err, attempt: msg.Attempt}
		default:
			if opts.DeadLetter != nil {
				_ = opts.DeadLetter.Send(ctx, msg, err)
			}
			return nil
		}
	}
}

type retrySignal struct {
	cause   error
	attempt int
}

func (e *retrySignal) Error() string {
	if e == nil || e.cause == nil {
		return "mq: retry"
	}
	return e.cause.Error()
}

func (e *retrySignal) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func isRetrySignal(err error) bool {
	var rs *retrySignal
	return stderrors.As(err, &rs)
}

func fmtPanic(r any) error {
	return fmt.Errorf("%w: %s", ErrHandlerPanic, panicString(r))
}

func panicString(r any) string {
	switch v := r.(type) {
	case string:
		return v
	case error:
		return v.Error()
	default:
		return "panic"
	}
}
