// Package admin 提供 A 端认证路由（/admin/v1/auth/*）。
//
// 路由只做「路径 → Handler → 中间件」绑定；通道无关逻辑在 transport/rest。
package admin

import (
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
)

// Register 将 A 端认证路由挂到 mux。
//
// login / refresh 不挂鉴权（分别凭密码与刷新令牌），其余端点必须携带 Bearer；
// 本包不注册权限点：已认证账号管理自己的会话，不需要额外 RBAC 授权。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler) {
	if mux == nil || h == nil {
		return
	}
	if authMW == nil {
		authMW = func(next http.Handler) http.Handler { return next }
	}

	mux.HandleFunc("POST /admin/v1/auth/login", h.Login)
	mux.HandleFunc("POST /admin/v1/auth/refresh", h.Refresh)
	mux.Handle("POST /admin/v1/auth/logout", authMW(http.HandlerFunc(h.Logout)))
	mux.Handle("POST /admin/v1/auth/logout-all", authMW(http.HandlerFunc(h.LogoutAll)))

	// MFA：setup / verify 支持「已登录 Bearer」与「登录前票据」两条入口；
	// 是否带 Authorization 决定走哪条，票据本身由用例层校验。
	mux.HandleFunc("POST /admin/v1/auth/mfa/setup",
		mfaTicketOrBearer(http.HandlerFunc(h.SetupMFA), authMW(http.HandlerFunc(h.SetupMFA))))
	mux.HandleFunc("POST /admin/v1/auth/mfa/verify",
		mfaTicketOrBearer(http.HandlerFunc(h.VerifyMFA), authMW(http.HandlerFunc(h.VerifyMFA))))
	mux.Handle("POST /admin/v1/auth/mfa/disable", authMW(http.HandlerFunc(h.DisableMFA)))

	// 会话管理：列表、单会话撤销与全量撤销。
	mux.Handle("GET /admin/v1/auth/sessions", authMW(http.HandlerFunc(h.ListSessions)))
	mux.Handle("DELETE /admin/v1/auth/sessions/{id}", authMW(http.HandlerFunc(h.RevokeSession)))
	mux.Handle("POST /admin/v1/auth/sessions/revoke-all", authMW(http.HandlerFunc(h.LogoutAll)))
}

// mfaTicketOrBearer 按是否携带 Authorization 切换鉴权：
// 无头走票据路径（setup 凭 mfa_setup_ticket，verify 凭 mfa_ticket / mfa_setup_ticket），
// 有头走 Bearer 路径。两种票据都由用例层校验，路由层不解析请求体。
func mfaTicketOrBearer(ticketPath, authPath http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) == "" {
			ticketPath.ServeHTTP(w, r)
			return
		}
		authPath.ServeHTTP(w, r)
	}
}
