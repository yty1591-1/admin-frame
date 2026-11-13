package observability_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/observability"

	"github.com/prometheus/client_golang/prometheus"
)

// TestDualReadMetricsExposeGauges 验证双读差异与降级指标可打点。
func TestDualReadMetricsExposeGauges(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := observability.NewDualRead(registry)
	if err != nil {
		t.Fatalf("new dual-read metrics: %v", err)
	}
	metrics.SetMismatch("platform", "server.addr", true)
	metrics.SetMismatch("platform", "log.level", false)
	metrics.SetDegraded("platform", true)

	families := gatherFamilies(t, registry)
	assertMetric(t, families, "config_dual_read_mismatch", 1)
	assertMetric(t, families, "config_dual_read_degraded", 1)
}

// TestNewDualReadRequiresRegisterer 验证缺少 Registry 时装配失败。
func TestNewDualReadRequiresRegisterer(t *testing.T) {
	if _, err := observability.NewDualRead(nil); err == nil {
		t.Fatal("NewDualRead(nil) error = nil, want error")
	}
}
