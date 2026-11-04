// Package biz 提供 B 端通知查询路由（/biz/v1/notifications*）。
//
// 隔离约束：全部查询强制 Principal.CurrentTenant（T-22），租户只能看到
// 自己的通知；B 端不提供代发 / 补偿 / 模板管理。
package biz

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/notification/transport/rest"
)

// Register 把 B 端通知查询路由挂到 mux。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler) {
	mux.Handle("GET /biz/v1/notifications", authMW(http.HandlerFunc(h.List)))
	mux.Handle("GET /biz/v1/notifications/{id}", authMW(http.HandlerFunc(h.Get)))
	mux.Handle("GET /biz/v1/notifications/{id}/deliveries", authMW(http.HandlerFunc(h.Deliveries)))
}

// NewRouter 注册 B 端通知路由并挂接鉴权中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW)
	return mux
}
