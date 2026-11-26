package platform_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	platformasync "github.com/admin-frame/admin-frame/internal/platform/async"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

func TestAsyncMetricsDeliveryCounter(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := platformasync.NewMetrics(registry)
	if err != nil {
		t.Fatalf("new metrics: %v", err)
	}
	if _, err := platformasync.NewMetrics(registry); err == nil {
		t.Fatal("duplicate registration should fail")
	}

	metrics.OnOutboxDelivered(context.Background(), "iam.audit", "success")
	metrics.OnOutboxDelivered(context.Background(), "iam.audit", "dead")

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	values := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "async_outbox_delivery_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "result" {
					values[label.GetValue()] = metric.GetCounter().GetValue()
				}
			}
		}
	}
	if values["success"] != 1 || values["dead"] != 1 {
		t.Fatalf("delivery counter = %+v, want success=1 dead=1", values)
	}
	if _, ok := values["retry"]; !ok {
		t.Fatalf("pre-created retry label missing: %+v", values)
	}
}

func TestAsyncMetricsGauges(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := platformasync.NewMetrics(registry)
	if err != nil {
		t.Fatalf("new metrics: %v", err)
	}
	now := time.Now().Add(-30 * time.Second)
	if err := metrics.RegisterGauges(func(context.Context) (outbox.Stats, error) {
		return outbox.Stats{Pending: 3, DLQ: 1, OldestCreatedAt: now, ReplayedRecently: 2}, nil
	}); err != nil {
		t.Fatalf("register gauges: %v", err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	values := map[string]float64{}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			values[family.GetName()] = metric.GetGauge().GetValue()
		}
	}
	if values["async_outbox_pending"] != 3 {
		t.Fatalf("async_outbox_pending = %v, want 3", values["async_outbox_pending"])
	}
	if values["async_dlq_depth"] != 1 {
		t.Fatalf("async_dlq_depth = %v, want 1", values["async_dlq_depth"])
	}
	if values["async_dlq_replayed_30m"] != 2 {
		t.Fatalf("async_dlq_replayed_30m = %v, want 2", values["async_dlq_replayed_30m"])
	}
	if lag := values["async_outbox_lag_seconds"]; lag < 29 || lag > 60 {
		t.Fatalf("async_outbox_lag_seconds = %v, want ~30", lag)
	}
}

func TestAsyncMetricsGaugesFailureExposesZero(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := platformasync.NewMetrics(registry)
	if err != nil {
		t.Fatalf("new metrics: %v", err)
	}
	if err := metrics.RegisterGauges(func(context.Context) (outbox.Stats, error) {
		return outbox.Stats{}, errors.New("db down")
	}); err != nil {
		t.Fatalf("register gauges: %v", err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			if value := metric.GetGauge().GetValue(); value != 0 {
				t.Fatalf("%s = %v, want 0 on stats failure", family.GetName(), value)
			}
		}
	}
}
