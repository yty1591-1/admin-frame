package rbac

import "net/http"

// RequireConfig 配置权限中间件。
type RequireConfig struct {
	// Checker 必填。
	Checker *Checker
	// Permission 单个必填权限点。
	Permission Permission
	// Any 非空时与 Permission 二选一语义：拥有任一即可（优先于 Permission）。
	Any []Permission
	// All 非空时须全部满足（优先于 Any / Permission）。
	All []Permission
	// SubjectFrom 从请求提取主体；返回 ok=false 视为未认证。
	SubjectFrom func(*http.Request) (Subject, bool)
	// OnUnauthenticated 未认证响应；nil 时 401 纯文本。
	OnUnauthenticated func(http.ResponseWriter, *http.Request)
	// OnDenied 无权限响应；nil 时 403 纯文本。
	OnDenied func(http.ResponseWriter, *http.Request, Permission)
}

// Require 在 AuthN 之后校验权限点（默认拒绝）。
func Require(cfg RequireConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.Checker == nil {
				writeDenied(cfg, w, r, cfg.Permission)
				return
			}
			if cfg.SubjectFrom == nil {
				writeUnauth(cfg, w, r)
				return
			}
			sub, ok := cfg.SubjectFrom(r)
			if !ok {
				writeUnauth(cfg, w, r)
				return
			}

			switch {
			case len(cfg.All) > 0:
				if !cfg.Checker.AllowAll(sub, cfg.All...) {
					writeDenied(cfg, w, r, firstPerm(cfg.All, cfg.Permission))
					return
				}
			case len(cfg.Any) > 0:
				if !cfg.Checker.AllowAny(sub, cfg.Any...) {
					writeDenied(cfg, w, r, firstPerm(cfg.Any, cfg.Permission))
					return
				}
			default:
				if !cfg.Permission.Valid() || !cfg.Checker.Allow(sub, cfg.Permission) {
					writeDenied(cfg, w, r, cfg.Permission)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func firstPerm(list []Permission, fallback Permission) Permission {
	if len(list) > 0 {
		return list[0]
	}
	return fallback
}

func writeUnauth(cfg RequireConfig, w http.ResponseWriter, r *http.Request) {
	if cfg.OnUnauthenticated != nil {
		cfg.OnUnauthenticated(w, r)
		return
	}
	http.Error(w, "unauthenticated", http.StatusUnauthorized)
}

func writeDenied(cfg RequireConfig, w http.ResponseWriter, r *http.Request, perm Permission) {
	if cfg.OnDenied != nil {
		cfg.OnDenied(w, r, perm)
		return
	}
	http.Error(w, Denied(perm).Error(), http.StatusForbidden)
}
