package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/pkg/metrics"
)

func TestNewRequiresService(t *testing.T) {
	_, err := metrics.New(metrics.Options{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHandlerExposesConstLabelsAndHTTP(t *testing.T) {
	m, err := metrics.New(metrics.Options{
		Service:         "admin-api",
		Channel:         "admin",
		Env:             "test",
		RegisterRuntime: metrics.Bool(false),
		RegisterProcess: metrics.Bool(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterBuildInfo("1.0.0", "abc", "2026-01-01"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/orders", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	h := m.HTTP().Middleware()(mux)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/orders", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}

	scrape := httptest.NewRecorder()
	m.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body, _ := io.ReadAll(scrape.Body)
	text := string(body)

	for _, want := range []string{
		`service="admin-api"`,
		`channel="admin"`,
		`env="test"`,
		"http_server_requests_total",
		"http_server_request_duration_seconds",
		"build_info",
		`version="1.0.0"`,
		`/admin/v1/orders`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics body missing %q\n%s", want, text)
		}
	}
}

func TestMiddlewareSkipsProbes(t *testing.T) {
	m, err := metrics.New(metrics.Options{
		Service:         "c-api",
		RegisterRuntime: metrics.Bool(false),
		RegisterProcess: metrics.Bool(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := m.HTTP().Middleware()(mux)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody))

	mf, err := m.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range mf {
		if fam.GetName() == "http_server_requests_total" && len(fam.Metric) > 0 {
			t.Fatalf("probe should not increment requests: %+v", fam)
		}
	}
}

func TestFactoryCounter(t *testing.T) {
	m, err := metrics.New(metrics.Options{
		Namespace:       "af",
		Service:         "worker",
		Channel:         "worker",
		RegisterRuntime: metrics.Bool(false),
		RegisterProcess: metrics.Bool(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.NewCounter("jobs_total", "jobs processed", "result")
	if err != nil {
		t.Fatal(err)
	}
	c.WithLabelValues("ok").Inc()

	scrape := httptest.NewRecorder()
	m.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body, _ := io.ReadAll(scrape.Body)
	if !strings.Contains(string(body), "af_jobs_total") {
		t.Fatalf("body=%s", body)
	}
}

func TestExternalRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := metrics.New(metrics.Options{
		Service:         "biz-api",
		Registerer:      reg,
		Gatherer:        reg,
		RegisterRuntime: metrics.Bool(false),
		RegisterProcess: metrics.Bool(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Gatherer() != reg {
		t.Fatal("gatherer mismatch")
	}
}

func TestSanitizeNamespaceViaMetricName(t *testing.T) {
	m, err := metrics.New(metrics.Options{
		Namespace:       "my-app.v1",
		Service:         "s",
		RegisterRuntime: metrics.Bool(false),
		RegisterProcess: metrics.Bool(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.NewCounter("custom_total", "custom")
	if err != nil {
		t.Fatal(err)
	}
	c.WithLabelValues().Inc()

	scrape := httptest.NewRecorder()
	m.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body, _ := io.ReadAll(scrape.Body)
	if !strings.Contains(string(body), "my_app_v1_custom_total") {
		t.Fatalf("expected sanitized namespace in body:\n%s", body)
	}
}
