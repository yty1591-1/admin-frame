package snapshotconfig_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/snapshotconfig"
	"github.com/admin-frame/admin-frame/pkg/configclient"
	pkgmetrics "github.com/admin-frame/admin-frame/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

// countingReader 记录采样次数，用于证明周期循环确实多次执行。
type countingReader struct {
	mu        sync.Mutex
	calls     int
	namespace []string
	snapshots map[string]configclient.Snapshot
}

func (r *countingReader) Namespaces() []string { return r.namespace }

func (r *countingReader) SnapshotFor(namespace string) (configclient.Snapshot, bool) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	snapshot, ok := r.snapshots[namespace]
	return snapshot, ok
}

func (r *countingReader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// TestRunPeriodicSamplesAndDeduplicatesLogs 验证周期采样多次执行、指标持续刷新、
// 而同一差异只记一条日志（避免 30s 周期把差异刷成噪音）。
func TestRunPeriodicSamplesAndDeduplicatesLogs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.yaml")
	if err := os.WriteFile(path, []byte("addr: \":8080\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	registry := prometheus.NewRegistry()
	disabled := false
	metrics, err := pkgmetrics.New(pkgmetrics.Options{
		Service: "test", Registerer: registry, Gatherer: registry,
		RegisterRuntime: &disabled, RegisterProcess: &disabled,
	})
	if err != nil {
		t.Fatalf("new metrics: %v", err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	reader := &countingReader{
		namespace: []string{"platform"},
		snapshots: map[string]configclient.Snapshot{
			"platform": {Values: map[string]string{"server.addr": ":9090"}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- snapshotconfig.RunPeriodic(ctx, snapshotconfig.PeriodicConfig{
			ConfigPath: path, Env: "dev", Reader: reader, Logger: logger,
			Metrics: metrics, Interval: 5 * time.Millisecond,
		})
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunPeriodic: %v", err)
	}

	if calls := reader.callCount(); calls < 2 {
		t.Fatalf("periodic sampling ran %d time(s), want >= 2", calls)
	}
	if got := sumGauge(t, gather(t, registry), "config_dual_read_mismatch"); got != 1 {
		t.Fatalf("mismatch gauge = %v, want 1", got)
	}
	// 采样多次但差异不变：明细日志只应出现一次。
	if got := strings.Count(logs.String(), "config dual-read mismatch"); got != 1 {
		t.Fatalf("mismatch log count = %d, want 1 (logs=%s)", got, logs.String())
	}
}

// TestRunPeriodicStopsWithoutBaseline 验证无文件基线时直接退出，不进入空转。
func TestRunPeriodicStopsWithoutBaseline(t *testing.T) {
	reader := &countingReader{namespace: []string{"platform"}, snapshots: map[string]configclient.Snapshot{}}
	done := make(chan error, 1)
	go func() {
		done <- snapshotconfig.RunPeriodic(context.Background(), snapshotconfig.PeriodicConfig{
			ConfigPath: filepath.Join(t.TempDir(), "missing.yaml"),
			Env:        "dev",
			Reader:     reader,
			Interval:   time.Millisecond,
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunPeriodic(no baseline) error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunPeriodic must return when baseline file is absent")
	}
	if calls := reader.callCount(); calls != 0 {
		t.Fatalf("sampling must not start without baseline, got %d call(s)", calls)
	}
}

// TestRunPeriodicWithoutReader 验证未接入配置中心时为空操作。
func TestRunPeriodicWithoutReader(t *testing.T) {
	if err := snapshotconfig.RunPeriodic(context.Background(), snapshotconfig.PeriodicConfig{
		ConfigPath: filepath.Join(t.TempDir(), "admin.yaml"),
	}); err != nil {
		t.Fatalf("RunPeriodic(nil reader) error = %v, want nil", err)
	}
}

// TestStartPeriodicRegistersBackgroundTask 验证接入平台后台任务注册器。
func TestStartPeriodicRegistersBackgroundTask(t *testing.T) {
	var registered func(context.Context) error
	snapshotconfig.StartPeriodic(func(task func(context.Context) error) { registered = task },
		snapshotconfig.PeriodicConfig{
			ConfigPath: filepath.Join(t.TempDir(), "missing.yaml"),
			Env:        "dev",
			Reader:     &countingReader{},
		})
	if registered == nil {
		t.Fatal("StartPeriodic must register a background task")
	}
	// 无基线时任务应立即返回，不阻塞 lifecycle。
	if err := registered(context.Background()); err != nil {
		t.Fatalf("background task error = %v, want nil", err)
	}

	// 未提供注册器时为空操作，不 panic。
	snapshotconfig.StartPeriodic(nil, snapshotconfig.PeriodicConfig{})
}

// TestRunPeriodicNeverFailsProcess 验证基线不可读时也只降级为不采样，
// 不把错误抛给进程生命周期（双读是观察工具，不应影响业务）。
func TestRunPeriodicNeverFailsProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(path, []byte("addr: [unclosed\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	reader := &countingReader{namespace: []string{"platform"}, snapshots: map[string]configclient.Snapshot{}}
	if err := snapshotconfig.RunPeriodic(context.Background(), snapshotconfig.PeriodicConfig{
		ConfigPath: path,
		Env:        "dev",
		Reader:     reader,
	}); err != nil {
		t.Fatalf("RunPeriodic(broken baseline) error = %v, want nil (must degrade silently)", err)
	}
	if calls := reader.callCount(); calls != 0 {
		t.Fatalf("sampling must not start with unreadable baseline, got %d call(s)", calls)
	}
}
