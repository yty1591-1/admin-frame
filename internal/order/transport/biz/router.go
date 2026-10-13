package biz

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// NewRouter 注册 B 端订单路由；数据范围由 Handler 强制 merchant_id。
func NewRouter(h *Handler, authMW func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /biz/v1/orders", authMW(platform.RequirePermission("order:read")(http.HandlerFunc(h.List))))
	mux.Handle("GET /biz/v1/orders/{id}", authMW(platform.RequirePermission("order:read")(http.HandlerFunc(h.Get))))
	mux.Handle("POST /biz/v1/orders/{id}/cancel", authMW(platform.RequirePermission("order:write")(http.HandlerFunc(h.Cancel))))
	return mux
}
