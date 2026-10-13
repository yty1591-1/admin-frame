package apperr

import (
	xerr "github.com/admin-frame/admin-frame/pkg/errors"
)

const (
	// CodeOK 表示成功（响应信封用）。
	CodeOK = 0
	// CodeBadRequest 表示请求参数非法。
	CodeBadRequest = 40000
	// CodeUnauthorized 表示未认证或令牌无效。
	CodeUnauthorized = 40100
	// CodeForbidden 表示已认证但无权限。
	CodeForbidden = 40300
	// CodeNotFound 表示资源不存在。
	CodeNotFound = 40400
	// CodeConflict 表示状态冲突或资源已存在。
	CodeConflict = 40900
	// CodeTooManyRequests 表示限流或配额耗尽（REL-05）。
	CodeTooManyRequests = 42900
	// CodeInternal 表示内部错误。
	CodeInternal = 50000
)

var (
	// ErrUnauthorized 未认证哨兵，可用 errors.Is 匹配。
	ErrUnauthorized = xerr.ErrUnauthenticated
	// ErrForbidden 无权限哨兵。
	ErrForbidden = xerr.ErrPermissionDenied
	// ErrNotFound 资源不存在哨兵。
	ErrNotFound = xerr.ErrNotFound
	// ErrConflict 冲突哨兵。
	ErrConflict = xerr.ErrAlreadyExists
	// ErrBadRequest 参数错误哨兵。
	ErrBadRequest = xerr.ErrInvalidArgument
)

// AppError 带稳定业务数字错误码的应用错误。
// 底层通过 Err 挂接 pkg/errors，保留因果链与语义码。
type AppError struct {
	Code    int
	Message string
	Err     error
}

// Error 实现 error 接口，优先返回 Message。
func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "error"
}

// Unwrap 返回底层 cause，供 errors.Is / As 使用。
func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NumericCode 实现 pkg/httpx.NumericCoder，返回稳定信封业务数字码。
func (e *AppError) NumericCode() int {
	if e == nil {
		return CodeInternal
	}
	return e.Code
}

// SemanticCode 将当前业务码映射为 pkg/errors 语义码。
func (e *AppError) SemanticCode() xerr.Code {
	if e == nil {
		return xerr.CodeUnknown
	}
	return MapSemanticCode(e.Code)
}

// BizCodeFromSemantic 将语义码映射为本仓库契约数字码（OpenAPI ErrorCode）。
func BizCodeFromSemantic(code xerr.Code) int {
	switch code {
	case xerr.CodeInvalidArgument, xerr.CodeFailedPrecondition, xerr.CodeOutOfRange:
		return CodeBadRequest
	case xerr.CodeUnauthenticated:
		return CodeUnauthorized
	case xerr.CodePermissionDenied:
		return CodeForbidden
	case xerr.CodeNotFound:
		return CodeNotFound
	case xerr.CodeAlreadyExists, xerr.CodeAborted:
		return CodeConflict
	case xerr.CodeResourceExhausted:
		return CodeTooManyRequests
	case xerr.CodeInternal, xerr.CodeUnknown, xerr.CodeDataLoss,
		xerr.CodeCanceled, xerr.CodeDeadlineExceeded,
		xerr.CodeUnimplemented, xerr.CodeUnavailable:
		return CodeInternal
	default:
		return CodeInternal
	}
}

// MapSemanticCode 将本仓库业务数字码映射为可移植语义码。
func MapSemanticCode(bizCode int) xerr.Code {
	switch bizCode {
	case CodeBadRequest:
		return xerr.CodeInvalidArgument
	case CodeUnauthorized:
		return xerr.CodeUnauthenticated
	case CodeForbidden:
		return xerr.CodePermissionDenied
	case CodeNotFound:
		return xerr.CodeNotFound
	case CodeConflict:
		return xerr.CodeAlreadyExists
	case CodeTooManyRequests:
		return xerr.CodeResourceExhausted
	case CodeInternal:
		return xerr.CodeInternal
	case CodeOK:
		return xerr.CodeUnknown
	default:
		if bizCode >= 50000 {
			return xerr.CodeInternal
		}
		if bizCode >= 40000 {
			return xerr.CodeInvalidArgument
		}
		return xerr.CodeUnknown
	}
}

// New 构造带业务码的 AppError；err 为空时自动挂接语义错误。
func New(code int, message string, err error) *AppError {
	if err == nil {
		err = xerr.New(message).WithCode(MapSemanticCode(code))
	}
	return &AppError{Code: code, Message: message, Err: err}
}

// Unauthorized 返回未认证错误。
func Unauthorized(msg string) *AppError {
	return &AppError{
		Code:    CodeUnauthorized,
		Message: msg,
		Err:     xerr.Unauthenticated(msg),
	}
}

// Forbidden 返回无权限错误。
func Forbidden(msg string) *AppError {
	return &AppError{
		Code:    CodeForbidden,
		Message: msg,
		Err:     xerr.PermissionDenied(msg),
	}
}

// NotFound 返回资源不存在错误。
func NotFound(msg string) *AppError {
	return &AppError{
		Code:    CodeNotFound,
		Message: msg,
		Err:     xerr.NotFound(msg),
	}
}

// BadRequest 返回参数错误。
func BadRequest(msg string) *AppError {
	return &AppError{
		Code:    CodeBadRequest,
		Message: msg,
		Err:     xerr.InvalidArgument(msg),
	}
}

// Conflict 返回状态冲突错误。
func Conflict(msg string) *AppError {
	return &AppError{
		Code:    CodeConflict,
		Message: msg,
		Err:     xerr.AlreadyExists(msg),
	}
}

// TooManyRequests 返回限流 / 配额耗尽错误（业务码 42900）。
func TooManyRequests(msg string) *AppError {
	return &AppError{
		Code:    CodeTooManyRequests,
		Message: msg,
		Err:     xerr.ResourceExhausted(msg),
	}
}

// Internal 返回内部错误并包装 cause。
func Internal(msg string, err error) *AppError {
	return &AppError{
		Code:    CodeInternal,
		Message: msg,
		Err:     xerr.InternalWrap(err, msg),
	}
}
