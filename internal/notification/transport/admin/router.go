// Package admin 提供 A 端通知管理路由（/admin/v1/notifications*、模板）。
//
// 路由与权限点集中注册；读与写分离（notification:read / notification:write）。
// transport 只依赖 shared 契约，不 import internal/platform（LAYER-01）。
package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/notification/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// Register 把 A 端通知管理路由挂到 mux。
func Register(mux *http.ServeMux, h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) {
	read := perm(string(iam.PermNotificationRead))
	write := perm(string(iam.PermNotificationWrite))

	// 通知单查询：tenant_id 查询参数缺省为平台哨兵（平台级运维视图）；
	// 指定 tenant_id 即跨租户查询（读权限 + 后续 ENT-12 数据范围契约约束）。
	mux.Handle("GET /admin/v1/notifications", authMW(read(http.HandlerFunc(h.List))))
	mux.Handle("GET /admin/v1/notifications/{id}", authMW(read(http.HandlerFunc(h.Get))))
	mux.Handle("GET /admin/v1/notifications/{id}/deliveries", authMW(read(http.HandlerFunc(h.Deliveries))))

	// 写动作：代发 / DLQ 补偿均入审计（notification.send / notification.dlq.replay）。
	mux.Handle("POST /admin/v1/notifications", authMW(write(http.HandlerFunc(h.Send))))
	mux.Handle("POST /admin/v1/notifications/{id}/replay", authMW(write(http.HandlerFunc(h.Replay))))

	// 模板管理：内容变更即新版本（渲染按版本锁定）。
	mux.Handle("GET /admin/v1/notification-templates", authMW(read(http.HandlerFunc(h.ListTemplates))))
	mux.Handle("POST /admin/v1/notification-templates", authMW(write(http.HandlerFunc(h.UpsertTemplate))))
	mux.Handle("PUT /admin/v1/notification-templates/status", authMW(write(http.HandlerFunc(h.SetTemplateEnabled))))
}

// NewRouter 注册 A 端通知路由并挂接鉴权与权限中间件（测试用）。
func NewRouter(h *rest.Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm)
	return mux
}
