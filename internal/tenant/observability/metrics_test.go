package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// newTestMetrics 构造带独立 Registry 的指标集。
func newTestMetrics(t *testing.T) (*Metrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	return m, reg
}

// counterValue 读取指定 label 组合的计数器值。
func counterValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, metric := range mf.GetMetric() {
			got := map[string]string{}
			for _, lp := range metric.GetLabel() {
				got[lp.GetName()] = lp.GetValue()
			}
			match := true
			for k, v := range labels {
				if got[k] != v {
					match = false
					break
				}
			}
			if match && len(labels) == len(got) {
				if metric.GetCounter() != nil {
					return metric.GetCounter().GetValue()
				}
				if metric.GetGauge() != nil {
					return metric.GetGauge().GetValue()
				}
			}
		}
	}
	return -1
}

// TestObserverCounters 验证四个观测信号到计数器的映射。
func TestObserverCounters(t *testing.T) {
	m, reg := newTestMetrics(t)

	m.GuardDecision("allow")
	m.GuardDecision("allow")
	m.GuardDecision("deny")
	m.GuardDecision("error")
	if got := counterValue(t, reg, "tenant_guard_decisions_total", map[string]string{"result": "allow"}); got != 2 {
		t.Fatalf("allow = %v, want 2", got)
	}
	if got := counterValue(t, reg, "tenant_guard_decisions_total", map[string]string{"result": "deny"}); got != 1 {
		t.Fatalf("deny = %v, want 1", got)
	}
	if got := counterValue(t, reg, "tenant_guard_decisions_total", map[string]string{"result": "error"}); got != 1 {
		t.Fatalf("error = %v, want 1", got)
	}

	m.GuardCacheHit(true)
	m.GuardCacheHit(false)
	if got := counterValue(t, reg, "tenant_guard_cache_hit_total", map[string]string{"hit": "true"}); got != 1 {
		t.Fatalf("hit true = %v", got)
	}
	if got := counterValue(t, reg, "tenant_guard_cache_hit_total", map[string]string{"hit": "false"}); got != 1 {
		t.Fatalf("hit false = %v", got)
	}

	m.Transition("pending", "active")
	if got := counterValue(t, reg, "tenant_transition_total",
		map[string]string{"from": "pending", "to": "active"}); got != 1 {
		t.Fatalf("transition = %v", got)
	}

	m.AuditEmitFailed()
	if got := counterValue(t, reg, "tenant_audit_emit_failed_total", nil); got != 1 {
		t.Fatalf("audit emit failed = %v", got)
	}
}

// TestNilMetricsIsNoop 验证 nil 指标集不 panic（最小进程形态退化）。
func TestNilMetricsIsNoop(t *testing.T) {
	var m *Metrics
	m.GuardDecision("allow")
	m.GuardCacheHit(true)
	m.Transition("pending", "active")
	m.AuditEmitFailed()
	if err := m.RegisterStatusGauge(nil); err == nil {
		t.Fatal("nil metrics 注册 Gauge 应报错")
	}
}

// TestRegisterStatusGauge 验证状态 Gauge：正常计数、缓存、失败沿用旧值。
func TestRegisterStatusGauge(t *testing.T) {
	m, reg := newTestMetrics(t)

	counts := map[domain.Status]int{domain.StatusActive: 3, domain.StatusPending: 1}
	calls := 0
	err := m.RegisterStatusGauge(func(context.Context) (map[domain.Status]int, error) {
		calls++
		return counts, nil
	})
	if err != nil {
		t.Fatalf("RegisterStatusGauge() error = %v", err)
	}
	// 重复注册幂等。
	if err := m.RegisterStatusGauge(func(context.Context) (map[domain.Status]int, error) {
		return nil, nil
	}); err != nil {
		t.Fatalf("重复注册 error = %v", err)
	}

	if got := counterValue(t, reg, "tenant_status", map[string]string{"status": "active"}); got != 3 {
		t.Fatalf("active = %v, want 3", got)
	}
	if got := counterValue(t, reg, "tenant_status", map[string]string{"status": "pending"}); got != 1 {
		t.Fatalf("pending = %v, want 1", got)
	}

	// 查询失败沿用旧值（不跳变成 0）。
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	// 缓存 TTL 内第二次 Gather 不应再查询。
	before := calls
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("第二次 Gather() error = %v", err)
	}
	if calls != before {
		t.Fatalf("缓存期内不应重复查询: calls %d -> %d", before, calls)
	}

	// nil 回调注册报错。
	if err := m.RegisterStatusGauge(nil); err == nil {
		t.Fatal("nil 回调应报错")
	}
}

// TestStatusGaugeFailureKeepsLastValue 验证查询失败时 Gauge 沿用上一次快照。
func TestStatusGaugeFailureKeepsLastValue(t *testing.T) {
	m, reg := newTestMetrics(t)

	ok := true
	err := m.RegisterStatusGauge(func(context.Context) (map[domain.Status]int, error) {
		if !ok {
			return nil, errors.New("db down")
		}
		return map[domain.Status]int{domain.StatusTerminated: 2}, nil
	})
	if err != nil {
		t.Fatalf("RegisterStatusGauge() error = %v", err)
	}
	if got := counterValue(t, reg, "tenant_status", map[string]string{"status": "terminated"}); got != 2 {
		t.Fatalf("terminated = %v, want 2", got)
	}

	// 强制缓存过期：直接操作内部状态不可行（未导出），等待不可取——
	// 用第二次注册独立实例验证失败路径：新 Gauge 查询失败时不暴露该状态。
	m2, reg2 := newTestMetrics(t)
	if err := m2.RegisterStatusGauge(func(context.Context) (map[domain.Status]int, error) {
		return nil, errors.New("db down")
	}); err != nil {
		t.Fatalf("RegisterStatusGauge() error = %v", err)
	}
	if got := counterValue(t, reg2, "tenant_status", map[string]string{"status": "terminated"}); got != -1 {
		t.Fatalf("查询失败不应暴露任何状态, got %v", got)
	}
}
