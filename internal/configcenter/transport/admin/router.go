// Package admin 提供 A 端配置中心管理路由（/admin/v1/configs/*）。
//
// 路由与权限点集中注册；读、写、审批、发布四权分离
// （config:read / config:write / config:approve / config:publish），
// 便于最小权限授予与职责分离。transport 只依赖 shared 契约，不 import
// internal/platform（LAYER-01）。
package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/configcenter/transport/rest"
	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// Register 把 A 端配置中心管理路由挂到 mux。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) {
	read := perm(string(iam.PermConfigRead))
	write := perm(string(iam.PermConfigWrite))
	approve := perm(string(iam.PermConfigApprove))
	publish := perm(string(iam.PermConfigPublish))

	mux.Handle("GET /admin/v1/configs/namespaces", authMW(read(http.HandlerFunc(h.ListNamespaces))))

	mux.Handle("GET /admin/v1/configs/items", authMW(read(http.HandlerFunc(h.ListItems))))
	mux.Handle("POST /admin/v1/configs/items", authMW(write(http.HandlerFunc(h.CreateItem))))
	mux.Handle("GET /admin/v1/configs/items/{id}", authMW(read(http.HandlerFunc(h.GetItem))))
	mux.Handle("PUT /admin/v1/configs/items/{id}", authMW(write(http.HandlerFunc(h.UpdateValue))))
	mux.Handle("GET /admin/v1/configs/items/{id}/versions", authMW(read(http.HandlerFunc(h.ListVersions))))
	mux.Handle("GET /admin/v1/configs/items/{id}/versions/{version}", authMW(read(http.HandlerFunc(h.GetVersion))))

	mux.Handle("POST /admin/v1/configs/releases", authMW(write(http.HandlerFunc(h.CreateRelease))))
	mux.Handle("GET /admin/v1/configs/releases", authMW(read(http.HandlerFunc(h.ListReleases))))
	mux.Handle("GET /admin/v1/configs/releases/{id}", authMW(read(http.HandlerFunc(h.GetRelease))))
	mux.Handle("POST /admin/v1/configs/releases/{id}/submit", authMW(write(http.HandlerFunc(h.SubmitRelease))))
	mux.Handle("POST /admin/v1/configs/releases/{id}/approve", authMW(approve(http.HandlerFunc(h.ApproveRelease))))
	mux.Handle("POST /admin/v1/configs/releases/{id}/publish", authMW(publish(http.HandlerFunc(h.PublishRelease))))
	mux.Handle("POST /admin/v1/configs/releases/{id}/rollback", authMW(publish(http.HandlerFunc(h.RollbackRelease))))
}

// NewRouter 注册 A 端配置中心路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm)
	return mux
}
