package admin

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/files"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// Register 将文件路由挂到 mux（A 端）。
//
// perm 由 cmd 注入，transport 不依赖平台层（ARCHITECTURE §6.1）。
func Register(mux *http.ServeMux, h *Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) {
	mux.Handle("POST /admin/v1/files/uploads/presign", authMW(perm(files.PermWrite)(http.HandlerFunc(h.PresignUpload))))
	mux.Handle("POST /admin/v1/files", authMW(perm(files.PermWrite)(http.HandlerFunc(h.Upload))))
	mux.Handle("GET /admin/v1/files/downloads/presign", authMW(perm(files.PermRead)(http.HandlerFunc(h.PresignDownload))))
	mux.Handle("GET /admin/v1/files/object", authMW(perm(files.PermRead)(http.HandlerFunc(h.Download))))
}

// NewRouter 独立 mux（测试用）。
func NewRouter(h *Handler, authMW func(http.Handler) http.Handler, perm auth.PermissionMiddleware) http.Handler {
	mux := http.NewServeMux()
	Register(mux, h, authMW, perm)
	return mux
}
