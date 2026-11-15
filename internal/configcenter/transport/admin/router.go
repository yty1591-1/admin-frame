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

	// 路由必须以 mux.Handle("METHOD /path", ...) 字面量注册：
	// tests/contract 的静态扫描依赖该字面量比对实现与 OpenAPI 契约，
	// 换成变量或辅助函数会让契约测试判定为「死契约」。
	mux.Handle("GET /admin/v1/configs/namespaces",
		observed(observer, RouteNamespaces, authMW(read(http.HandlerFunc(h.ListNamespaces)))))

	mux.Handle("GET /admin/v1/configs/items",
		observed(observer, RouteItemsList, authMW(read(http.HandlerFunc(h.ListItems)))))
	mux.Handle("POST /admin/v1/configs/items",
		observed(observer, RouteItemsCreate, authMW(write(http.HandlerFunc(h.CreateItem)))))
	mux.Handle("GET /admin/v1/configs/items/{id}",
		observed(observer, RouteItemsGet, authMW(read(http.HandlerFunc(h.GetItem)))))
	mux.Handle("PUT /admin/v1/configs/items/{id}",
		observed(observer, RouteItemsUpdate, authMW(write(http.HandlerFunc(h.UpdateValue)))))
	mux.Handle("GET /admin/v1/configs/items/{id}/versions",
		observed(observer, RouteVersionsList, authMW(read(http.HandlerFunc(h.ListVersions)))))
	mux.Handle("GET /admin/v1/configs/items/{id}/versions/{version}",
		observed(observer, RouteVersionsGet, authMW(read(http.HandlerFunc(h.GetVersion)))))

	mux.Handle("POST /admin/v1/configs/releases",
		observed(observer, RouteReleasesCreate, authMW(write(http.HandlerFunc(h.CreateRelease)))))
	mux.Handle("GET /admin/v1/configs/releases",
		observed(observer, RouteReleasesList, authMW(read(http.HandlerFunc(h.ListReleases)))))
	mux.Handle("GET /admin/v1/configs/releases/{id}",
		observed(observer, RouteReleasesGet, authMW(read(http.HandlerFunc(h.GetRelease)))))
	mux.Handle("POST /admin/v1/configs/releases/{id}/submit",
		observed(observer, RouteReleasesSubmit, authMW(write(http.HandlerFunc(h.SubmitRelease)))))
	mux.Handle("POST /admin/v1/configs/releases/{id}/approve",
		observed(observer, RouteReleasesApprove, authMW(approve(http.HandlerFunc(h.ApproveRelease)))))
	mux.Handle("POST /admin/v1/configs/releases/{id}/publish",
		observed(observer, RouteReleasesPublish, authMW(publish(http.HandlerFunc(h.PublishRelease)))))
	mux.Handle("POST /admin/v1/configs/releases/{id}/rollback",
		observed(observer, RouteReleasesRollback, authMW(publish(http.HandlerFunc(h.RollbackRelease)))))

	secretRead := perm(string(iam.PermConfigSecretRead))
	secretWrite := perm(string(iam.PermConfigSecretWrite))
	secretApprove := perm(string(iam.PermConfigSecretApprove))
	mux.Handle("GET /admin/v1/configs/secrets",
		observed(observer, RouteSecretsList, authMW(secretRead(http.HandlerFunc(h.ListSecrets)))))
	mux.Handle("POST /admin/v1/configs/secrets",
		observed(observer, RouteSecretsCreate, authMW(secretWrite(http.HandlerFunc(h.CreateSecret)))))
	mux.Handle("GET /admin/v1/configs/secrets/{id}",
		observed(observer, RouteSecretsGet, authMW(secretRead(http.HandlerFunc(h.GetSecret)))))
	mux.Handle("POST /admin/v1/configs/secrets/{id}/submit",
		observed(observer, RouteSecretsSubmit, authMW(secretWrite(http.HandlerFunc(h.SubmitSecret)))))
	mux.Handle("POST /admin/v1/configs/secrets/{id}/approve",
		observed(observer, RouteSecretsApprove, authMW(secretApprove(http.HandlerFunc(h.ApproveSecret)))))
	mux.Handle("POST /admin/v1/configs/secrets/{id}/rotate",
		observed(observer, RouteSecretsRotate, authMW(secretWrite(http.HandlerFunc(h.RotateSecret)))))
	mux.Handle("POST /admin/v1/configs/secrets/{id}/revoke",
		observed(observer, RouteSecretsRevoke, authMW(secretWrite(http.HandlerFunc(h.RevokeSecret)))))
	mux.Handle("GET /admin/v1/configs/secrets/{id}/versions",
		observed(observer, RouteSecretsVersions, authMW(secretRead(http.HandlerFunc(h.ListSecretVersions)))))
	mux.Handle("POST /admin/v1/configs/secrets/{id}/versions/{version}/activate",
		observed(observer, RouteSecretsActivate, authMW(secretWrite(http.HandlerFunc(h.ActivateSecretVersion)))))
	mux.Handle("GET /admin/v1/configs/secrets/{id}/grants",
		observed(observer, RouteSecretsGrantsList, authMW(secretRead(http.HandlerFunc(h.ListSecretGrants)))))
	mux.Handle("POST /admin/v1/configs/secrets/{id}/grants",
		observed(observer, RouteSecretsGrantsCreate, authMW(secretWrite(http.HandlerFunc(h.CreateSecretGrant)))))
	mux.Handle("POST /admin/v1/configs/secrets/{id}/grants/{grant_id}/revoke",
		observed(observer, RouteSecretsGrantsRevoke, authMW(secretWrite(http.HandlerFunc(h.RevokeSecretGrant)))))
	mux.Handle("GET /admin/v1/configs/secrets/{id}/accesses",
		observed(observer, RouteSecretsAccesses, authMW(secretRead(http.HandlerFunc(h.ListSecretAccesses)))))
}

