package platform_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/pkg/health"
)

// TestMountHealthRoutes 验证存活/就绪探针语义（OBS-02）。
func TestMountHealthRoutes(t *testing.T) {
	hh := health.New()
	hh.Register("ok", func() error { return nil })
	mux := http.NewServeMux()
	httpserver.MountHealth(mux, hh)
	httpserver.MountVersion(mux, httpserver.BuildInfo{Service: "c-api", Version: "v1"})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("version status=%d", rec.Code)
	}
	var info httpserver.BuildInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Service != "c-api" || info.Version != "v1" {
		t.Fatalf("info=%+v", info)
	}
}

// TestMountReadinessFails 验证依赖失败时 readyz 返回 503。
func TestMountReadinessFails(t *testing.T) {
	hh := health.New()
	hh.Register("db", func() error { return errors.New("down") })
	mux := http.NewServeMux()
	httpserver.MountHealth(mux, hh)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "down") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

// TestMountNilSafe 验证 nil mux / nil handler 不 panic（AC-PLAT-08-3）。
func TestMountNilSafe(_ *testing.T) {
	httpserver.MountHealth(nil, nil)
	httpserver.MountVersion(nil, httpserver.BuildInfo{})
	httpserver.MountMetrics(nil, nil)
	mux := http.NewServeMux()
	httpserver.MountHealth(mux, nil)
	httpserver.MountMetrics(mux, nil)
}

// TestMountMetrics 验证 /metrics 端点挂载。
func TestMountMetrics(t *testing.T) {
	mux := http.NewServeMux()
	httpserver.MountMetrics(mux, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("metrics"))
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	if rec.Code != http.StatusOK || rec.Body.String() != "metrics" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
