package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// asyncStatsTimeout 是 GaugeFunc 单次查询积压统计的超时，避免采集线程被慢 SQL 拖住。
const asyncStatsTimeout = 3 * time.Second

// AsyncMetrics 是平台级异步基础设施指标集（ENT-03）。
//
// 与 IAM 自有指标的关系：`async_*` 描述通用 Relay，IAM 审计投递仍由
// `iam_outbox_*` 暴露，两者共享同一 Registry。
type AsyncMetrics struct {
	delivery   *prometheus.CounterVec
	registerer prometheus.Registerer
}

// NewAsyncMetrics 创建并注册 async_* 指标；重复注册视为装配错误（fail-fast）。
func NewAsyncMetrics(registerer prometheus.Registerer) (*AsyncMetrics, error) {
	if registerer == nil {
		return nil, fmt.Errorf("async metrics: registerer is required")
	}
	m := &AsyncMetrics{
		registerer: registerer,
		delivery: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "async_outbox_delivery_total",
			Help: "Outbox 投递结果，result=success|retry|dead|poison。",
		}, []string{"result"}),
	}
	if err := registerer.Register(m.delivery); err != nil {
		return nil, fmt.Errorf("async metrics: register delivery counter: %w", err)
	}
	// 预创建结果标签，保证无投递时也能被采集，避免告警表达式因指标缺失静默失效。
	for _, result := range []string{"success", "retry", "dead", "poison"} {
		m.delivery.WithLabelValues(result)
	}
	return m, nil
}

// OnOutboxDelivered 实现 outbox.Observer。
func (m *AsyncMetrics) OnOutboxDelivered(_ context.Context, _, result string) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	m.delivery.WithLabelValues(result).Inc()
}

// RegisterGauges 注册 async_outbox_pending / async_outbox_lag_seconds / async_dlq_depth。
//
// 三个 GaugeFunc 各自查询统计；查询失败时 pending/dlq 暴露 0、lag 暴露 0，
// 投递失败仍可通过 async_outbox_delivery_total{result=dead|poison} 告警。
func (m *AsyncMetrics) RegisterGauges(stats func(context.Context) (outbox.Stats, error)) error {
	if m == nil || m.registerer == nil {
		return fmt.Errorf("async metrics: metrics is not initialized")
	}
	if stats == nil {
		return fmt.Errorf("async metrics: stats function is required")
	}
	counters := []struct {
		name string
		help string
		pick func(outbox.Stats, time.Time) float64
	}{
		{
			name: "async_outbox_pending",
			help: "Outbox 未完成事件数（含 pending/processing/dead/failed）；持续 >0 说明投递受阻。",
			pick: func(s outbox.Stats, _ time.Time) float64 { return nonNegative(s.Pending) },
		},
		{
			name: "async_outbox_lag_seconds",
			help: "Outbox 最老未完成事件的积压秒数；持续增长触发 P2 告警。",
			pick: func(s outbox.Stats, now time.Time) float64 {
				if s.OldestCreatedAt.IsZero() || !s.OldestCreatedAt.Before(now) {
					return 0
				}
				return now.Sub(s.OldestCreatedAt).Seconds()
			},
		},
		{
			name: "async_dlq_depth",
			help: "死信表中未重放的事件数；>0 需按 ASYNC_DLQ runbook 处理。",
			pick: func(s outbox.Stats, _ time.Time) float64 { return nonNegative(s.DLQ) },
		},
	}
	for _, counter := range counters {
		gauge := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: counter.name,
			Help: counter.help,
		}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), asyncStatsTimeout)
			defer cancel()
			snapshot, err := stats(ctx)
			if err != nil {
				return 0
			}
			return counter.pick(snapshot, time.Now())
		})
		if err := m.registerer.Register(gauge); err != nil {
			return fmt.Errorf("async metrics: register %s: %w", counter.name, err)
		}
	}
	return nil
}

func nonNegative(value int) float64 {
	if value < 0 {
		return 0
	}
	return float64(value)
}

// 编译期断言：指标集必须满足 Relay 观测端口。
var _ outbox.Observer = (*AsyncMetrics)(nil)
