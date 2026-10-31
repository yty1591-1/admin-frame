// Package biz 提供 B 端认证路由（/biz/v1/auth/*）。
//
// 路由只做「路径 → Handler → 中间件」绑定；通道无关逻辑在 transport/rest。
package biz

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
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

	// 认证响应含令牌，统一禁止缓存（IDN-P2-03）。
	secure := pkghttpx.NoStoreMiddleware

	mux.Handle("POST /biz/v1/auth/login", secure(http.HandlerFunc(h.Login)))
	mux.Handle("POST /biz/v1/auth/refresh", secure(http.HandlerFunc(h.Refresh)))
	mux.Handle("POST /biz/v1/auth/logout", secure(authMW(http.HandlerFunc(h.Logout))))
	mux.Handle("POST /biz/v1/auth/logout-all", secure(authMW(http.HandlerFunc(h.LogoutAll))))
	// GET /biz/v1/me 返回当前商户员工资料；仅需认证，不额外要求权限点。
	// 响应含账号状态等敏感字段，统一禁止缓存。
	mux.Handle("GET /biz/v1/me", secure(authMW(http.HandlerFunc(h.Me))))
	// PATCH /biz/v1/me 只改展示昵称；与 GET 同级，仅需认证。
	mux.Handle("PATCH /biz/v1/me", secure(authMW(http.HandlerFunc(h.UpdateMe))))
}
