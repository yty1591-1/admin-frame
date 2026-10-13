package errors

import "net/http"

// HTTPStatus 将语义码映射为建议的 HTTP 状态码。
// transport 层可据此写响应；业务数字码仍由调用方自行维护。
func HTTPStatus(code Code) int {
	switch code.normalize() {
	case CodeInvalidArgument, CodeFailedPrecondition, CodeOutOfRange:
		return http.StatusBadRequest
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodePermissionDenied:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeAlreadyExists, CodeAborted:
		return http.StatusConflict
	case CodeResourceExhausted:
		return http.StatusTooManyRequests
	case CodeCanceled:
		return 499 // 客户端关闭（Nginx 惯例）；非 RFC 标准
	case CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case CodeUnimplemented:
		return http.StatusNotImplemented
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeDataLoss, CodeInternal, CodeUnknown:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// HTTPStatusOf 从错误链提取语义码并映射 HTTP 状态。
func HTTPStatusOf(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return HTTPStatus(CodeOf(err))
}
