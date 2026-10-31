// Package c 的 HTTP 路由注册。
package c

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
)

// Register 注册 C 端认证路由。
//
// login 始终注册；短信未启用时由 Handler 返回 50300，避免路由漂移暴露配置。
// sms/send 仅在短信启用时注册；OAuth 路由仅对已配置 provider 注册。
func Register(mux *http.ServeMux, h *Handler, authMW func(http.Handler) http.Handler) {
	if mux == nil || h == nil {
		return
	}
	// 认证响应含令牌 / 验证码语义，统一禁止缓存（IDN-P2-03）；认证路由使用独立请求体上限（SEC-03）。
	secure := func(next http.Handler) http.Handler {
		return pkghttpx.NoStoreMiddleware(rest.AuthBodyLimit(next))
	}
	if h.sms != nil {
		mux.Handle("POST /c/v1/auth/sms/send", secure(http.HandlerFunc(h.SendSMSCode)))
		mux.Handle("POST /c/v1/auth/sms/register", secure(http.HandlerFunc(h.Register)))
		mux.Handle("POST /c/v1/auth/phone/rebind", secure(authMW(http.HandlerFunc(h.RebindPhone))))
	}
	mux.Handle("POST /c/v1/auth/login", secure(http.HandlerFunc(h.Login)))
	mux.Handle("POST /c/v1/auth/refresh", secure(http.HandlerFunc(h.rest.Refresh)))
	mux.Handle("POST /c/v1/auth/logout", secure(authMW(http.HandlerFunc(h.rest.Logout))))
	mux.Handle("DELETE /c/v1/auth/account", secure(authMW(http.HandlerFunc(h.DeleteAccount))))
	// BIZ-01：C 端个人中心；仅需认证，不回显手机号等 PII，写入审计。
	mux.Handle("GET /c/v1/me", secure(authMW(http.HandlerFunc(h.rest.Me))))
	mux.Handle("PATCH /c/v1/me", secure(authMW(http.HandlerFunc(h.rest.UpdateMe))))
	for _, provider := range h.providers {
		mux.Handle("POST /c/v1/auth/oauth/"+provider+"/authorize", secure(http.HandlerFunc(h.OAuthAuthorize)))
		mux.Handle("POST /c/v1/auth/oauth/"+provider+"/callback", secure(http.HandlerFunc(h.OAuthCallback)))
	}
}
