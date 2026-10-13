package c

import "net/http"

// NewRouter 注册 C 端订单路由。
func NewRouter(h *Handler, authMW func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /c/v1/orders", authMW(http.HandlerFunc(h.Create)))
	mux.Handle("GET /c/v1/orders", authMW(http.HandlerFunc(h.List)))
	mux.Handle("GET /c/v1/orders/{id}", authMW(http.HandlerFunc(h.Get)))
	mux.Handle("POST /c/v1/orders/{id}/cancel", authMW(http.HandlerFunc(h.Cancel)))
	return mux
}
