package errors

import stderrors "errors"

// Join 合并多个错误，兼容 Go 1.20+ errors.Join。
// 全部为 nil 时返回 nil。
func Join(errs ...error) error {
	return stderrors.Join(errs...)
}

// Combine 将多个错误合并为单个 *Error（CodeInternal），
// 便于统一附加 Op/Fields；无有效错误时返回 nil。
func Combine(msg string, errs ...error) *Error {
	joined := Join(errs...)
	if joined == nil {
		return nil
	}
	return Wrap(joined, msg).WithCode(CodeInternal)
}
