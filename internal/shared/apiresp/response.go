package apiresp

import (
	stderrors "errors"
	"log/slog"
	"net/http"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	xerr "github.com/admin-frame/admin-frame/pkg/errors"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
)

// Envelope 统一 JSON 响应体（别名，契约字段与 pkg/httpx 一致）。
type Envelope = pkghttpx.Envelope

// JSON 写入任意业务码与 HTTP 状态的 JSON 响应。
func JSON(w http.ResponseWriter, status, code int, message string, data any, traceID string) {
	pkghttpx.JSON(w, status, code, message, data, traceID)
}

// OK 写入成功响应（code=0）。
func OK(w http.ResponseWriter, data any, traceID string) {
	pkghttpx.OK(w, data, traceID)
}

// FromError 将 error 映射为统一错误响应；未知错误返回 500 且不暴露内部细节。
//
// 优先识别 *apperr.AppError（稳定契约码）；其余委托 pkg/httpx，
// 并用 apperr 语义→数字码表覆盖可移植默认映射，保证与 OpenAPI ErrorCode 一致。
func FromError(w http.ResponseWriter, err error, traceID string) {
	// 5xx 根因必须可定位：响应体对客户端保持笼统文案，但服务端日志记录完整错误链。
	if code := bizCodeOf(err); code >= 50000 {
		slog.Default().Error("internal request failure",
			"code", code, "trace_id", traceID, "error", err)
	}
	pkghttpx.FromError(w, err, traceID,
		pkghttpx.WithBizCode(bizCodeOf),
		pkghttpx.WithStatus(statusOf),
		pkghttpx.WithMessage(messageOf),
	)
}

func bizCodeOf(err error) int {
	var ae *apperr.AppError
	if stderrors.As(err, &ae) {
		return ae.Code
	}
	return apperr.BizCodeFromSemantic(xerr.CodeOf(err))
}

func statusOf(err error) int {
	var ae *apperr.AppError
	if stderrors.As(err, &ae) {
		return pkghttpx.HTTPStatusFromBizCode(ae.Code)
	}
	// 有语义码时优先用 pkg/errors 的 HTTP 映射（含 499/504/503 等），
	// 再保证信封数字码与 apperr 一致。
	if xerr.CodeOf(err) != xerr.CodeUnknown {
		return xerr.HTTPStatusOf(err)
	}
	return pkghttpx.HTTPStatusFromBizCode(bizCodeOf(err))
}

func messageOf(err error) string {
	var ae *apperr.AppError
	if stderrors.As(err, &ae) && ae.Message != "" {
		// AppError.Message 由业务显式构造；内部错误构造时已用笼统文案。
		if ae.Code == apperr.CodeInternal {
			return "internal error"
		}
		return ae.Message
	}
	return xerr.PublicMessageOf(err)
}
