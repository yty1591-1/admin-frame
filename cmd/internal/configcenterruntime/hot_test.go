package configcenterruntime

import (
	"io"
	"log/slog"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/observability"
	"github.com/admin-frame/admin-frame/pkg/configclient"

	"github.com/prometheus/client_golang/prometheus"
)

// newHotFixture 构造热更状态与指标；logger 丢弃输出。
func newHotFixture(t *testing.T) (*hotState, *slog.LevelVar, *prometheus.Registry) {
	t.Helper()
	hot, err := newHotState()
	if err != nil {
		t.Fatalf("newHotState: %v", err)
	}
	level := &slog.LevelVar{}
	level.Set(slog.LevelInfo)
	registry := prometheus.NewRegistry()
	return hot, level, registry
}

func TestHotStateAppliesWhitelistedKeys(t *testing.T) {
	hot, level, registry := newHotFixture(t)
	metrics, err := observability.New(registry)
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	hot.onEvent(configclient.RefreshEvent{
		Namespace: "platform",
		Loaded:    true,
		Snapshot: configclient.Snapshot{
			ReleaseID: 7,
			Namespace: "platform",
			Values: map[string]string{
				"log.level":          "debug",
				"rate_limit.rps":     "120",
				"server.addr":        ":9090", // 已声明但不可热更 → ignored
				"unknown.leaked.key": "x",     // 未声明 → 完全忽略
			},
		},
	}, logger, metrics, level)

	view := hot.holder.Current()
	if view.Version() != 1 || view.ReleaseID() != 7 {
		t.Fatalf("view metadata = version %d / release %d", view.Version(), view.ReleaseID())
	}
	if view.Int("rate_limit.rps", 0) != 120 {
		t.Fatalf("rps = %d, want 120", view.Int("rate_limit.rps", 0))
	}
	if _, ok := view.Lookup("server.addr"); ok {
		t.Fatal("startup key must not enter the hot view")
	}
	if _, ok := view.Lookup("unknown.leaked.key"); ok {
		t.Fatal("undeclared key must not enter the hot view")
	}
	if level.Level() != slog.LevelDebug {
		t.Fatalf("log level = %v, want debug", level.Level())
	}
}

func TestHotStateIgnoresIdenticalView(t *testing.T) {
	hot, level, registry := newHotFixture(t)
	metrics, err := observability.New(registry)
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	event := configclient.RefreshEvent{
		Namespace: "platform", Loaded: true,
		Snapshot: configclient.Snapshot{
			ReleaseID: 1, Values: map[string]string{"rate_limit.rps": "100"},
		},
	}
	hot.onEvent(event, nil, metrics, level)
	version := hot.holder.Current().Version()
	hot.onEvent(event, nil, metrics, level)
	if got := hot.holder.Current().Version(); got != version {
		t.Fatalf("identical snapshot bumped version %d → %d", version, got)
	}
}

func TestHotStateRejectsInvalidValueAndKeepsView(t *testing.T) {
	hot, level, registry := newHotFixture(t)
	metrics, err := observability.New(registry)
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	hot.onEvent(configclient.RefreshEvent{
		Namespace: "platform", Loaded: true,
		Snapshot: configclient.Snapshot{
			ReleaseID: 1, Values: map[string]string{"rate_limit.rps": "100"},
		},
	}, logger, metrics, level)
	before := hot.holder.Current()

	// 一个非法值必须让整次构建失败，而不是部分生效。
	hot.onEvent(configclient.RefreshEvent{
		Namespace: "platform", Loaded: true,
		Snapshot: configclient.Snapshot{
			ReleaseID: 2,
			Values:    map[string]string{"rate_limit.rps": "abc", "log.level": "debug"},
		},
	}, logger, metrics, level)
	after := hot.holder.Current()
	if after.Version() != before.Version() || after.Int("rate_limit.rps", 0) != 100 {
		t.Fatalf("invalid build must keep previous view, got version %d rps %d",
			after.Version(), after.Int("rate_limit.rps", 0))
	}
	if level.Level() != slog.LevelInfo {
		t.Fatal("log level must not change when the whole build is rejected")
	}
}

func TestHotStateMergesNamespaces(t *testing.T) {
	hot, level, registry := newHotFixture(t)
	metrics, err := observability.New(registry)
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	hot.onEvent(configclient.RefreshEvent{
		Namespace: "platform", Loaded: true,
		Snapshot: configclient.Snapshot{ReleaseID: 1, Values: map[string]string{"rate_limit.rps": "50"}},
	}, nil, metrics, level)
	hot.onEvent(configclient.RefreshEvent{
		Namespace: "identity", Loaded: true,
		Snapshot: configclient.Snapshot{ReleaseID: 2, Values: map[string]string{"idempotency.ttl": "2h"}},
	}, nil, metrics, level)

	view := hot.holder.Current()
	if view.Int("rate_limit.rps", 0) != 50 {
		t.Fatalf("rps lost after identity refresh: %d", view.Int("rate_limit.rps", 0))
	}
	if view.Duration("idempotency.ttl", 0).Hours() != 2 {
		t.Fatalf("idempotency ttl = %v", view.Duration("idempotency.ttl", 0))
	}
}

func TestHotStateSkipsFailedEvent(t *testing.T) {
	hot, level, registry := newHotFixture(t)
	metrics, err := observability.New(registry)
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	hot.onEvent(configclient.RefreshEvent{
		Namespace: "platform", Err: io.ErrUnexpectedEOF, Loaded: true,
		Snapshot: configclient.Snapshot{ReleaseID: 9, Values: map[string]string{"log.level": "debug"}},
	}, nil, metrics, level)
	if hot.holder.Current().Version() != 0 {
		t.Fatal("failed refresh must not build a view")
	}
	if level.Level() != slog.LevelInfo {
		t.Fatal("failed refresh must not change log level")
	}
}
