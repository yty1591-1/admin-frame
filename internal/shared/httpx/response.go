package httpx

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// Envelope 统一 JSON 响应体。
type Envelope struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	TraceID string      `json:"trace_id,omitempty"`
}

// JSON 写入任意业务码与 HTTP 状态的 JSON 响应。
func JSON(w http.ResponseWriter, status int, code int, message string, data interface{}, traceID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{ // 响应写入失败时只能记录日志，此处忽略
		Code:    code,
		Message: message,
		Data:    data,
		TraceID: traceID,
	})
}

// OK 写入成功响应（code=0）。
func OK(w http.ResponseWriter, data interface{}, traceID string) {
	JSON(w, http.StatusOK, apperr.CodeOK, "ok", data, traceID)
}

// FromError 将 error 映射为统一错误响应；未知错误返回 500 且不暴露内部细节。
func FromError(w http.ResponseWriter, err error, traceID string) {
	var ae *apperr.AppError
	if errors.As(err, &ae) {
		JSON(w, httpStatus(ae.Code), ae.Code, ae.Message, nil, traceID)
		return
	}
	switch {
	case errors.Is(err, apperr.ErrUnauthorized):
		JSON(w, http.StatusUnauthorized, apperr.CodeUnauthorized, "unauthorized", nil, traceID)
	case errors.Is(err, apperr.ErrForbidden):
		JSON(w, http.StatusForbidden, apperr.CodeForbidden, "forbidden", nil, traceID)
	case errors.Is(err, apperr.ErrNotFound):
		JSON(w, http.StatusNotFound, apperr.CodeNotFound, "not found", nil, traceID)
	case errors.Is(err, apperr.ErrBadRequest):
		JSON(w, http.StatusBadRequest, apperr.CodeBadRequest, err.Error(), nil, traceID)
	case errors.Is(err, apperr.ErrConflict):
		JSON(w, http.StatusConflict, apperr.CodeConflict, err.Error(), nil, traceID)
	default:
		JSON(w, http.StatusInternalServerError, apperr.CodeInternal, "internal error", nil, traceID)
	}
}

func httpStatus(code int) int {
	switch code {
	case apperr.CodeBadRequest:
		return http.StatusBadRequest
	case apperr.CodeUnauthorized:
		return http.StatusUnauthorized
	case apperr.CodeForbidden:
		return http.StatusForbidden
	case apperr.CodeNotFound:
		return http.StatusNotFound
	case apperr.CodeConflict:
		return http.StatusConflict
	default:
		if code >= 50000 {
			return http.StatusInternalServerError
		}
		return http.StatusBadRequest
	}
}
