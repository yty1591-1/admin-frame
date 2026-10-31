// Package admin 提供 A 端身份域路由（/admin/v1/auth/* 与 /admin/v1/me）。
//
// 路由只做「路径 → Handler → 中间件」绑定；通道无关逻辑在 transport/rest。
package admin

import (
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/identity/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
)

// Register 将 A 端认证路由挂到 mux。
//
// login / refresh 不挂鉴权（分别凭密码与刷新令牌），其余端点必须携带 Bearer。
// 自助端点（会话、改密、MFA）只需本人登录；账号查询要求
// `identity:account:read`，生命周期要求 `identity:account:write`，
// 凭据重置 / 删除 / 会话撤销分别要求 credential / delete / session 权限，
// 禁止匿名或仅凭登录即可查看 / 操作他人账号。
func Register(
	mux *http.ServeMux,
	h *rest.Handler,
	authMW func(http.Handler) http.Handler,
	permMW auth.PermissionMiddleware,
) {
	if mux == nil || h == nil {
		return
	}
	if authMW == nil {
		authMW = func(next http.Handler) http.Handler { return next }
	}
	accountWrite := permission(permMW, string(iam.PermIdentityAccountWrite))
	accountRead := permission(permMW, string(iam.PermIdentityAccountRead))
	// 四类处置动作独立授权，落实职责分离（SEC-02 / IDN-GAP-P1-14）。
	accountCredential := permission(permMW, string(iam.PermIdentityAccountCredential))
	accountDelete := permission(permMW, string(iam.PermIdentityAccountDelete))
	accountSession := permission(permMW, string(iam.PermIdentityAccountSession))

	// 认证与账号端点可能返回令牌、MFA 密钥与账号敏感信息，统一禁止缓存（IDN-P2-03）；
	// 同时为认证路由设置独立请求体上限，避免全局 32MiB 被匿名请求放大（SEC-03）。
	secure := func(next http.Handler) http.Handler {
		return pkghttpx.NoStoreMiddleware(rest.AuthBodyLimit(next))
	}

	mux.Handle("POST /admin/v1/auth/login", secure(http.HandlerFunc(h.Login)))
	mux.Handle("POST /admin/v1/auth/refresh", secure(http.HandlerFunc(h.Refresh)))
	mux.Handle("GET /admin/v1/me", secure(authMW(http.HandlerFunc(h.Me))))
	// PATCH /admin/v1/me 只改展示昵称；与 GET 同级，仅需认证。
	mux.Handle("PATCH /admin/v1/me", secure(authMW(http.HandlerFunc(h.UpdateMe))))
	mux.Handle("POST /admin/v1/auth/logout", secure(authMW(http.HandlerFunc(h.Logout))))
	mux.Handle("POST /admin/v1/auth/logout-all", secure(authMW(http.HandlerFunc(h.LogoutAll))))

	// MFA：setup / verify 支持「已登录 Bearer」与「登录前票据」两条入口；
	// 是否带 Authorization 决定走哪条，票据本身由用例层校验。
	mux.Handle("POST /admin/v1/auth/mfa/setup",
		secure(ticketOrBearer(http.HandlerFunc(h.SetupMFA), authMW(http.HandlerFunc(h.SetupMFA)))))
	mux.Handle("POST /admin/v1/auth/mfa/verify",
		secure(ticketOrBearer(http.HandlerFunc(h.VerifyMFA), authMW(http.HandlerFunc(h.VerifyMFA)))))
	mux.Handle("POST /admin/v1/auth/mfa/disable", secure(authMW(http.HandlerFunc(h.DisableMFA))))

	// 会话管理：列表、单会话撤销与全量撤销。
	mux.Handle("GET /admin/v1/auth/sessions", secure(authMW(http.HandlerFunc(h.ListSessions))))
	mux.Handle("DELETE /admin/v1/auth/sessions/{id}", secure(authMW(http.HandlerFunc(h.RevokeSession))))
	mux.Handle("POST /admin/v1/auth/sessions/revoke-all", secure(authMW(http.HandlerFunc(h.LogoutAll))))

	// 改密：Bearer 走自助改密；无 Authorization 时按强制改密票据处理。
	mux.Handle("POST /admin/v1/auth/password/change",
		secure(ticketOrBearer(http.HandlerFunc(h.ChangePassword), authMW(http.HandlerFunc(h.ChangePassword)))))

	// 账号查询：identity:account:read。
	mux.Handle("GET /admin/v1/auth/accounts",
		secure(authMW(accountRead(http.HandlerFunc(h.ListAccounts)))))
	mux.Handle("GET /admin/v1/auth/accounts/{id}",
		secure(authMW(accountRead(http.HandlerFunc(h.GetAccount)))))
	mux.Handle("GET /admin/v1/auth/accounts/{id}/sessions",
		secure(authMW(accountRead(http.HandlerFunc(h.ListAccountSessions)))))

	// 账号生命周期管理：identity:account:write（不含凭据、删除与会话撤销）。
	// 所有处置动作强制 Idempotency-Key，避免重试造成重复撤销 / 重复重置（P1-11）。
	mux.Handle("POST /admin/v1/auth/accounts/{id}/disable",
		secure(authMW(accountWrite(idempotent(http.HandlerFunc(h.DisableAccount))))))
	mux.Handle("POST /admin/v1/auth/accounts/{id}/enable",
		secure(authMW(accountWrite(idempotent(http.HandlerFunc(h.EnableAccount))))))
	mux.Handle("POST /admin/v1/auth/accounts/{id}/lock",
		secure(authMW(accountWrite(idempotent(http.HandlerFunc(h.LockAccount))))))
	mux.Handle("POST /admin/v1/auth/accounts/{id}/unlock",
		secure(authMW(accountWrite(idempotent(http.HandlerFunc(h.UnlockAccount))))))
	// 凭据重置：identity:account:credential，与生命周期权限分离。
	mux.Handle("POST /admin/v1/auth/accounts/{id}/mfa/reset",
		secure(authMW(accountCredential(idempotent(http.HandlerFunc(h.ResetAccountMFA))))))
	mux.Handle("POST /admin/v1/auth/accounts/{id}/password/reset",
		secure(authMW(accountCredential(idempotent(http.HandlerFunc(h.ResetAccountPassword))))))
	// 删除 / 恢复：删除独立授权，恢复仍属生命周期写。
	mux.Handle("DELETE /admin/v1/auth/accounts/{id}",
		secure(authMW(accountDelete(idempotent(http.HandlerFunc(h.DeleteAccount))))))
	mux.Handle("POST /admin/v1/auth/accounts/{id}/restore",
		secure(authMW(accountWrite(idempotent(http.HandlerFunc(h.RestoreAccount))))))
	// 会话撤销：identity:account:session。
	mux.Handle("DELETE /admin/v1/auth/accounts/{id}/sessions/{sid}",
		secure(authMW(accountSession(idempotent(http.HandlerFunc(h.RevokeAccountSession))))))
	mux.Handle("POST /admin/v1/auth/accounts/{id}/sessions/revoke-all",
		secure(authMW(accountSession(idempotent(http.HandlerFunc(h.RevokeAccountSessions))))))
}

// permission 返回权限中间件；未注入时失败关闭（403），禁止账号管理端点裸奔。
func permission(permMW auth.PermissionMiddleware, code string) func(http.Handler) http.Handler {
	if permMW == nil {
		return func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "permission middleware is not configured", http.StatusForbidden)
			})
		}
	}
	return permMW(code)
}

// idempotent 强制账号处置请求携带 Idempotency-Key。
//
// 全局 pipeline 在配置启用时负责真正的去重与响应回放；本中间件只负责拒绝缺少
// 幂等键的高风险写入，避免「配置未强制」导致重复撤销 / 重复重置。
func idempotent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
			httpx.FromError(w, apperr.BadRequest("Idempotency-Key header is required"),
				httpx.TraceIDFromContext(r.Context()))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ticketOrBearer 按是否携带 Authorization 切换鉴权：
// 无头走票据路径（MFA 票据 / 强制改密票据），有头走 Bearer 路径。
// 票据本身由用例层校验，路由层不解析请求体。
func ticketOrBearer(ticketPath, authPath http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) == "" {
			ticketPath.ServeHTTP(w, r)
			return
		}
		authPath.ServeHTTP(w, r)
	}
}