// 路由标签与 observability 包保持一致（低基数、稳定）。
const (
	RouteNamespaces          = "namespaces"
	RouteItemsList           = "items.list"
	RouteItemsCreate         = "items.create"
	RouteItemsGet            = "items.get"
	RouteItemsUpdate         = "items.update"
	RouteVersionsList        = "versions.list"
	RouteVersionsGet         = "versions.get"
	RouteReleasesCreate      = "releases.create"
	RouteReleasesList        = "releases.list"
	RouteReleasesGet         = "releases.get"
	RouteReleasesSubmit      = "releases.submit"
	RouteReleasesApprove     = "releases.approve"
	RouteReleasesPublish     = "releases.publish"
	RouteReleasesRollback    = "releases.rollback"
	RouteSecretsList         = "secrets.list"
	RouteSecretsCreate       = "secrets.create"
	RouteSecretsGet          = "secrets.get"
	RouteSecretsSubmit       = "secrets.submit"
	RouteSecretsApprove      = "secrets.approve"
	RouteSecretsRotate       = "secrets.rotate"
	RouteSecretsRevoke       = "secrets.revoke"
	RouteSecretsVersions     = "secrets.versions"
	RouteSecretsActivate     = "secrets.activate"
	RouteSecretsGrantsList   = "secrets.grants.list"
	RouteSecretsGrantsCreate = "secrets.grants.create"
	RouteSecretsGrantsRevoke = "secrets.grants.revoke"
	RouteSecretsAccesses     = "secrets.accesses"
)

// NewRouter 注册 A 端配置中心路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware,
	observers ...RequestObserver) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm, observers...)
	return mux
}

// observed 按需包裹观测中间件；observer 为 nil 时原样返回，行为与未接入观测一致。
func observed(observer RequestObserver, route string, next http.Handler) http.Handler {
	if observer == nil {
		return next
	}
	return observe(observer, route, next)
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

// WriteHeader 记录并转发响应状态码。
func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
