// Package c 的 HTTP 路由注册。
package c

import (
	"net/http"
)

// Register 注册 C 端认证路由。
//
// login 始终注册；短信未启用时由 Handler 返回 50300，避免路由漂移暴露配置。
// sms/send 仅在短信启用时注册；OAuth 路由仅对已配置 provider 注册。
func Register(mux *http.ServeMux, h *Handler, authMW func(http.Handler) http.Handler) {
	if mux == nil || h == nil {
		return
	}
	if h.sms != nil {
		mux.HandleFunc("POST /c/v1/auth/sms/send", h.SendSMSCode)
	}
	mux.HandleFunc("POST /c/v1/auth/login", h.Login)
	mux.HandleFunc("POST /c/v1/auth/refresh", h.rest.Refresh)
	mux.Handle("POST /c/v1/auth/logout", authMW(http.HandlerFunc(h.rest.Logout)))
	for _, provider := range h.providers {
		mux.HandleFunc("POST /c/v1/auth/oauth/"+provider+"/authorize", h.OAuthAuthorize)
		mux.HandleFunc("POST /c/v1/auth/oauth/"+provider+"/callback", h.OAuthCallback)
	}
}
