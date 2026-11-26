package scim

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
)

// Register 把 SCIM 2.0 路由挂到 mux。
//
// 认证：Bearer scim_ 令牌；账号访问令牌无法调用（反之亦然，见 AC-P16-SCIM-6）。
// 每个端点都走统一的认证 + 限流 + 观测包装，避免漏挂。
//
// 路径一律写字面量而不是常量拼接：tests/contract 的漂移检测静态扫描
// `mux.Handle("METHOD /path", ...)` 字面量，拼接会让这些端点对契约对账
// 不可见，进而被判定为「实现有、契约无」或反之。前缀固定为 /admin/scim/v2，
// 不随 channel 变化（§21.1.2）；meta.location 的基址由 scimBaseURL 推导。
func Register(mux *http.ServeMux, h *Handler) {
	if mux == nil || h == nil {
		return
	}

	// 发现端点：只读，供 IdP 探测能力。
	mux.Handle("GET /admin/scim/v2/ServiceProviderConfig",
		h.wrap("ServiceProviderConfig", "GET", h.ServiceProviderConfig))
	mux.Handle("GET /admin/scim/v2/ResourceTypes",
		h.wrap("ResourceTypes", "GET", h.ResourceTypes))
	mux.Handle("GET /admin/scim/v2/ResourceTypes/{id}",
		h.wrap("ResourceTypes", "GET", h.ResourceTypeByID))
	mux.Handle("GET /admin/scim/v2/Schemas",
		h.wrap("Schemas", "GET", h.Schemas))
	mux.Handle("GET /admin/scim/v2/Schemas/{id}",
		h.wrap("Schemas", "GET", h.SchemaByID))

	mux.Handle("GET /admin/scim/v2/Users", h.wrap("User", "GET", h.ListUsers))
	mux.Handle("POST /admin/scim/v2/Users", h.wrap("User", "POST", h.CreateUser))
	mux.Handle("GET /admin/scim/v2/Users/{id}", h.wrap("User", "GET", h.GetUser))
	mux.Handle("PUT /admin/scim/v2/Users/{id}", h.wrap("User", "PUT", h.ReplaceUser))
	mux.Handle("PATCH /admin/scim/v2/Users/{id}", h.wrap("User", "PATCH", h.PatchUser))
	mux.Handle("DELETE /admin/scim/v2/Users/{id}", h.wrap("User", "DELETE", h.DeleteUser))

	mux.Handle("GET /admin/scim/v2/Groups", h.wrap("Group", "GET", h.ListGroups))
	mux.Handle("POST /admin/scim/v2/Groups", h.wrap("Group", "POST", h.CreateGroup))
	mux.Handle("GET /admin/scim/v2/Groups/{id}", h.wrap("Group", "GET", h.GetGroup))
	mux.Handle("PUT /admin/scim/v2/Groups/{id}", h.wrap("Group", "PUT", h.ReplaceGroup))
	mux.Handle("PATCH /admin/scim/v2/Groups/{id}", h.wrap("Group", "PATCH", h.PatchGroup))
	mux.Handle("DELETE /admin/scim/v2/Groups/{id}", h.wrap("Group", "DELETE", h.DeleteGroup))

	// /Bulk 明确不支持：返回 501 notImplemented，避免客户端反复试探（§21.1.1）。
	mux.Handle("POST /admin/scim/v2/Bulk", h.wrap("Bulk", "POST", h.BulkNotImplemented))
}

// BulkNotImplemented 返回 501 notImplemented。
func (h *Handler) BulkNotImplemented(w http.ResponseWriter, _ *http.Request) {
	writeError(w, &scimUnavailableError)
}

// scimUnavailableError 是 /Bulk 的固定错误体；501 不属于 RFC 7644 §3.12 的 scimType 枚举。
var scimUnavailableError = app.ScimError{
	Status: http.StatusNotImplemented,
	Detail: "bulk operations are not supported",
}

// apirespTraceID 返回请求链路 ID，供用例写入审计。
func apirespTraceID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return apiresp.TraceIDFromContext(r.Context())
}
