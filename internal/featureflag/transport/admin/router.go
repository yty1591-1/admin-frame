// Package admin 提供 A 端 featureflag 管理路由（/admin/v1/feature-flags*）。
//
// 路由与权限点集中注册；读与写分离（featureflag:read / featureflag:write）。
// transport 只依赖 shared 契约，不 import internal/platform（LAYER-01）。
package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/featureflag/transport/rest"
	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// Register 把 A 端 flag 管理路由挂到 mux。
//
// flag 是平台级治理资源（kill switch / 灰度配比），不接租户数据范围
// （ENT-12 语义不适用）；越权面由权限点与 featureflag.* 审计承担。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) {
	read := perm(string(iam.PermFeatureFlagRead))
	write := perm(string(iam.PermFeatureFlagWrite))

	mux.Handle("GET /admin/v1/feature-flags", authMW(read(http.HandlerFunc(h.List))))
	mux.Handle("POST /admin/v1/feature-flags", authMW(write(http.HandlerFunc(h.Create))))
	mux.Handle("GET /admin/v1/feature-flags/{key}", authMW(read(http.HandlerFunc(h.Get))))
	mux.Handle("PATCH /admin/v1/feature-flags/{key}", authMW(write(http.HandlerFunc(h.Update))))
	mux.Handle("POST /admin/v1/feature-flags/{key}/status", authMW(write(http.HandlerFunc(h.SetStatus))))
	mux.Handle("DELETE /admin/v1/feature-flags/{key}", authMW(write(http.HandlerFunc(h.Delete))))
}

// NewRouter 注册 A 端 featureflag 路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm)
	return mux
}
