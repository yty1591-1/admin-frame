package biz

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/order"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// NewRouter 注册 B 端订单路由；数据范围由 Handler 强制 merchant_id。
//
// perm 由 cmd 注入，transport 不依赖平台层；商户隔离规则仍在 Handler/app。
func NewRouter(h *Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /biz/v1/orders", authMW(perm(order.PermRead)(http.HandlerFunc(h.List))))
	mux.Handle("GET /biz/v1/orders/{id}", authMW(perm(order.PermRead)(http.HandlerFunc(h.Get))))
	mux.Handle("POST /biz/v1/orders/{id}/cancel", authMW(perm(order.PermWrite)(http.HandlerFunc(h.Cancel))))
	return mux
}
