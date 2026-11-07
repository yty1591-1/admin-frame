package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/middleware"
)

// Chain 按顺序组合中间件（委托 pkg/middleware）。
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	converted := make([]middleware.Middleware, 0, len(mws))
	for _, mw := range mws {
		if mw != nil {
			converted = append(converted, mw)
		}
	}
	return middleware.Chain(h, converted...)
}

// Recover 捕获 handler panic，记录日志并返回内部错误信封。
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return middleware.Recover(middleware.RecoverConfig{
		Logger: log,
		OnPanic: func(w http.ResponseWriter, r *http.Request, _ any) {
			httpx.FromError(w, apperr.Internal("internal error", nil), httpx.TraceIDFromContext(r.Context()))
		},
	})
}

// RequestID 注入/回写 X-Request-ID（委托 pkg/middleware；OBS-05）。
func RequestID() func(http.Handler) http.Handler {
	return middleware.RequestID()
}

// AccessLog 记录访问日志（委托 pkg/middleware；含 trace_id）。
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return middleware.AccessLog(log)
}
