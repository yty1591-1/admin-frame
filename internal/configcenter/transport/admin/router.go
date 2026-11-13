// Package admin 提供 A 端配置中心管理路由（/admin/v1/configs/*）。
//
// 路由与权限点集中注册；读、写、审批、发布四权分离
// （config:read / config:write / config:approve / config:publish），
// 便于最小权限授予与职责分离。transport 只依赖 shared 契约，不 import
// internal/platform（LAYER-01）。
package admin

import (
	"net/http"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/transport/rest"
	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// RequestObserver 记录管理面请求结果；实现必须并发安全，nil 表示不观测。
type RequestObserver interface {
	ObserveAdminRequest(route string, status int, elapsed time.Duration)
}

// Register 把 A 端配置中心管理路由挂到 mux。
//
// observers 可选；传入时按路由记录请求量与耗时（配置中心指标）。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler,
	perm auth.PermissionMiddleware, observers ...RequestObserver) {
	var observer RequestObserver
	if len(observers) > 0 {
		observer = observers[0]
	}
	read := perm(string(iam.PermConfigRead))
	write := perm(string(iam.PermConfigWrite))
	approve := perm(string(iam.PermConfigApprove))
	publish := perm(string(iam.PermConfigPublish))

	handle(mux, observer, RouteNamespaces, "GET /admin/v1/configs/namespaces",
		authMW(read(http.HandlerFunc(h.ListNamespaces))))

	handle(mux, observer, RouteItemsList, "GET /admin/v1/configs/items",
		authMW(read(http.HandlerFunc(h.ListItems))))
	handle(mux, observer, RouteItemsCreate, "POST /admin/v1/configs/items",
		authMW(write(http.HandlerFunc(h.CreateItem))))
	handle(mux, observer, RouteItemsGet, "GET /admin/v1/configs/items/{id}",
		authMW(read(http.HandlerFunc(h.GetItem))))
	handle(mux, observer, RouteItemsUpdate, "PUT /admin/v1/configs/items/{id}",
		authMW(write(http.HandlerFunc(h.UpdateValue))))
	handle(mux, observer, RouteVersionsList, "GET /admin/v1/configs/items/{id}/versions",
		authMW(read(http.HandlerFunc(h.ListVersions))))
	handle(mux, observer, RouteVersionsGet, "GET /admin/v1/configs/items/{id}/versions/{version}",
		authMW(read(http.HandlerFunc(h.GetVersion))))

	handle(mux, observer, RouteReleasesCreate, "POST /admin/v1/configs/releases",
		authMW(write(http.HandlerFunc(h.CreateRelease))))
	handle(mux, observer, RouteReleasesList, "GET /admin/v1/configs/releases",
		authMW(read(http.HandlerFunc(h.ListReleases))))
	handle(mux, observer, RouteReleasesGet, "GET /admin/v1/configs/releases/{id}",
		authMW(read(http.HandlerFunc(h.GetRelease))))
	handle(mux, observer, RouteReleasesSubmit, "POST /admin/v1/configs/releases/{id}/submit",
		authMW(write(http.HandlerFunc(h.SubmitRelease))))
	handle(mux, observer, RouteReleasesApprove, "POST /admin/v1/configs/releases/{id}/approve",
		authMW(approve(http.HandlerFunc(h.ApproveRelease))))
	handle(mux, observer, RouteReleasesPublish, "POST /admin/v1/configs/releases/{id}/publish",
		authMW(publish(http.HandlerFunc(h.PublishRelease))))
	handle(mux, observer, RouteReleasesRollback, "POST /admin/v1/configs/releases/{id}/rollback",
		authMW(publish(http.HandlerFunc(h.RollbackRelease))))
}

// 路由标签与 observability 包保持一致（低基数、稳定）。
const (
	RouteNamespaces       = "namespaces"
	RouteItemsList        = "items.list"
	RouteItemsCreate      = "items.create"
	RouteItemsGet         = "items.get"
	RouteItemsUpdate      = "items.update"
	RouteVersionsList     = "versions.list"
	RouteVersionsGet      = "versions.get"
	RouteReleasesCreate   = "releases.create"
	RouteReleasesList     = "releases.list"
	RouteReleasesGet      = "releases.get"
	RouteReleasesSubmit   = "releases.submit"
	RouteReleasesApprove  = "releases.approve"
	RouteReleasesPublish  = "releases.publish"
	RouteReleasesRollback = "releases.rollback"
)

// NewRouter 注册 A 端配置中心路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware,
	observers ...RequestObserver) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm, observers...)
	return mux
}

// handle 按需包裹观测中间件后注册路由。
func handle(mux *http.ServeMux, observer RequestObserver, route, pattern string, next http.Handler) {
	if observer != nil {
		next = observe(observer, route, next)
	}
	mux.Handle(pattern, next)
}

// observe 记录请求状态码与耗时；不改变响应体。
func observe(observer RequestObserver, route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		observer.ObserveAdminRequest(route, rec.status, time.Since(start))
	})
}

// statusRecorder 捕获响应状态码，默认 200（仅 Write 未显式 WriteHeader 时）。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
