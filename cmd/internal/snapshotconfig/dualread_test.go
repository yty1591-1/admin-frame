package snapshotconfig_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/snapshotconfig"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestReportDualReadMetrics 验证差异、缺失与新增键分别落到指标。
func TestReportDualReadMetrics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.yaml")
	content := `
addr: ":8080"
shutdown_timeout: 15s
http:
  read_timeout: 5s
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	center := map[string]string{
		"server.addr":             ":9090", // 与文件不同
		"server.shutdown_timeout": "15s",   // 一致
		"log.level":               "debug", // 中心新增
	}
	registry := prometheus.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := snapshotconfig.Report(context.Background(), path, "dev",
		startupFor(map[string]map[string]string{"platform": center}), logger, registry); err != nil {
		t.Fatalf("Report: %v", err)
	}

	families := gather(t, registry)
	// server.addr 值差异 + http.read_timeout 中心缺失 = 2 个阻断项。
	if got := sumGauge(t, families, "config_dual_read_mismatch"); got != 2 {
		t.Fatalf("mismatch = %v, want 2", got)
	}
	if got := sumGauge(t, families, "config_dual_read_degraded"); got != 0 {
		t.Fatalf("degraded = %v, want 0", got)
	}
}

// TestReportWithoutCenter 验证未接入配置中心时不打点、不报错。
func TestReportWithoutCenter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.yaml")
	if err := os.WriteFile(path, []byte("server:\n  addr: \":8080\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	registry := prometheus.NewRegistry()
	if err := snapshotconfig.Report(context.Background(), path, "dev", nil, nil, registry); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(gather(t, registry)) != 0 {
		t.Fatal("dual-read metrics must stay empty when config center is not configured")
	}
}

// TestReportMissingFileIsSkipped 验证无文件启动时不因缺少基线而失败。
func TestReportMissingFileIsSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	center := map[string]string{"server.addr": ":9090"}
	if err := snapshotconfig.Report(context.Background(), path, "dev",
		startupFor(map[string]map[string]string{"platform": center}),
		nil, prometheus.NewRegistry()); err != nil {
		t.Fatalf("Report(missing file) error = %v, want nil", err)
	}
}

// TestReportCoversMultipleNamespaces 验证每个启用 namespace 独立对比。
func TestReportCoversMultipleNamespaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.yaml")
	content := `
addr: ":8080"
identity:
  enabled: true
  access_ttl: 15m
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	registry := prometheus.NewRegistry()
	err := snapshotconfig.Report(context.Background(), path, "dev", startupFor(
		map[string]map[string]string{
			"platform": {"server.addr": ":8080"},
			"identity": {
				"identity.enabled":    "false", // 与文件不同
				"identity.access_ttl": "15m",   // 一致
			},
		}), nil, registry)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if got := sumGauge(t, gather(t, registry), "config_dual_read_mismatch"); got != 1 {
		t.Fatalf("mismatch = %v, want 1", got)
	}
}

// startupFor 构造测试用启动快照集合。
func startupFor(byNamespace map[string]map[string]string) *snapshotconfig.Startup {
	values := make(map[string]string)
	namespaces := make([]string, 0, len(byNamespace))
	for namespace, entries := range byNamespace {
		namespaces = append(namespaces, namespace)
		for key, value := range entries {
			values[key] = value
		}
	}
	return &snapshotconfig.Startup{
		Values: values, ByNamespace: byNamespace, Namespaces: namespaces, Env: "dev",
	}
}

func gather(t *testing.T, registry *prometheus.Registry) map[string]*dto.MetricFamily {
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

func sumGauge(t *testing.T, families map[string]*dto.MetricFamily, name string) float64 {
	t.Helper()
	family, ok := families[name]
	if !ok {
		t.Fatalf("metric %s not registered", name)
	}
	var total float64
	for _, metric := range family.GetMetric() {
		if metric.Gauge != nil {
			total += metric.Gauge.GetValue()
		}
	}
	return total
}
