package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/metrics"
)

func TestCustomRouteLabel(t *testing.T) {
	m, err := metrics.New(metrics.Options{
		Service:         "admin-api",
		RegisterRuntime: metrics.Bool(false),
		RegisterProcess: metrics.Bool(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := m.HTTP().Middleware(
		metrics.WithRouteLabel(func(*http.Request) string { return "/fixed" }),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/anything?x=1", http.NoBody))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d", rr.Code)
	}

	families, err := m.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, fam := range families {
		if fam.GetName() != "http_server_requests_total" {
			continue
		}
		for _, metric := range fam.Metric {
			labels := map[string]string{}
			for _, lp := range metric.Label {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["route"] == "/fixed" && labels["code"] == "201" && labels["method"] == http.MethodPost {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected labeled request metric")
	}
}

func TestNormalizePatternViaServeMux(t *testing.T) {
	m, err := metrics.New(metrics.Options{
		Service:         "admin-api",
		RegisterRuntime: metrics.Bool(false),
		RegisterProcess: metrics.Bool(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/orders/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := m.HTTP().Middleware()(mux)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admin/v1/orders/42", http.NoBody))

	families, err := m.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, fam := range families {
		if fam.GetName() != "http_server_requests_total" {
			continue
		}
		for _, metric := range fam.Metric {
			for _, lp := range metric.Label {
				if lp.GetName() == "route" && lp.GetValue() == "/admin/v1/orders/{id}" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("expected pattern route label")
	}
}
