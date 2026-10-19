package rbac

import "github.com/admin-frame/admin-frame/pkg/errors"

// Denied 构造权限不足错误（语义码 permission_denied → 40300）。
func Denied(perm Permission) *errors.Error {
	p := perm.Normalize()
	if p == "" {
		return errors.PermissionDenied("permission denied")
	}
	return errors.PermissionDenied("permission denied: " + string(p))
}

// ErrUnauthenticated 未认证（中间件无主体时使用）。
func ErrUnauthenticated() *errors.Error {
	return errors.Unauthenticated("unauthenticated")
}
