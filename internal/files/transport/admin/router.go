package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// Register 将文件路由挂到 mux（A 端）。
func Register(mux *http.ServeMux, h *Handler, authMW func(http.Handler) http.Handler) {
	mux.Handle("POST /admin/v1/files/uploads/presign", authMW(platform.RequirePermission("file:write")(http.HandlerFunc(h.PresignUpload))))
	mux.Handle("POST /admin/v1/files", authMW(platform.RequirePermission("file:write")(http.HandlerFunc(h.Upload))))
	mux.Handle("GET /admin/v1/files/downloads/presign", authMW(platform.RequirePermission("file:read")(http.HandlerFunc(h.PresignDownload))))
	mux.Handle("GET /admin/v1/files/object", authMW(platform.RequirePermission("file:read")(http.HandlerFunc(h.Download))))
}

// NewRouter 独立 mux（测试用）。
func NewRouter(h *Handler, authMW func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW)
	return mux
}
