package async

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/pkg/metrics"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// asyncStatsTimeout 是 GaugeFunc 单次查询积压统计的超时，避免采集线程被慢 SQL 拖住。
const asyncStatsTimeout = 2 * time.Second

// asyncStatsCacheTTL 是三个 async Gauge 共用快照的有效期。
//
// 取值要覆盖 Prometheus 默认抓取间隔（15s）的一部分：同一抓取周期内三个 Gauge
// 只触发一次查询，DB 慢时 /metrics 响应不会叠加成 N 倍超时。
const asyncStatsCacheTTL = 2 * time.Second

// Metrics 是平台级异步基础设施指标集（ENT-03）。
//
// 与 IAM 自有指标的关系：`async_*` 描述通用 Relay，IAM 审计投递仍由
// `iam_outbox_*` 暴露，两者共享同一 Registry。
type Metrics struct {
	delivery      *prometheus.CounterVec
	brokerPublish *prometheus.CounterVec
	brokerConsume *prometheus.CounterVec
	registerer    prometheus.Registerer
}

// NewMetrics 创建并注册 async_* 指标；重复注册视为装配错误（fail-fast）。
func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	if registerer == nil {
		return nil, fmt.Errorf("async metrics: registerer is required")
	}
	m := &Metrics{
		registerer: registerer,
		delivery: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "async_outbox_delivery_total",
			Help: "Outbox 投递结果，result=success|retry|dead|poison。",
		}, []string{"result"}),
		brokerPublish: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "async_broker_publish_total",
			Help: "外部 broker 发布结果，result=success|error。",
		}, []string{"result"}),
		brokerConsume: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "async_broker_consume_total",
			Help: "外部 broker 消费结果，result=ack|retry|dead。",
		}, []string{"result"}),
	}
	for _, collector := range []prometheus.Collector{m.delivery, m.brokerPublish, m.brokerConsume} {
		if err := registerer.Register(collector); err != nil {
			return nil, fmt.Errorf("async metrics: register: %w", err)
		}
	}
	// 预创建结果标签，保证无投递时也能被采集，避免告警表达式因指标缺失静默失效。
	for _, result := range []string{"success", "retry", "dead", "poison"} {
		m.delivery.WithLabelValues(result)
	}
	for _, result := range []string{"success", "error"} {
		m.brokerPublish.WithLabelValues(result)
	}
	for _, result := range []string{"ack", "retry", "dead"} {
		m.brokerConsume.WithLabelValues(result)
	}
	return m, nil
}

// OnOutboxDelivered 实现 outbox.Observer。
func (m *Metrics) OnOutboxDelivered(_ context.Context, _, result string) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	m.delivery.WithLabelValues(result).Inc()
}

// OnBrokerPublish 实现 BrokerObserver。
func (m *Metrics) OnBrokerPublish(_ context.Context, result string) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	m.brokerPublish.WithLabelValues(result).Inc()
}

// OnBrokerConsume 实现 BrokerObserver。
func (m *Metrics) OnBrokerConsume(_ context.Context, result string) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	m.brokerConsume.WithLabelValues(result).Inc()
}

// RegisterGauges 注册 async_outbox_pending / async_outbox_lag_seconds / async_dlq_depth /
// async_dlq_replayed_30m。
//
// 每个 GaugeFunc 各自查询统计；查询失败时 pending/dlq/replayed 暴露 0、lag 暴露 0，
// 投递失败仍可通过 async_outbox_delivery_total{result=dead|poison} 告警。
//
// 所有 Gauge 共用一份带 TTL 的快照（metrics.SnapshotCache）：DB 变慢时每个抓取
// 周期只付一次查询超时，而不是三次；否则 /metrics 响应会慢到超过 Prometheus 的
// scrape_timeout，导致该进程**全部**指标丢失，连 db_pool_* 也一起消失
// （ENT-06 T6 实测：DB 池耗尽时 DBPoolExhaustion 反而无法触发）。
func (m *Metrics) RegisterGauges(stats func(context.Context) (outbox.Stats, error)) error {
	if m == nil || m.registerer == nil {
		return fmt.Errorf("async metrics: metrics is not initialized")
	}
	if stats == nil {
		return fmt.Errorf("async metrics: stats function is required")
	}
	cache := metrics.NewSnapshotCache[outbox.Stats](asyncStatsCacheTTL, stats)
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
		{
			name: "async_dlq_replayed_30m",
			help: "最近 30 分钟内被重放的死信行数；与 dlq_depth 同时 >0 表示重放未生效。",
			pick: func(s outbox.Stats, _ time.Time) float64 { return nonNegative(s.ReplayedRecently) },
		},
	}
	for _, counter := range counters {
		gauge := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: counter.name,
			Help: counter.help,
		}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), asyncStatsTimeout)
			defer cancel()
			// fresh 与 err 只用于可观测性：采集失败时沿用上一次成功快照，
			// 避免把「暂时采不到」显示成「积压为 0」。
			snapshot, _, _ := cache.Get(ctx)
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
var _ outbox.Observer = (*Metrics)(nil)
