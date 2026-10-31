package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// TestMetricsRecordCoreEvents 验证指标注册与关键事件计数，避免指标名漂移导致告警静默。
func TestMetricsRecordCoreEvents(t *testing.T) {
	registry := prometheus.NewRegistry()
	m, err := NewMetrics(registry)
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	ctx := context.Background()
	m.ObserveLogin(ctx, domain.ChannelAdmin, app.MetricResultFailure, app.MetricReasonBadCredential, 100*time.Millisecond)
	m.ObserveTokenReuse(ctx)
	m.ObserveSessionRevoke(ctx, string(domain.RevokeReasonReplay), app.MetricResultFailure)
	m.OnRevocationStoreFailure(ctx, "account_load", errors.New("store down"))
	m.ObserveSMSProviderAlert("insufficient_balance", "aliyun")

	counters := gatheredCounters(t, registry)
	if got := counters["identity_token_reuse_total"]; got != 1 {
		t.Fatalf("identity_token_reuse_total = %v, want 1", got)
	}
	if got := counters["identity_session_revoke_total"]; got != 1 {
		t.Fatalf("identity_session_revoke_total = %v, want 1", got)
	}
	if got := counters["identity_revocation_store_errors_total"]; got != 1 {
		t.Fatalf("identity_revocation_store_errors_total = %v, want 1", got)
	}
	if got := counters["identity_login_total"]; got != 1 {
		t.Fatalf("identity_login_total = %v, want 1", got)
	}
	if got := counters["identity_sms_provider_alerts_total"]; got != 1 {
		t.Fatalf("identity_sms_provider_alerts_total = %v, want 1", got)
	}

	// 重复注册必须 fail-fast，避免同一 Registry 下指标冲突。
	if _, err := NewMetrics(registry); err == nil {
		t.Fatalf("NewMetrics() on same registry must fail")
	}
}

// TestCleanupMetricsRecordRun 验证清理指标覆盖成功/失败/跳过与批次上限（OPS-02）。
func TestCleanupMetricsRecordRun(t *testing.T) {
	registry := prometheus.NewRegistry()
	m, err := NewCleanupMetrics(registry)
	if err != nil {
		t.Fatalf("NewCleanupMetrics() error = %v", err)
	}
	ctx := context.Background()
	m.OnCleanupRun(ctx, app.CleanupRunResult{
		Stats:    app.CleanupStats{ExpiredSessions: 2, DeletedLoginAttempts: 3, BatchLimitHit: true},
		Duration: 100 * time.Millisecond,
	})
	m.OnCleanupRun(ctx, app.CleanupRunResult{Skipped: true, SkipReason: "lock_held"})
	m.OnCleanupRun(ctx, app.CleanupRunResult{Err: errors.New("db down")})

	counters := gatheredCounters(t, registry)
	if got := counters["identity_cleanup_runs_total"]; got != 3 {
		t.Fatalf("identity_cleanup_runs_total = %v, want 3", got)
	}
	if got := counters["identity_cleanup_rows_total"]; got != 5 {
		t.Fatalf("identity_cleanup_rows_total = %v, want 5", got)
	}
	if got := counters["identity_cleanup_batch_limit_hit_total"]; got != 1 {
		t.Fatalf("identity_cleanup_batch_limit_hit_total = %v, want 1", got)
	}
	if _, err := NewCleanupMetrics(registry); err == nil {
		t.Fatalf("NewCleanupMetrics() on same registry must fail")
	}
}

// gatheredCounters 汇总 Registry 中所有 counter 的值，按指标名累加各标签序列。
func gatheredCounters(t *testing.T, registry *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	out := make(map[string]float64)
	for _, family := range families {
		if family.GetType() != dto.MetricType_COUNTER {
			continue
		}
		for _, metric := range family.GetMetric() {
			out[family.GetName()] += metric.GetCounter().GetValue()
		}
	}
	return out
}
