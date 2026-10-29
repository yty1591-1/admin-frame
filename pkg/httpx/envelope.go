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
	// noStoreCacheControl 是敏感响应禁止缓存的 Cache-Control 取值（IDN-P2-03）。
	noStoreCacheControl = "no-store, no-cache, must-revalidate, private"
)

// NoStore 为响应追加禁止缓存头；用于含令牌、MFA 密钥等敏感载荷的端点。
func NoStore(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Cache-Control", noStoreCacheControl)
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
	// 避免不同 Authorization 的响应被共享缓存复用。
	header.Add("Vary", "Authorization")
}

// NoStoreMiddleware 为整条路由统一追加禁止缓存头，避免各 Handler 遗漏。
func NoStoreMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		NoStore(w)
		next.ServeHTTP(w, r)
	})
}

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
