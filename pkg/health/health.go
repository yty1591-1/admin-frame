package health

import (
	"encoding/json"
	"net/http"
	"sync"
)

// Checker 执行单项就绪探测；返回非 nil 表示该依赖未就绪。
type Checker func() error

// Handler 聚合存活与就绪 HTTP 探针。
type Handler struct {
	mu        sync.RWMutex
	readiness []namedChecker
}

type namedChecker struct {
	name string
	fn   Checker
}

// New 创建空的探针 Handler，需再 Register 就绪检查项。
func New() *Handler {
	return &Handler{}
}

// Register 注册命名就绪检查；并发安全。
func (h *Handler) Register(name string, fn Checker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.readiness = append(h.readiness, namedChecker{name: name, fn: fn})
}

// Liveness 返回存活探针：进程能响应即 200，不检查依赖。
func (h *Handler) Liveness() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

// Readiness 返回就绪探针：执行全部 Checker，任一失败则 503。
func (h *Handler) Readiness() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		h.mu.RLock()
		checks := append([]namedChecker(nil), h.readiness...)
		h.mu.RUnlock()

		type item struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}
		out := make([]item, 0, len(checks))
		ok := true
		for _, c := range checks {
			it := item{Name: c.name, Status: "ok"}
			if err := c.fn(); err != nil {
				ok = false
				it.Status = "fail"
				it.Error = err.Error()
			}
			out = append(out, it)
		}
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		// 客户端断开时 Encode 失败可忽略。
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": map[bool]string{true: "ok", false: "fail"}[ok],
			"checks": out,
		})
	}
}
