package iamruntime_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/metrics"
)

// newDeps 构造不依赖外部服务的装配参数（db.driver=memory）。
func newDeps(t *testing.T, outbox platform.IAMOutboxConfig) *platform.Deps {
	t.Helper()
	met, err := metrics.New(metrics.Options{Service: "admin-api", Channel: "admin", Env: "local"})
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}
	return &platform.Deps{
		Config: &platform.Config{
			Env:     "local",
			Service: "admin-api",
			Channel: "admin",
			IAM: platform.IAMConfig{
				Mode:        platform.IAMModeShadow,
				LoadTimeout: 300 * time.Millisecond,
				Cache: platform.IAMCacheConfig{
					Driver:      platform.IAMCacheDriverMemory,
					L1TTL:       3 * time.Second,
					L2TTL:       5 * time.Minute,
					NegativeTTL: 30 * time.Second,
					KeyPrefix:   "iam",
				},
				Limits: platform.IAMLimitsConfig{
					MaxRolesPerSubject:        20,
					MaxPermissionsPerRole:     200,
					MaxBindingsPerSubject:     50,
					MaxCustomRolesPerMerchant: 50,
				},
				Outbox: outbox,
			},
		},
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics: met,
	}
}

// TestRuntimeOutboxDisabledHasNoBackgroundTasks 验证默认装配不启动后台投递。
func TestRuntimeOutboxDisabledHasNoBackgroundTasks(t *testing.T) {
	rt, err := iamruntime.New(newDeps(t, platform.IAMOutboxConfig{}), iamruntime.Options{})
	if err != nil {
		t.Fatalf("iamruntime.New: %v", err)
	}
	if len(rt.Background) != 0 {
		t.Fatalf("background tasks = %d, want 0 when outbox disabled", len(rt.Background))
	}
}

// TestRuntimeOutboxEnabledStartsPublisher 验证启用后注册后台任务并暴露积压指标。
func TestRuntimeOutboxEnabledStartsPublisher(t *testing.T) {
	registry := prometheus.NewRegistry()
	met, err := metrics.New(metrics.Options{
		Service:    "admin-api",
		Channel:    "admin",
		Env:        "local",
		Registerer: registry,
		Gatherer:   registry,
	})
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}
	deps := newDeps(t, platform.IAMOutboxConfig{
		Enabled:      true,
		PollInterval: 10 * time.Millisecond,
		BatchSize:    10,
		MaxAttempts:  3,
	})
	deps.Metrics = met

	rt, err := iamruntime.New(deps, iamruntime.Options{})
	if err != nil {
		t.Fatalf("iamruntime.New: %v", err)
	}
	if len(rt.Background) != 1 {
		t.Fatalf("background tasks = %d, want 1 publisher", len(rt.Background))
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Background[0](ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("publisher stopped with error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("publisher did not stop after context cancellation")
	}
}

// TestRuntimeOutboxGaugesRegistered 验证两个积压指标可在 /metrics 中被采集。
func TestRuntimeOutboxGaugesRegistered(t *testing.T) {
	registry := prometheus.NewRegistry()
	met, err := metrics.New(metrics.Options{
		Service:    "admin-api",
		Channel:    "admin",
		Env:        "local",
		Registerer: registry,
		Gatherer:   registry,
	})
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}
	deps := newDeps(t, platform.IAMOutboxConfig{Enabled: true, PollInterval: time.Second, BatchSize: 10, MaxAttempts: 3})
	deps.Metrics = met
	if _, err := iamruntime.New(deps, iamruntime.Options{}); err != nil {
		t.Fatalf("iamruntime.New: %v", err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	wanted := map[string]bool{"iam_outbox_pending": false, "iam_outbox_lag_seconds": false, "iam_outbox_delivery_total": false}
	for _, family := range families {
		if _, ok := wanted[family.GetName()]; ok {
			wanted[family.GetName()] = true
		}
	}
	for name, found := range wanted {
		if !found {
			t.Fatalf("metric %s is not registered", name)
		}
	}
}
