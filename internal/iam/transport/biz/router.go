// Package biz 提供 B 端 IAM 自助路由（/biz/v1/iam/*）。
//
// 与 A 端共用 transport/rest 的 Handler：端别在装配期固定为 biz，
// merchant_id 只能来自 Principal（SEC-14），请求参数中的 merchant_id 被忽略。
//
// 本包刻意不注册以下路由：
//   - POST /authorize：模拟鉴权依赖 iam:check，属 A 端专属权限点；
//   - 破玻璃：平台级应急能力，B 端无对应权限点，路由不注册。
package biz

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// Register 将 B 端 IAM 自助路由挂到 mux。
//
// 权限点复用目录中的 both 权限：iam:permission:read 为 A 端专属，
// 因此权限目录查询改由 iam:role:read 守卫（角色页需要目录来渲染可选项）。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) {
	// 权限目录（只读，仅 biz / both）。
	mux.Handle("GET /biz/v1/iam/permissions", authMW(perm(string(iam.PermRoleRead))(http.HandlerFunc(h.ListPermissions))))
	mux.Handle("GET /biz/v1/iam/permissions/tree", authMW(perm(string(iam.PermRoleRead))(http.HandlerFunc(h.PermissionTree))))

	// 本商户角色生命周期。
	mux.Handle("GET /biz/v1/iam/roles", authMW(perm(string(iam.PermRoleRead))(http.HandlerFunc(h.ListRoles))))
	mux.Handle("POST /biz/v1/iam/roles", authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.CreateRole))))
	mux.Handle("GET /biz/v1/iam/roles/{id}", authMW(perm(string(iam.PermRoleRead))(http.HandlerFunc(h.GetRole))))
	mux.Handle("PATCH /biz/v1/iam/roles/{id}", authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.UpdateRole))))
	mux.Handle("POST /biz/v1/iam/roles/{id}/clone", authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.CloneRole))))
	mux.Handle("DELETE /biz/v1/iam/roles/{id}", authMW(perm(string(iam.PermRoleWrite))(http.HandlerFunc(h.DeleteRole))))

	// 授权（全量替换，差量审计）。
	mux.Handle("PUT /biz/v1/iam/roles/{id}/permissions", authMW(perm(string(iam.PermRoleGrant))(http.HandlerFunc(h.ReplacePermissions))))

	// 本商户绑定。
	mux.Handle("GET /biz/v1/iam/bindings", authMW(perm(string(iam.PermBindingRead))(http.HandlerFunc(h.ListBindings))))
	mux.Handle("POST /biz/v1/iam/bindings", authMW(perm(string(iam.PermBindingWrite))(http.HandlerFunc(h.CreateBinding))))
	mux.Handle("DELETE /biz/v1/iam/bindings/{id}", authMW(perm(string(iam.PermBindingWrite))(http.HandlerFunc(h.DeleteBinding))))

	// 本商户主体有效权限。
	mux.Handle("GET /biz/v1/iam/subjects/{subject_type}/{subject_id}/permissions",
		authMW(perm(string(iam.PermBindingRead))(http.HandlerFunc(h.SubjectPermissions))))

	// 本商户高风险变更审批单：申请与审批使用不同权限点，便于把「提申请」与
	// 「批准申请」授予不同角色（SoD）；申请人不得自审由领域层强制。
	// 列表使用 iam:change:request：审批人若要看待办，需要同时持有该权限点。
	mux.Handle("GET /biz/v1/iam/change-requests",
		authMW(perm(string(iam.PermChangeRequest))(http.HandlerFunc(h.ListChangeRequests))))
	mux.Handle("POST /biz/v1/iam/change-requests",
		authMW(perm(string(iam.PermChangeRequest))(http.HandlerFunc(h.CreateChangeRequest))))
	mux.Handle("POST /biz/v1/iam/change-requests/{id}/approve",
		authMW(perm(string(iam.PermChangeReview))(http.HandlerFunc(h.ApproveChangeRequest))))
	mux.Handle("POST /biz/v1/iam/change-requests/{id}/reject",
		authMW(perm(string(iam.PermChangeReview))(http.HandlerFunc(h.RejectChangeRequest))))
	mux.Handle("POST /biz/v1/iam/change-requests/{id}/cancel",
		authMW(perm(string(iam.PermChangeRequest))(http.HandlerFunc(h.CancelChangeRequest))))
	mux.Handle("POST /biz/v1/iam/change-requests/{id}/apply",
		authMW(perm(string(iam.PermChangeReview))(http.HandlerFunc(h.ApplyChangeRequest))))

	// 当前主体权限：仅需认证。
	mux.Handle("GET /biz/v1/iam/me/permissions", authMW(http.HandlerFunc(h.MePermissions)))
}

// NewRouter 注册 B 端 IAM 路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm)
	return mux
}
