package observability_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/iam/observability"
	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// compile-time 接口断言：指标实现必须满足三条观测端口。
var (
	_ iampcache.Observer        = (*observability.Metrics)(nil)
	_ app.Observer              = (*observability.Metrics)(nil)
	_ security.ShadowObserver   = (*observability.Metrics)(nil)
	_ security.DecisionObserver = (*observability.Metrics)(nil)
)

// newMetrics 构造独立 Registry 上的指标集。
func newMetrics(t *testing.T) (*observability.Metrics, *prometheus.Registry) {
	t.Helper()
	registry := prometheus.NewRegistry()
	metrics, err := observability.NewMetrics(registry)
	if err != nil {
		t.Fatalf("new metrics: %v", err)
	}
	return metrics, registry
}

// counterValue 从 Gather 结果中取指定指标 / 标签的计数值。
func counterValue(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if !labelsMatch(metric.GetLabel(), labels) {
				continue
			}
			return metric.GetCounter().GetValue()
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, labels)
	return 0
}

// labelsMatch 判断样本标签是否与期望完全一致（避免同名多标签样本误匹配）。
func labelsMatch(pairs []*dto.LabelPair, expected map[string]string) bool {
	if len(expected) != len(pairs) {
		return false
	}
	for _, pair := range pairs {
		if expected[pair.GetName()] != pair.GetValue() {
			return false
		}
	}
	return true
}

// TestCacheAndDecisionMetrics 验证缓存、决策、失效与加载指标。
func TestCacheAndDecisionMetrics(t *testing.T) {
	metrics, registry := newMetrics(t)
	ctx := context.Background()

	metrics.OnCache(iampcache.LayerL1, iampcache.ResultMiss)
	metrics.OnCache(iampcache.LayerL2, iampcache.ResultHit)
	metrics.OnCache(iampcache.LayerL2, iampcache.ResultError)
	metrics.OnInvalidateFailure(iampcache.LayerL2)
	metrics.OnSnapshotLoad(ctx, "admin", iampcache.LayerDB, 20*time.Millisecond)
	metrics.OnSnapshot(ctx, "admin", 12)
	metrics.OnSnapshotError(ctx, "cache_write", context.DeadlineExceeded)
	metrics.OnDecision(ctx, "admin", security.DecisionAllow)
	metrics.OnDecision(ctx, "biz", security.DecisionDeny)
	metrics.OnDecision(ctx, "biz", security.DecisionUnavailable)

	if got := counterValue(t, registry, "iam_authz_cache_total", map[string]string{"layer": "l1", "result": "miss"}); got != 1 {
		t.Fatalf("l1 miss=%v", got)
	}
	if got := counterValue(t, registry, "iam_authz_cache_total", map[string]string{"layer": "l2", "result": "hit"}); got != 1 {
		t.Fatalf("l2 hit=%v", got)
	}
	if got := counterValue(t, registry, "iam_cache_invalidate_failed_total", map[string]string{"layer": "l2"}); got != 1 {
		t.Fatalf("invalidate=%v", got)
	}
	if got := counterValue(t, registry, "iam_authz_decisions_total", map[string]string{"channel": "admin", "result": "allow"}); got != 1 {
		t.Fatalf("allow=%v", got)
	}
	if got := counterValue(t, registry, "iam_authz_decisions_total", map[string]string{"channel": "biz", "result": "unavailable"}); got != 1 {
		t.Fatalf("unavailable=%v", got)
	}
	if got := counterValue(t, registry, "iam_authz_load_errors_total", map[string]string{"stage": "cache_write"}); got != 1 {
		t.Fatalf("load error=%v", got)
	}
	// 直方图只断言已注册且有样本。
	assertHistogramHasSample(t, registry, "iam_authz_duration_seconds")
	assertHistogramHasSample(t, registry, "iam_authz_snapshot_permissions")
}

// TestShadowMetrics 验证 shadow 差异与加载错误指标。
func TestShadowMetrics(t *testing.T) {
	metrics, registry := newMetrics(t)
	ctx := context.Background()
	metrics.OnMismatch(ctx, security.Mismatch{
		Channel: "admin", Permission: "iam:permission:read", StaticAllow: true, IAMAllow: false,
	})
	metrics.OnLoadError(ctx, "admin", context.DeadlineExceeded)

	if got := counterValue(t, registry, "iam_shadow_mismatch_total", map[string]string{
		"channel": "admin", "static_allow": "true", "iam_allow": "false",
	}); got != 1 {
		t.Fatalf("mismatch=%v", got)
	}
	if got := counterValue(t, registry, "iam_authz_load_errors_total", map[string]string{"stage": "shadow"}); got != 1 {
		t.Fatalf("shadow load error=%v", got)
	}
}

