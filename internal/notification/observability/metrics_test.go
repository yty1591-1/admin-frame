package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
)

func TestMetricsRegistersAndObserves(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(reg, nil)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}

	// 各埋点不 panic 且计数可见。
	m.DeliveryObserved(domain.ChannelEmail, "smtp", "accepted", "")
	m.DeliveryObserved(domain.ChannelSms, "aliyun", "failed", "provider_error")
	m.RateLimited()
	m.RequestObserved("tenant.invitation", domain.RequestRejected)
	m.AuditEmitFailed()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	names := make(map[string]bool, len(families))
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{
		"notify_send_total", "notify_rate_limited_total",
		"notify_audit_emit_failed_total", "notify_request_total",
	} {
		if !names[want] {
			t.Fatalf("metric %s must be registered, got %v", want, names)
		}
	}
}

func TestMetricsNilSafetyAndDuplicateRegistration(t *testing.T) {
	var nilMetrics *Metrics
	nilMetrics.DeliveryObserved(domain.ChannelEmail, "smtp", "accepted", "")
	nilMetrics.RateLimited()
	nilMetrics.RequestObserved("s", domain.RequestSent)
	nilMetrics.AuditEmitFailed()

	reg := prometheus.NewRegistry()
	if _, err := NewMetrics(reg, nil); err != nil {
		t.Fatalf("first register: %v", err)
	}
	// 同一 registerer 重复注册必须失败（防装配期静默丢指标）。
	if _, err := NewMetrics(reg, nil); err == nil {
		t.Fatal("duplicate registration must fail")
	}
}

func TestMetricsDeliveryGauge(t *testing.T) {
	reg := prometheus.NewRegistry()
	calls := 0
	reader := func(context.Context) (map[domain.DeliveryStatus]int, error) {
		calls++
		return map[domain.DeliveryStatus]int{
			domain.DeliveryPending:  3,
			domain.DeliveryRetrying: 2,
			domain.DeliveryAccepted: 10,
		}, nil
	}
	m, err := NewMetrics(reg, reader)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	_ = m

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	found := false
	for _, f := range families {
		if f.GetName() != "notify_delivery_active" {
			continue
		}
		found = true
		if len(f.GetMetric()) != 1 || f.GetMetric()[0].GetGauge().GetValue() != 5 {
			t.Fatalf("gauge must report pending+retrying=5, got %+v", f.GetMetric())
		}
	}
	if !found {
		t.Fatal("delivery gauge must be registered")
	}

	// 读失败沿用旧快照，不阻塞抓取。
	failing := func(context.Context) (map[domain.DeliveryStatus]int, error) {
		return nil, errors.New("db down")
	}
	reg2 := prometheus.NewRegistry()
	if _, err := NewMetrics(reg2, failing); err != nil {
		t.Fatalf("NewMetrics with failing reader: %v", err)
	}
	if _, err := reg2.Gather(); err != nil {
		t.Fatalf("Gather with failing reader: %v", err)
	}
}
