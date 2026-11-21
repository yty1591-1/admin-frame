package httpx

import (
	"net/http"
	"strconv"

	"github.com/admin-frame/admin-frame/pkg/errors"
)

// RetryAfterCoder 可选：错误携带建议重试秒数；FromError 会写入 Retry-After 响应头。
type RetryAfterCoder interface {
	RetryAfterSeconds() int
}

// writeOptions 控制 FromError 的消息、状态与业务码解析。
type writeOptions struct {
	bizCode func(error) int
	message func(error) string
	status  func(error) int
}

// Option 配置 FromError 行为。
type Option func(*writeOptions)

// WithBizCode 自定义错误到信封数字码的映射；nil 回落默认策略。
func WithBizCode(fn func(error) int) Option {
	return func(o *writeOptions) {
		if fn != nil {
			o.bizCode = fn
		}
	}
}

// WithMessage 自定义客户端可见消息；默认使用 errors.PublicMessageOf。
// C 端宜传入更笼统的文案策略（见 SECURITY_STANDARDS）。
func WithMessage(fn func(error) string) Option {
	return func(o *writeOptions) {
		if fn != nil {
			o.message = fn
		}
	}
}

// WithStatus 自定义 HTTP 状态；默认使用 errors.HTTPStatusOf。
func WithStatus(fn func(error) int) Option {
	return func(o *writeOptions) {
		if fn != nil {
			o.status = fn
		}
	}
}

func applyOptions(opts []Option) writeOptions {
	o := writeOptions{
		bizCode: defaultBizCodeOf,
		message: errors.PublicMessageOf,
		status:  errors.HTTPStatusOf,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// defaultBizCodeOf 解析信封数字码：优先 NumericCoder，其次语义码默认表。
func defaultBizCodeOf(err error) int {
	if err == nil {
		return CodeOK
	}
	var nc NumericCoder
	if errors.As(err, &nc) {
		if code := nc.NumericCode(); code != 0 {
			return code
		}
	}
	return DefaultBizCode(errors.CodeOf(err))
}

// FromError 将 error 映射为统一错误信封并写入响应。
//
// 行为约定：
//   - err == nil 时写入成功空响应（200 / code=0）
//   - 默认 Message 为 PublicMessageOf，避免泄露内部细节
//   - 默认 HTTP 状态为 errors.HTTPStatusOf
//   - 默认业务码：NumericCoder 优先，否则 DefaultBizCode(语义码)
//
// 未知错误不会把 Error() 原文返回客户端。
func FromError(w http.ResponseWriter, err error, traceID string, opts ...Option) {
	if err == nil {
		OK(w, nil, traceID)
		return
	}
	o := applyOptions(opts)
	status := o.status(err)
	code := o.bizCode(err)
	msg := o.message(err)
	if msg == "" {
		msg = "error"
	}
	var retry RetryAfterCoder
	if errors.As(err, &retry) {
		if seconds := retry.RetryAfterSeconds(); seconds > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
	}
	Fail(w, status, code, msg, traceID)
}

// HTTPStatusFromBizCode 将常见信封数字码映射为 HTTP 状态（供业务层复用）。
// 未识别码：≥50000 → 500，其余客户端错误倾向 → 400。
func HTTPStatusFromBizCode(code int) int {
	switch code {
	case CodeOK:
		return http.StatusOK
	case BizCodeBadRequest:
		return http.StatusBadRequest
	case BizCodeUnauthorized:
		return http.StatusUnauthorized
	case BizCodeForbidden:
		return http.StatusForbidden
	case BizCodeNotFound:
		return http.StatusNotFound
	case BizCodeConflict, BizCodeInstallInProgress, BizCodeInstallCompleted:
		return http.StatusConflict
	case BizCodeTooManyRequests:
		return http.StatusTooManyRequests
	case BizCodeServiceUnavailable, BizCodeInstallRequired:
		return http.StatusServiceUnavailable
	case BizCodeInternal:
		return http.StatusInternalServerError
	default:
		if code >= BizCodeInternal {
			return http.StatusInternalServerError
		}
		if code > CodeOK {
			return http.StatusBadRequest
		}
		return http.StatusInternalServerError
	}
}
