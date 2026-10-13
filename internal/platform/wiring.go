package platform

import (
	"net/http"

	"github.com/admin-frame/admin-frame/pkg/health"
)

// MountHealth 注册存活/就绪探针（无需鉴权）。
func MountHealth(mux *http.ServeMux, h *health.Handler) {
	mux.HandleFunc("GET /healthz", h.Liveness())
	mux.HandleFunc("GET /readyz", h.Readiness())
}

// MountVersion 暴露构建信息占位。
func MountVersion(mux *http.ServeMux, service, version string) {
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service":"` + service + `","version":"` + version + `"}`)) // 忽略短响应写失败
	})
}
