package security

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// Authorizer 把 rbac.Checker 适配为 HTTP 权限中间件工厂。
//
// Checker 由 cmd 在装配期构造（真实 IAM 或本地演示表），platform 与本包都
// 不持有角色/权限点数据，避免平台层与业务域耦合（ARCHITECTURE §5.6）。
type Authorizer struct {
	checker *rbac.Checker
}

// NewAuthorizer 包装 checker；nil 时生成默认拒绝（fail-closed）的实例。
func NewAuthorizer(checker *rbac.Checker) *Authorizer {
	return &Authorizer{checker: checker}
}

// Require 返回校验单个权限点的中间件；未认证 401，无权限 403。
//
// 返回类型与 internal/shared/auth.PermissionMiddleware 一致，便于 cmd 直接注入
// transport，而不让 transport 依赖平台层。
func (a *Authorizer) Require(permission string) func(http.Handler) http.Handler {
	var checker *rbac.Checker
	if a != nil {
		checker = a.checker
	}
	return rbac.Require(rbac.RequireConfig{
		Checker:    checker,
		Permission: rbac.Permission(permission),
		SubjectFrom: func(r *http.Request) (rbac.Subject, bool) {
			p, ok := auth.FromContext(r.Context())
			if !ok {
				return rbac.Subject{}, false
			}
			return rbac.Subject{Roles: p.Roles}, true
		},
		OnUnauthenticated: func(w http.ResponseWriter, r *http.Request) {
			httpx.FromError(w, apperr.Unauthorized("unauthorized"), httpx.TraceIDFromContext(r.Context()))
		},
		OnDenied: func(w http.ResponseWriter, r *http.Request, need rbac.Permission) {
			httpx.FromError(w, apperr.Forbidden(rbac.Denied(need).Error()), httpx.TraceIDFromContext(r.Context()))
		},
	})
}

// RequireAny 返回「拥有任一权限点即放行」的中间件。
func (a *Authorizer) RequireAny(permissions ...string) func(http.Handler) http.Handler {
	var checker *rbac.Checker
	if a != nil {
		checker = a.checker
	}
	needs := make([]rbac.Permission, 0, len(permissions))
	for _, p := range permissions {
		needs = append(needs, rbac.Permission(p))
	}
	return rbac.Require(rbac.RequireConfig{
		Checker: checker,
		Any:     needs,
		SubjectFrom: func(r *http.Request) (rbac.Subject, bool) {
			p, ok := auth.FromContext(r.Context())
			if !ok {
				return rbac.Subject{}, false
			}
			return rbac.Subject{Roles: p.Roles}, true
		},
		OnUnauthenticated: func(w http.ResponseWriter, r *http.Request) {
			httpx.FromError(w, apperr.Unauthorized("unauthorized"), httpx.TraceIDFromContext(r.Context()))
		},
		OnDenied: func(w http.ResponseWriter, r *http.Request, need rbac.Permission) {
			httpx.FromError(w, apperr.Forbidden(rbac.Denied(need).Error()), httpx.TraceIDFromContext(r.Context()))
		},
	})
}
