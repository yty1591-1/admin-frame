package errors

import (
	stderrors "errors"
	"fmt"
)

var (
	// Is 判定错误链是否匹配 target，等价于标准库 errors.Is。
	Is = stderrors.Is
	// As 在错误链中查找并赋值到 target，等价于标准库 errors.As。
	As = stderrors.As
	// Unwrap 返回被包装的下一层错误，等价于标准库 errors.Unwrap。
	Unwrap = stderrors.Unwrap
)

// NewStd 创建标准库 sentinel（无栈），用于纯领域哨兵。
func NewStd(text string) error {
	return stderrors.New(text)
}

// Errorf 等价于 fmt.Errorf，支持 %w。
func Errorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// AsError 将 err 转为 *Error；若本就是 *Error 则直接返回，
// 否则包装为 CodeUnknown（保留 cause）。nil 返回 nil。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if stderrors.As(err, &e) {
		return e
	}
	return Wrap(err, err.Error()).WithCode(CodeUnknown)
}

// CodeOf 从错误链提取首个非空语义码；无则 CodeUnknown。
func CodeOf(err error) Code {
	if err == nil {
		return CodeUnknown
	}
	var e *Error
	if stderrors.As(err, &e) {
		return e.Code()
	}
	return CodeUnknown
}

// IsCode 判断错误链是否匹配指定语义码。
func IsCode(err error, code Code) bool {
	if err == nil {
		return false
	}
	return CodeOf(err) == code.normalize()
}

// MessageOf 提取对外消息；非 *Error 时返回 Error()。
func MessageOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if stderrors.As(err, &e) {
		if e.msg != "" {
			return e.msg
		}
	}
	return err.Error()
}

// PublicMessageOf 提取可安全返回客户端的消息。
func PublicMessageOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if stderrors.As(err, &e) {
		return e.PublicMessage()
	}
	return "internal error"
}

// OpOf 提取操作名。
func OpOf(err error) string {
	var e *Error
	if stderrors.As(err, &e) {
		return e.Op()
	}
	return ""
}

// FieldsOf 提取结构化字段。
func FieldsOf(err error) []Field {
	var e *Error
	if stderrors.As(err, &e) {
		return e.Fields()
	}
	return nil
}

// RetryableOf 判断是否可重试。
func RetryableOf(err error) bool {
	if err == nil {
		return false
	}
	var e *Error
	if stderrors.As(err, &e) {
		return e.Retryable()
	}
	return false
}

// Cause 沿 Unwrap 走到最内层错误。
func Cause(err error) error {
	for err != nil {
		u := stderrors.Unwrap(err)
		if u == nil {
			return err
		}
		err = u
	}
	return nil
}
