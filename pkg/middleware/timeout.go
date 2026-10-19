package middleware

import (
	"context"
	"net/http"
	"time"
)

// Timeout 为请求注入 context deadline（软超时）。
//
// Handler / 下游须尊重 ctx.Done()；不会强杀已在写响应的 goroutine。
// 需要硬切断响应时用 HardTimeout。
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d <= 0 {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// HardTimeout 使用标准库 TimeoutHandler：超时后返回 503 与 msg（硬切断）。
func HardTimeout(d time.Duration, msg string) Middleware {
	if msg == "" {
		msg = "request timeout"
	}
	return func(next http.Handler) http.Handler {
		if d <= 0 {
			return next
		}
		return http.TimeoutHandler(next, d, msg)
	}
}
