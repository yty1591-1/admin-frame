package middleware

import "net/http"

// Middleware 是标准 HTTP 中间件签名。
type Middleware func(http.Handler) http.Handler

// Chain 按顺序组合中间件（先传的更靠外，最先执行）。
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] != nil {
			h = mws[i](h)
		}
	}
	return h
}
