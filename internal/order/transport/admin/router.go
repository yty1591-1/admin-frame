package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// Register 将订单路由挂到 mux。
func Register(mux *http.ServeMux, h *Handler, authMW func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/v1/orders", authMW(platform.RequirePermission("order:read")(http.HandlerFunc(h.List))))
	mux.Handle("GET /admin/v1/orders/{id}", authMW(platform.RequirePermission("order:read")(http.HandlerFunc(h.Get))))
	mux.Handle("POST /admin/v1/orders/{id}/cancel", authMW(platform.RequirePermission("order:write")(http.HandlerFunc(h.Cancel))))
}

// NewRouter 注册 A 端订单路由并挂接鉴权与权限中间件。
func NewRouter(h *Handler, authMW func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW)
	return mux
}
