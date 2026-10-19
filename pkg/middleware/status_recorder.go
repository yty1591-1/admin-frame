package middleware

import "net/http"

// StatusRecorder 记录写入的 HTTP 状态码，供 AccessLog / Metrics 使用。
type StatusRecorder struct {
	http.ResponseWriter
	Status int
}

// NewStatusRecorder 默认状态 200（若 handler 未显式 WriteHeader）。
func NewStatusRecorder(w http.ResponseWriter) *StatusRecorder {
	return &StatusRecorder{ResponseWriter: w, Status: http.StatusOK}
}

// WriteHeader 记录状态码并转发给底层。
func (r *StatusRecorder) WriteHeader(code int) {
	r.Status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap 支持 ResponseController 透传。
func (r *StatusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
