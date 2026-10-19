package middleware

import (
	"log/slog"
	"net/http"

	"github.com/admin-frame/admin-frame/pkg/contextx"
)

// RecoverConfig 配置 panic 恢复中间件。
type RecoverConfig struct {
	// Logger 记录 panic；可为 nil（仅响应、不打日志）。
	Logger *slog.Logger
	// OnPanic 自定义响应；nil 时写 500 text/plain。
	// 业务侧应注入信封错误（如 apperr + httpx），勿在此依赖 internal。
	OnPanic func(w http.ResponseWriter, r *http.Request, recovered any)
}

// Recover 捕获 handler panic，记录日志并返回错误响应。
func Recover(cfg RecoverConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if cfg.Logger != nil {
					cfg.Logger.ErrorContext(r.Context(), "panic recovered",
						"panic", rec,
						"path", r.URL.Path,
						"trace_id", contextx.RequestID(r.Context()),
					)
				}
				if cfg.OnPanic != nil {
					cfg.OnPanic(w, r, rec)
					return
				}
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
