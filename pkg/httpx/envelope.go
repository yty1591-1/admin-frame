package httpx

import (
	"encoding/json"
	"net/http"
)

const (
	// CodeOK 表示成功时的信封业务码。
	CodeOK = 0
	// ContentTypeJSON 是本包写入响应时使用的 Content-Type。
	ContentTypeJSON = "application/json; charset=utf-8"
)

// Envelope 是统一 JSON 响应信封（API 契约基线）。
//
// 成功：Code=0，Message 通常为 "ok"，Data 为业务载荷。
// 失败：Code 为稳定数字错误码，Data 通常省略；禁止在 Message 中放入堆栈、DSN、SQL 等内部细节。
type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
}

// JSON 写入任意业务码与 HTTP 状态的 JSON 信封。
// 编码失败（常见于客户端断开）时忽略错误；需要感知失败时请使用 WriteJSON。
func JSON(w http.ResponseWriter, status, code int, message string, data any, traceID string) {
	_ = WriteJSON(w, status, code, message, data, traceID) // 客户端断开导致 Encode 失败可忽略
}

// WriteJSON 与 JSON 相同，但返回编码错误，供调用方记录日志。
func WriteJSON(w http.ResponseWriter, status, code int, message string, data any, traceID string) error {
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(Envelope{
		Code:    code,
		Message: message,
		Data:    data,
		TraceID: traceID,
	})
}

// OK 写入成功响应（HTTP 200，code=0，message="ok"）。
func OK(w http.ResponseWriter, data any, traceID string) {
	JSON(w, http.StatusOK, CodeOK, "ok", data, traceID)
}

// Fail 写入失败响应（无 data）。status 为 HTTP 状态，code 为信封业务数字码。
func Fail(w http.ResponseWriter, status, code int, message, traceID string) {
	JSON(w, status, code, message, nil, traceID)
}
