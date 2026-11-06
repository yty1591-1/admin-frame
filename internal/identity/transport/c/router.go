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
	// BIZ-02：身份绑定 / 解绑；绑定回调必须由发起时同一账号完成。
	mux.Handle("GET /c/v1/me/bindings", secure(authMW(http.HandlerFunc(h.ListBindings))))
	mux.Handle("DELETE /c/v1/me/bindings/{provider}", secure(authMW(http.HandlerFunc(h.Unbind))))
	mux.Handle("POST /c/v1/auth/bindings/{provider}/authorize", secure(authMW(http.HandlerFunc(h.OAuthBindAuthorize))))
	mux.Handle("POST /c/v1/auth/bindings/{provider}/callback", secure(authMW(http.HandlerFunc(h.OAuthBindCallback))))
	if len(h.providers) > 0 {
		// 使用通配符注册，handler 从路径取值；未配置的 provider 由用例层返回 40400。
		// 若按字面量拼接注册，PathValue("provider") 为空，所有真实请求都会 40400。
		mux.Handle("POST /c/v1/auth/oauth/{provider}/authorize", secure(http.HandlerFunc(h.OAuthAuthorize)))
		mux.Handle("POST /c/v1/auth/oauth/{provider}/callback", secure(http.HandlerFunc(h.OAuthCallback)))
	}
	if h.saml != nil {
		// ACS 使用 512KB 独立上限（handler 内 MaxBytesReader），不能套用 16KB AuthBodyLimit；
		// metadata 面向 IdP 公开读取，同样不加认证请求体限制。
		mux.Handle("POST /c/v1/auth/saml/{provider}/authorize", secure(http.HandlerFunc(h.SAMLAuthorize)))
		mux.Handle("POST /c/v1/auth/saml/acs", pkghttpx.NoStoreMiddleware(http.HandlerFunc(h.SAMLACS)))
		mux.Handle("POST /c/v1/auth/saml/exchange", secure(http.HandlerFunc(h.SAMLExchange)))
		mux.Handle("GET /c/v1/auth/saml/metadata", pkghttpx.NoStoreMiddleware(http.HandlerFunc(h.SAMLMetadata)))
	}
}
