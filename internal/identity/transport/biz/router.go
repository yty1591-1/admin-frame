// Package biz 提供 B 端认证路由（/biz/v1/auth/*）。
//
// 路由只做「路径 → Handler → 中间件」绑定；通道无关逻辑在 transport/rest。
package biz

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
)

// Register 将 B 端认证路由挂到 mux。
//
// login / refresh 不挂鉴权（分别凭密码与刷新令牌），其余端点必须携带 Bearer；
// B 端不提供会话列表 / 单会话撤销，商户员工只能登出当前会话或全端。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler) {
	if mux == nil || h == nil {
		return
	}
	if authMW == nil {
		authMW = func(next http.Handler) http.Handler { return next }
	}

	mux.HandleFunc("POST /biz/v1/auth/login", h.Login)
	mux.HandleFunc("POST /biz/v1/auth/refresh", h.Refresh)
	mux.Handle("POST /biz/v1/auth/logout", authMW(http.HandlerFunc(h.Logout)))
	mux.Handle("POST /biz/v1/auth/logout-all", authMW(http.HandlerFunc(h.LogoutAll)))
}
