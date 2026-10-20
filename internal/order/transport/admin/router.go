package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/order"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// Register 将订单路由挂到 mux。
//
// perm 由 cmd 注入（通常为 security.Authorizer.Require）：transport 只依赖
// shared 契约，不 import internal/platform（ARCHITECTURE §6.1）。
func Register(mux *http.ServeMux, h *Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) {
	mux.Handle("GET /admin/v1/orders", authMW(perm(order.PermRead)(http.HandlerFunc(h.List))))
	mux.Handle("GET /admin/v1/orders/{id}", authMW(perm(order.PermRead)(http.HandlerFunc(h.Get))))
	mux.Handle("POST /admin/v1/orders/{id}/cancel", authMW(perm(order.PermWrite)(http.HandlerFunc(h.Cancel))))
}

// NewRouter 注册 A 端订单路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm)
	return mux
}
