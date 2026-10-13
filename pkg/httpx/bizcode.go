package httpx

import (
	"github.com/admin-frame/admin-frame/pkg/errors"
)

// 可移植的信封数字码基线（与常见 REST / 本仓库 apperr 约定对齐）。
// 业务仓库应以自身契约码为准，并通过 WithBizCode / NumericCoder 覆盖。
const (
	// BizCodeBadRequest 参数非法。
	BizCodeBadRequest = 40000
	// BizCodeUnauthorized 未认证。
	BizCodeUnauthorized = 40100
	// BizCodeForbidden 无权限。
	BizCodeForbidden = 40300
	// BizCodeNotFound 资源不存在。
	BizCodeNotFound = 40400
	// BizCodeConflict 状态冲突。
	BizCodeConflict = 40900
	// BizCodeTooManyRequests 限流或配额耗尽。
	BizCodeTooManyRequests = 42900
	// BizCodeInternal 内部错误。
	BizCodeInternal = 50000
)

// NumericCoder 由携带稳定信封数字码的错误实现（例如业务 AppError）。
// FromError 优先使用该接口，避免语义码二次猜测。
type NumericCoder interface {
	NumericCode() int
}

// DefaultBizCode 将 pkg/errors 语义码映射为可移植信封数字码。
// 未知语义码：≥ client 类倾向 40000，否则 50000。
func DefaultBizCode(code errors.Code) int {
	switch code {
	case errors.CodeInvalidArgument, errors.CodeFailedPrecondition, errors.CodeOutOfRange:
		return BizCodeBadRequest
	case errors.CodeUnauthenticated:
		return BizCodeUnauthorized
	case errors.CodePermissionDenied:
		return BizCodeForbidden
	case errors.CodeNotFound:
		return BizCodeNotFound
	case errors.CodeAlreadyExists, errors.CodeAborted:
		return BizCodeConflict
	case errors.CodeResourceExhausted:
		return BizCodeTooManyRequests
	case errors.CodeCanceled, errors.CodeDeadlineExceeded,
		errors.CodeUnimplemented, errors.CodeUnavailable,
		errors.CodeDataLoss, errors.CodeInternal, errors.CodeUnknown:
		return BizCodeInternal
	default:
		return BizCodeInternal
	}
}
