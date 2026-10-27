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

	// 角色模板：平台定义基线，B 端按商户实例化；同步为高风险显式收敛动作。
	mux.Handle("GET /admin/v1/iam/role-templates",
		authMW(perm(string(iam.PermRoleRead))(http.HandlerFunc(h.ListRoleTemplates))))
	mux.Handle("POST /admin/v1/iam/role-templates",
		authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.CreateRoleTemplate))))
	mux.Handle("GET /admin/v1/iam/role-templates/{id}",
		authMW(perm(string(iam.PermRoleRead))(http.HandlerFunc(h.GetRoleTemplate))))
	mux.Handle("PATCH /admin/v1/iam/role-templates/{id}",
		authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.UpdateRoleTemplate))))
	mux.Handle("PUT /admin/v1/iam/role-templates/{id}/permissions",
		authMW(perm(string(iam.PermRoleGrant))(http.HandlerFunc(h.ReplaceRoleTemplatePermissions))))
	mux.Handle("DELETE /admin/v1/iam/role-templates/{id}",
		authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.DeleteRoleTemplate))))
	mux.Handle("GET /admin/v1/iam/role-templates/{id}/drift",
		authMW(perm(string(iam.PermRoleRead))(http.HandlerFunc(h.GetTemplateDrift))))
	mux.Handle("POST /admin/v1/iam/role-templates/{id}/sync",
		authMW(perm(string(iam.PermRoleGrant))(http.HandlerFunc(h.SyncRoleTemplate))))
	mux.Handle("POST /admin/v1/iam/role-templates/{id}/instantiate",
		authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.InstantiateRoleTemplate))))

	// 绑定。
	mux.Handle("GET /admin/v1/iam/bindings", authMW(perm(string(iam.PermBindingRead))(http.HandlerFunc(h.ListBindings))))
	mux.Handle("POST /admin/v1/iam/bindings", authMW(perm(string(iam.PermBindingWrite))(http.HandlerFunc(h.CreateBinding))))
	mux.Handle("DELETE /admin/v1/iam/bindings/{id}", authMW(perm(string(iam.PermBindingWrite))(http.HandlerFunc(h.DeleteBinding))))

	// 主体有效权限与模拟鉴权。
	mux.Handle("GET /admin/v1/iam/subjects/{subject_type}/{subject_id}/permissions",
		authMW(perm(string(iam.PermBindingRead))(http.HandlerFunc(h.SubjectPermissions))))
	mux.Handle("POST /admin/v1/iam/authorize", authMW(perm(string(iam.PermCheck))(http.HandlerFunc(h.Authorize))))

	// 高风险变更审批单：申请 / 通过 / 拒绝 / 撤销 / 应用。
	// 申请需要写权限，决定与查询分别使用独立的 iam:change:* 权限点，便于把
	// 「能提申请」与「能批准申请」授予不同角色（SoD）。
	mux.Handle("GET /admin/v1/iam/change-requests",
		authMW(perm(string(iam.PermChangeRead))(http.HandlerFunc(h.ListChangeRequests))))
	mux.Handle("POST /admin/v1/iam/change-requests",
		authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.CreateChangeRequest))))
	mux.Handle("POST /admin/v1/iam/change-requests/{id}/approve",
		authMW(perm(string(iam.PermChangeApprove))(http.HandlerFunc(h.ApproveChangeRequest))))
	mux.Handle("POST /admin/v1/iam/change-requests/{id}/reject",
		authMW(perm(string(iam.PermChangeApprove))(http.HandlerFunc(h.RejectChangeRequest))))
	mux.Handle("POST /admin/v1/iam/change-requests/{id}/cancel",
		authMW(perm(string(iam.PermChangeRead))(http.HandlerFunc(h.CancelChangeRequest))))
	mux.Handle("POST /admin/v1/iam/change-requests/{id}/apply",
		authMW(perm(string(iam.PermChangeApprove))(http.HandlerFunc(h.ApplyChangeRequest))))

	// 破玻璃（限时超级权限）：申请与审批必须是不同主体（SoD），
	// 撤销使用审批权限——能批准超级权限的人才应能提前收回。
	mux.Handle("POST /admin/v1/iam/breakglass",
		authMW(perm(string(iam.PermBreakglassRequest))(http.HandlerFunc(h.RequestBreakglass))))
	mux.Handle("POST /admin/v1/iam/breakglass/{id}/approve",
		authMW(perm(string(iam.PermBreakglassApprove))(http.HandlerFunc(h.ApproveBreakglass))))
	mux.Handle("POST /admin/v1/iam/breakglass/{id}/revoke",
		authMW(perm(string(iam.PermBreakglassApprove))(http.HandlerFunc(h.RevokeBreakglass))))

	// 当前主体权限：仅需认证，不额外要求权限点。
	mux.Handle("GET /admin/v1/iam/me/permissions", authMW(http.HandlerFunc(h.MePermissions)))
}

// NewRouter 注册 IAM 管理端路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm)
	return mux
}
