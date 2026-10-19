package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/admin-frame/admin-frame/pkg/contextx"
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

// AccessLog 记录访问日志（method/path/status/duration + trace_id/span_id）。
func AccessLog(log *slog.Logger) Middleware {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := NewStatusRecorder(w)
			next.ServeHTTP(rw, r)
			log.InfoContext(r.Context(), "access",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.Status,
				"duration_ms", time.Since(start).Milliseconds(),
				"trace_id", contextx.RequestID(r.Context()),
				"span_id", tracing.SpanID(r.Context()),
			)
		})
	}
}
