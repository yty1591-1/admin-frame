package observability_test

import (
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/observability"
	"github.com/admin-frame/admin-frame/pkg/configclient"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestMetricsExposeM1Series 验证 M1 指标族注册与关键计数路径。
func TestMetricsExposeM1Series(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := observability.New(registry)
	if err != nil {
		t.Fatalf("new metrics: %v", err)
	}

	metrics.ObserveAdminRequest(observability.RouteReleasesPublish, 200, 10*time.Millisecond)
	metrics.ObserveAdminRequest(observability.RouteReleasesPublish, 409, 5*time.Millisecond)
	metrics.SetPendingApproval("platform", 2)

	now := time.Now().UTC()
	metrics.ObserveClientRefresh(configclient.RefreshEvent{
		At:     now,
		Loaded: true,
		Snapshot: configclient.Snapshot{
			Namespace: "platform",
			ReleaseNo: 7,
		},
	})
	metrics.ObserveClientRefresh(configclient.RefreshEvent{
		At:       now.Add(6 * time.Minute),
		Loaded:   true,
		Degraded: true,
		Snapshot: configclient.Snapshot{
			Namespace: "platform",
			ReleaseNo: 7,
		},
		Err: configclient.ErrSnapshotHashMismatch,
	})
	metrics.ClientColdStartDegraded()
	metrics.ClientWatchDropped()

	families := gatherFamilies(t, registry)
	assertMetric(t, families, "config_admin_request_total", 2)
	assertMetric(t, families, "config_release_publish_total", 2)
	assertMetric(t, families, "config_release_pending_approval", 2)
	assertMetric(t, families, "config_client_fetch_total", 2)
	assertMetric(t, families, "config_client_snapshot_age_seconds", 360)
	assertMetric(t, families, "config_client_version", 7)
	assertMetric(t, families, "config_client_snapshot_corrupt_total", 1)
	assertMetric(t, families, "config_client_cold_start_degraded_total", 1)
	assertMetric(t, families, "config_client_watch_dropped_total", 1)
}

// TestNewRequiresRegisterer 验证缺少 Registry 时装配失败，避免观测静默缺失。
func TestNewRequiresRegisterer(t *testing.T) {
	if _, err := observability.New(nil); err == nil {
		t.Fatal("New(nil) error = nil, want error")
	}
}

// gatherFamilies 汇总全部指标族。
func gatherFamilies(t *testing.T, registry *prometheus.Registry) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	index := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		index[family.GetName()] = family
	}
	return index
}

// assertMetric 断言指标存在，且各序列取值之和等于 want。
func assertMetric(t *testing.T, families map[string]*dto.MetricFamily, name string, want float64) {
	t.Helper()
	family, ok := families[name]
	if !ok {
		t.Fatalf("metric %s not registered", name)
	}
	var got float64
	for _, metric := range family.GetMetric() {
		switch {
		case metric.Counter != nil:
			got += metric.Counter.GetValue()
		case metric.Gauge != nil:
			got += metric.Gauge.GetValue()
		}
	}
	if got != want {
		t.Fatalf("metric %s = %v, want %v", name, got, want)
	}
}
