package platform

import (
	"encoding/json"
	"net/http"

	"github.com/admin-frame/admin-frame/pkg/health"
)

// BuildInfo 构建身份（RELS-03 / OBS-04）。
type BuildInfo struct {
	Service   string `json:"service"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

// MountHealth 注册存活/就绪探针（无需鉴权）。
//
// mux 或 h 为 nil 时直接返回，便于测试与可选依赖场景。
func MountHealth(mux *http.ServeMux, h *health.Handler) {
	if mux == nil || h == nil {
		return
	}
	mux.HandleFunc("GET /healthz", h.Liveness())
	mux.HandleFunc("GET /readyz", h.Readiness())
}

// MountVersion 暴露构建信息（service / version / commit / build_time）。
func MountVersion(mux *http.ServeMux, info BuildInfo) {
	if mux == nil {
		return
	}
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})
}

// MountMetrics 注册 Prometheus 拉取端点（GET /metrics，无需鉴权）。
// 生产须靠网络策略 / 独立 listen 限制暴露面；本函数不附加鉴权。
func MountMetrics(mux *http.ServeMux, h http.Handler) {
	if mux == nil || h == nil {
		return
	}
	mux.Handle("GET /metrics", h)
}
