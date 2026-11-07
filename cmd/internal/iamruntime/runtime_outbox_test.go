package iamruntime_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/id"
	"github.com/admin-frame/admin-frame/pkg/metrics"
)

// iamTestConfig 返回 IAM 运行时测试用的域配置（shadow + memory 缓存）。
func iamTestConfig() iamconfig.IAMConfig {
	return iamconfig.IAMConfig{
		Mode:        iamconfig.IAMModeShadow,
		LoadTimeout: 300 * time.Millisecond,
		Cache: iamconfig.IAMCacheConfig{
			Driver:      iamconfig.IAMCacheDriverMemory,
			L1TTL:       3 * time.Second,
			L2TTL:       5 * time.Minute,
			NegativeTTL: 30 * time.Second,
			KeyPrefix:   "iam",
		},
		Limits: iamconfig.IAMLimitsConfig{
			MaxRolesPerSubject:        20,
			MaxPermissionsPerRole:     200,
			MaxBindingsPerSubject:     50,
			MaxCustomRolesPerMerchant: 50,
		},
	}
}

// newDeps 构造不依赖外部服务的装配参数（db.driver=memory 或 SkipPing 的 mysql）。
func newDeps(t *testing.T, async platform.AsyncConfig, withDatabase bool) *platform.Deps {
	t.Helper()
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
	deps := &platform.Deps{
		Config: &platform.Config{
			Env:     "local",
			Service: "admin-api",
			Channel: "admin",
			Async:   async,
		},
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics: met,
		IDs:     id.MustSnowflake(id.Options{NodeID: 1}),
	}
	if !withDatabase {
		return deps
	}
	database, err := db.Open(context.Background(), db.Config{
		Driver:   "mysql",
		DSN:      "iam:iam@tcp(127.0.0.1:3306)/iam",
		SkipPing: true,
	})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	deps.Database = database
	rt, err := platform.NewAsyncRuntime(deps)
	if err != nil {
		t.Fatalf("NewAsyncRuntime: %v", err)
	}
	deps.Async = rt
	return deps
}

// TestRuntimeWithoutAsyncKeepsSlogRecorder 验证未启用 async 时装配成功且不注册异步指标。
func TestRuntimeWithoutAsyncKeepsSlogRecorder(t *testing.T) {
	deps := newDeps(t, platform.AsyncConfig{}, false)
	if deps.Async != nil {
		t.Fatal("async runtime must be nil when disabled")
	}
	if _, err := iamruntime.New(deps, iamTestConfig(), iamruntime.Options{}); err != nil {
		t.Fatalf("iamruntime.New: %v", err)
	}
	if err := iamruntime.RegisterAsync(deps); err != nil {
		t.Fatalf("RegisterAsync must be a no-op when disabled: %v", err)
	}
}

// TestRuntimeAsyncRegistersIAMHandlers 验证启用 async 后 IAM 主题已在路由中注册。
func TestRuntimeAsyncRegistersIAMHandlers(t *testing.T) {
	deps := newDeps(t, platform.AsyncConfig{
		Enabled: true,
		Driver:  platform.AsyncDriverDB,
		Outbox: platform.AsyncOutboxConfig{
			PollInterval: time.Second,
			BatchSize:    10,
			Lease:        30 * time.Second,
			MaxAttempts:  3,
			Concurrency:  1,
		},
		Consumer: platform.AsyncConsumerConfig{
			Concurrency:       2,
			VisibilityTimeout: time.Minute,
			RetryBackoff:      platform.AsyncBackoffExponential,
		},
	}, true)
	if deps.Async == nil {
		t.Fatal("async runtime must be assembled when enabled")
	}
	if _, err := iamruntime.New(deps, iamTestConfig(), iamruntime.Options{}); err != nil {
		t.Fatalf("iamruntime.New: %v", err)
	}
}

// TestRuntimeAsyncGaugesRegistered 验证 IAM 积压指标与投递指标可在 /metrics 中被采集。
func TestRuntimeAsyncGaugesRegistered(t *testing.T) {
	deps := newDeps(t, platform.AsyncConfig{
		Enabled: true,
		Driver:  platform.AsyncDriverDB,
		Outbox: platform.AsyncOutboxConfig{
			PollInterval: time.Second,
			BatchSize:    10,
			Lease:        30 * time.Second,
			MaxAttempts:  3,
			Concurrency:  1,
		},
		Consumer: platform.AsyncConsumerConfig{
			Concurrency:       2,
			VisibilityTimeout: time.Minute,
			RetryBackoff:      platform.AsyncBackoffExponential,
		},
	}, true)
	if _, err := iamruntime.New(deps, iamTestConfig(), iamruntime.Options{}); err != nil {
		t.Fatalf("iamruntime.New: %v", err)
	}

	families, err := deps.Metrics.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	wanted := map[string]bool{
		"iam_outbox_pending":        false,
		"iam_outbox_lag_seconds":    false,
		"iam_outbox_delivery_total": false,
	}
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
