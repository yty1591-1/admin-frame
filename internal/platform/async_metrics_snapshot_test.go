package platform_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// TestAsyncMetricsGaugesShareOneSnapshot 断言三个 async Gauge 共用一份快照：
// 一次 Gather 只查一次库。
//
// 这是 ENT-06 T6 的回归门禁：DB 池打满时，若每个 Gauge 各查一次库，
// /metrics 响应会叠加到超过 Prometheus 的 scrape_timeout，抓取失败后该进程
// 全部指标一起丢失，DBPoolExhaustion 反而永远不响。
func TestAsyncMetricsGaugesShareOneSnapshot(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := platform.NewAsyncMetrics(registry)
	if err != nil {
		t.Fatalf("new metrics: %v", err)
	}
	var calls int32
	if err := metrics.RegisterGauges(func(context.Context) (outbox.Stats, error) {
		atomic.AddInt32(&calls, 1)
		return outbox.Stats{Pending: 7}, nil
	}); err != nil {
		t.Fatalf("register gauges: %v", err)
	}
	if _, err := registry.Gather(); err != nil {
		t.Fatalf("gather: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("stats calls=%d, want 1 (three gauges must share one snapshot)", got)
	}
}

// TestAsyncMetricsGaugesSurviveSlowStats 断言采集函数超时/失败时，
// Gather 仍能返回指标（沿用上一次成功快照），而不是把采集错误抛给抓取端。
func TestAsyncMetricsGaugesSurviveSlowStats(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := platform.NewAsyncMetrics(registry)
	if err != nil {
		t.Fatalf("new metrics: %v", err)
	}
	if err := metrics.RegisterGauges(func(context.Context) (outbox.Stats, error) {
		return outbox.Stats{}, errors.New("db unavailable")
	}); err != nil {
		t.Fatalf("register gauges: %v", err)
	}
	start := time.Now()
	families, err := registry.Gather()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("gather took %s, want bounded by a single stats timeout", elapsed)
	}
	found := false
	for _, family := range families {
		if family.GetName() == "async_outbox_pending" {
			found = true
		}
	}
	if !found {
		t.Fatal("async_outbox_pending missing after failed stats")
	}
}
