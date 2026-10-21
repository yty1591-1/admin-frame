// Package admin 提供 A 端 IAM 管理路由（/admin/v1/iam/*）。
//
// 路由与权限点在此集中注册，Handler 复用 transport/rest；本包不感知
// 仓储与缓存实现，避免 transport 反向依赖 infrastructure（CI-09）。
package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// Register 将 IAM 管理端路由挂到 mux。
//
// perm 由 cmd 注入（security.Authorizer.Require）：transport 只依赖 shared 契约，
// 不 import internal/platform（ARCHITECTURE §6.1）。读接口与写接口使用不同权限点，
// 便于最小权限授予。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) {
	// 权限目录（只读）。
	mux.Handle("GET /admin/v1/iam/permissions", authMW(perm(string(iam.PermPermissionRead))(http.HandlerFunc(h.ListPermissions))))
	mux.Handle("GET /admin/v1/iam/permissions/tree", authMW(perm(string(iam.PermPermissionRead))(http.HandlerFunc(h.PermissionTree))))

	// 角色生命周期。
	mux.Handle("GET /admin/v1/iam/roles", authMW(perm(string(iam.PermRoleRead))(http.HandlerFunc(h.ListRoles))))
	mux.Handle("POST /admin/v1/iam/roles", authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.CreateRole))))
	mux.Handle("GET /admin/v1/iam/roles/{id}", authMW(perm(string(iam.PermRoleRead))(http.HandlerFunc(h.GetRole))))
	mux.Handle("PATCH /admin/v1/iam/roles/{id}", authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.UpdateRole))))
	mux.Handle("POST /admin/v1/iam/roles/{id}/clone", authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.CloneRole))))
	mux.Handle("DELETE /admin/v1/iam/roles/{id}", authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.DeleteRole))))

	// 授权（全量替换，差量审计）。
	mux.Handle("PUT /admin/v1/iam/roles/{id}/permissions", authMW(perm(string(iam.PermRoleGrant))(http.HandlerFunc(h.ReplacePermissions))))

	// 绑定。
	mux.Handle("GET /admin/v1/iam/bindings", authMW(perm(string(iam.PermBindingRead))(http.HandlerFunc(h.ListBindings))))
	mux.Handle("POST /admin/v1/iam/bindings", authMW(perm(string(iam.PermBindingWrite))(http.HandlerFunc(h.CreateBinding))))
	mux.Handle("DELETE /admin/v1/iam/bindings/{id}", authMW(perm(string(iam.PermBindingWrite))(http.HandlerFunc(h.DeleteBinding))))

	// 主体有效权限与模拟鉴权。
	mux.Handle("GET /admin/v1/iam/subjects/{subject_type}/{subject_id}/permissions",
		authMW(perm(string(iam.PermBindingRead))(http.HandlerFunc(h.SubjectPermissions))))
	mux.Handle("POST /admin/v1/iam/authorize", authMW(perm(string(iam.PermCheck))(http.HandlerFunc(h.Authorize))))

	// 当前主体权限：仅需认证，不额外要求权限点。
	mux.Handle("GET /admin/v1/iam/me/permissions", authMW(http.HandlerFunc(h.MePermissions)))
}

// NewRouter 注册 IAM 管理端路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm)
	return mux
}
