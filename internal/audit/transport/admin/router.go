// Package admin 提供 A 端审计路由（/admin/v1/audit/*）。
//
// 路由与权限点集中注册；查询与导出共用 `audit:read`（导出范围另由
// audit.export.* 配置与审计事件约束），与权限目录 catalog 保持一致。
package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/audit/transport/rest"
	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// Register 把审计查询与导出路由挂到 mux。
//
// perm 由 cmd 注入（security.Authorizer.Require）；transport 只依赖 shared 契约，
// 不 import internal/platform（LAYER-01）。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) {
	mux.Handle("GET /admin/v1/audit/events",
		authMW(perm(string(iam.PermAuditRead))(http.HandlerFunc(h.List))))
	mux.Handle("GET /admin/v1/audit/events/export",
		authMW(perm(string(iam.PermAuditRead))(http.HandlerFunc(h.Export))))
}

// NewRouter 注册审计路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm)
	return mux
}
