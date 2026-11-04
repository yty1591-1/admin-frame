// Package observability 提供通知域核心指标的 Prometheus 适配（ENT-10 NT-08）。
//
// 本包实现 notification/app 的 Observer 端口并注册 notify_delivery Gauge，
// 由 notificationruntime 在装配期创建一次并注入所有通知用例；本包不参与
// 投递判定，只把应用层信号翻译为指标。全部标签为低基数枚举，
// 收件人等 PII 一律不得进入标签（SEC：指标脱敏硬约束）。
package observability

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/internal/notification/app"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
)

// deliveryGaugeCacheTTL 是 notify_delivery Gauge 查询快照的有效期
// （与 tenant_status / 平台 async 口径一致，防多目标抓取放大查询）。
const deliveryGaugeCacheTTL = 2 * time.Second

// Metrics 是通知域指标集，实现 app.Observer。
type Metrics struct {
	sendTotal       *prometheus.CounterVec
	rateLimited     prometheus.Counter
	auditEmitFailed prometheus.Counter
	requestTotal    *prometheus.CounterVec
	deliveryGauge   prometheus.GaugeFunc
	registerer      prometheus.Registerer
	registered      map[string]bool
}

// 编译期断言：指标集必须满足应用层观测端口。
var _ app.Observer = (*Metrics)(nil)

// NewMetrics 构造指标集；registerer 为 nil 时返回不可用实例
// （各方法退化为 no-op，方便单测直接构造）。
//
// deliveryGauge 需要仓储计数（CountDeliveriesByStatus）；reader 为 nil 时
// 跳过 Gauge 注册（单测 / 内存模式），仅注册计数器族。
func NewMetrics(registerer prometheus.Registerer,
	reader func(ctx context.Context) (map[domain.DeliveryStatus]int, error)) (*Metrics, error) {
	m := &Metrics{
		sendTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notify_send_total",
			Help: "渠道投递结果计数，按 channel / provider / result 分桶：accepted 受理成功 / retrying 退避重试 / failed 终态失败 / degraded 主备降级；result=failed 时 semantic 给出低基数语义码。",
		}, []string{"channel", "provider", "result", "semantic"}),
		rateLimited: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "notify_rate_limited_total",
			Help: "频控拒绝次数（供应商配额 / 收件人限额）；突增提示营销放量或轰炸尝试，键不含收件人信息。",
		}),
		auditEmitFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "notify_audit_emit_failed_total",
			Help: "通知域审计事件写出失败次数（拒发 / 补偿留痕丢失）；非零即应告警。",
		}),
		requestTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notify_request_total",
			Help: "通知单状态落定计数，按 scene / status 分桶；rejected 与 failed 需结合 runbook 排查（偏好 / 黑名单 / 供应商故障）。",
		}, []string{"scene", "status"}),
		registerer: registerer,
		registered: make(map[string]bool),
	}
	if registerer == nil {
		return m, nil
	}
	if reader != nil {
		gauge, gerr := newDeliveryGauge(reader)
		if gerr != nil {
			return nil, gerr
		}
		m.deliveryGauge = gauge
	}
	for _, c := range []prometheus.Collector{m.sendTotal, m.rateLimited, m.auditEmitFailed, m.requestTotal} {
		if err := registerer.Register(c); err != nil {
			return nil, fmt.Errorf("notification observability: register counter: %w", err)
		}
	}
	if m.deliveryGauge != nil {
		if err := registerer.Register(m.deliveryGauge); err != nil {
			return nil, fmt.Errorf("notification observability: register delivery gauge: %w", err)
		}
	}
	return m, nil
}

// newDeliveryGauge 构造投递明细状态 Gauge（带 2s 快照缓存）。
func newDeliveryGauge(reader func(ctx context.Context) (map[domain.DeliveryStatus]int, error)) (prometheus.GaugeFunc, error) {
	var (
		mu       sync.Mutex
		cached   float64
		lastRead time.Time
	)
	gauge := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "notify_delivery_active",
		Help: "非终态投递明细数（pending + retrying）；持续增长提示编排消费能力不足或供应商持续故障。",
	}, func() float64 {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(lastRead) < deliveryGaugeCacheTTL {
			return cached
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		counts, err := reader(ctx)
		if err != nil {
			// 查询失败沿用旧快照（对齐租户域口径），不阻塞抓取。
			return cached
		}
		lastRead = time.Now()
		cached = 0
		cached += float64(counts[domain.DeliveryPending])
		cached += float64(counts[domain.DeliveryRetrying])
		return cached
	})
	return gauge, nil
}

// DeliveryObserved 记录一次渠道投递结果（app.Observer 端口）。
func (m *Metrics) DeliveryObserved(channel domain.Channel, provider, result, semantic string) {
	if m == nil {
		return
	}
	m.sendTotal.WithLabelValues(string(channel), provider, result, semantic).Inc()
}

// RateLimited 记录一次频控拒绝（app.Observer 端口）。
func (m *Metrics) RateLimited() {
	if m == nil {
		return
	}
	m.rateLimited.Inc()
}

// RequestObserved 记录一次通知单状态落定（app.Observer 端口）。
func (m *Metrics) RequestObserved(scene string, status domain.RequestStatus) {
	if m == nil {
		return
	}
	m.requestTotal.WithLabelValues(scene, string(status)).Inc()
}

// AuditEmitFailed 记录一次审计写出失败（app.Observer 端口）。
func (m *Metrics) AuditEmitFailed() {
	if m == nil {
		return
	}
	m.auditEmitFailed.Inc()
}