// TestChangeMetrics 验证角色 / 绑定变更计数与失败率统计。
func TestChangeMetrics(t *testing.T) {
	metrics, registry := newMetrics(t)
	ctx := context.Background()
	base := audit.Event{
		EventID: "1", Channel: "admin", ResourceType: app.ResourceTypeRole, ResourceID: "r1",
		ActorID: "actor", Outcome: audit.OutcomeSuccess, OccurredAt: time.Now().UTC(),
		TraceID: "t1", Service: "test",
	}

	roleEvent := base
	roleEvent.Action = audit.ActionRBACRoleChange
	roleEvent.Attrs = map[string]string{app.AttrOperation: "create"}
	if err := (observability.ChangeRecorder{Metrics: metrics}).Record(ctx, roleEvent); err == nil {
		// Inner 为空必须失败关闭，同时仍记一次 failure。
		t.Fatal("missing inner recorder must fail")
	}

	bindingEvent := base
	bindingEvent.Action = audit.ActionRBACBindingChange
	bindingEvent.Channel = "biz"
	bindingEvent.Attrs = map[string]string{app.AttrOperation: "bind"}
	recorder := observability.ChangeRecorder{
		Inner:   recorderFunc(func(context.Context, audit.Event) error { return nil }),
		Metrics: metrics,
	}
	if err := recorder.Record(ctx, bindingEvent); err != nil {
		t.Fatalf("record binding: %v", err)
	}

	if got := counterValue(t, registry, "iam_role_changes_total", map[string]string{
		"channel": "admin", "operation": "create", "outcome": "failure",
	}); got != 1 {
		t.Fatalf("role failure=%v", got)
	}
	if got := counterValue(t, registry, "iam_binding_changes_total", map[string]string{
		"channel": "biz", "operation": "bind", "outcome": "success",
	}); got != 1 {
		t.Fatalf("binding success=%v", got)
	}
}

// recorderFunc 把函数适配为 app.ChangeRecorder（测试桩）。
type recorderFunc func(context.Context, audit.Event) error

func (f recorderFunc) Record(ctx context.Context, event audit.Event) error { return f(ctx, event) }

// TestNewMetricsRejectsInvalidSetup 验证装配期参数校验与重复注册。
func TestNewMetricsRejectsInvalidSetup(t *testing.T) {
	if _, err := observability.NewMetrics(nil); err == nil {
		t.Fatal("nil registerer must be rejected")
	}
	registry := prometheus.NewRegistry()
	if _, err := observability.NewMetrics(registry); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if _, err := observability.NewMetrics(registry); err == nil {
		t.Fatal("duplicate registration must be rejected")
	}
}

// TestMetricsNilSafety 验证 nil 指标对象不 panic（禁用观测的部署）。
func TestMetricsNilSafety(_ *testing.T) {
	var metrics *observability.Metrics
	ctx := context.Background()
	metrics.OnCache(iampcache.LayerL1, iampcache.ResultHit)
	metrics.OnInvalidateFailure(iampcache.LayerL2)
	metrics.OnSnapshotLoad(ctx, "admin", iampcache.LayerL1, time.Millisecond)
	metrics.OnSnapshot(ctx, "admin", 1)
	metrics.OnSnapshotError(ctx, "db", context.Canceled)
	metrics.OnDecision(ctx, "admin", security.DecisionAllow)
	metrics.OnMismatch(ctx, security.Mismatch{})
	metrics.OnLoadError(ctx, "admin", context.Canceled)
	metrics.OnChange(ctx, audit.ActionRBACRoleChange, "admin", "create", "success")
}

// assertHistogramHasSample 断言直方图至少有一个样本。
func assertHistogramHasSample(t *testing.T, registry *prometheus.Registry, name string) {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metric.GetHistogram().GetSampleCount() > 0 {
				return
			}
		}
	}
	t.Fatalf("histogram %s has no samples", name)
}
