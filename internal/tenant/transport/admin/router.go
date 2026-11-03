// Package admin 提供 A 端租户管理路由（/admin/v1/tenants/*）。
//
// 路由与权限点集中注册；读与写使用不同权限点，便于最小权限授予
// （运营可看租户但不能停用）。transport 只依赖 shared 契约，不 import
// internal/platform（LAYER-01）。
package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/tenant/transport/rest"
)

// Register 把租户管理路由挂到 mux。
//
// 四个迁移动作共用 tenant:write：拆成独立权限点会让「谁能停用」这类问题
// 依赖角色组合推断，反而难以审计；动作差异已由审计 action 区分。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) {
	read := perm(string(iam.PermTenantRead))
	write := perm(string(iam.PermTenantWrite))

	mux.Handle("GET /admin/v1/tenants", authMW(read(http.HandlerFunc(h.List))))
	mux.Handle("POST /admin/v1/tenants", authMW(write(http.HandlerFunc(h.Create))))
	mux.Handle("GET /admin/v1/tenants/{id}", authMW(read(http.HandlerFunc(h.Get))))

	mux.Handle("POST /admin/v1/tenants/{id}/activate", authMW(write(http.HandlerFunc(h.Activate))))
	mux.Handle("POST /admin/v1/tenants/{id}/suspend", authMW(write(http.HandlerFunc(h.Suspend))))
	mux.Handle("POST /admin/v1/tenants/{id}/resume", authMW(write(http.HandlerFunc(h.Resume))))
	mux.Handle("POST /admin/v1/tenants/{id}/terminate", authMW(write(http.HandlerFunc(h.Terminate))))
}

// NewRouter 注册租户路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm)
	return mux
}
