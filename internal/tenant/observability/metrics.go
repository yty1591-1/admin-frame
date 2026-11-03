// Package observability 提供租户域核心指标的 Prometheus 适配（ENT-09 T-16）。
//
// 本包实现 tenant/app 的 Observer 端口并注册 tenant_status Gauge，
// 由 tenantruntime 在装配期创建一次并注入所有租户用例；本包不参与
// 守卫判定与状态迁移，只把应用层信号翻译为指标。
package observability

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// statusGaugeCacheTTL 是 tenant_status Gauge 查询快照的有效期。
//
// 与 identity / 平台 async 口径一致：同一抓取周期内只查一次库，
// 避免多目标抓取放大查询压力。
const statusGaugeCacheTTL = 2 * time.Second

// Metrics 是租户域指标集，实现 app.Observer。
type Metrics struct {
	guardDecisions  *prometheus.CounterVec
	guardCacheHits  *prometheus.CounterVec
	transitions     *prometheus.CounterVec
	auditEmitFailed prometheus.Counter
	registerer      prometheus.Registerer
	registered      map[string]bool
	mu              sync.Mutex
}

// 编译期断言：指标集必须满足应用层观测端口。
var _ app.Observer = (*Metrics)(nil)

// NewMetrics 构造指标集并注册计数器；registerer 为 nil 时返回不可用实例
// （各方法退化为 no-op，方便单测直接构造）。
func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		guardDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tenant_guard_decisions_total",
			Help: "租户状态守卫判定次数，按 result 分桶：allow 放行 / deny 明确拒绝 / error 无法判定（fail closed）。",
		}, []string{"result"}),
		guardCacheHits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tenant_guard_cache_hit_total",
			Help: "守卫缓存查询次数，hit=true 命中 / false 回源数据库；命中率骤降提示缓存被冲刷或 TTL 被调小。",
		}, []string{"hit"}),
		transitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tenant_transition_total",
			Help: "租户状态迁移成功次数，按 from / to 分桶；pending→active 是新租户上量信号，*→suspended 需核对停用公告。",
		}, []string{"from", "to"}),
		auditEmitFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tenant_audit_emit_failed_total",
			Help: "审计事件发送失败次数（Outbox 写入失败，业务事务随之回滚）；非零即应告警。",
		}),
		registerer: registerer,
		registered: make(map[string]bool),
	}
	if registerer == nil {
		return m, nil
	}
	for _, c := range []prometheus.Collector{m.guardDecisions, m.guardCacheHits, m.transitions, m.auditEmitFailed} {
		if err := registerer.Register(c); err != nil {
			return nil, fmt.Errorf("tenant observability: register counter: %w", err)
		}
	}
	return m, nil
}

// GuardDecision 记录一次守卫判定（allow / deny / error）。
func (m *Metrics) GuardDecision(result string) {
	if m == nil {
		return
	}
	m.guardDecisions.WithLabelValues(result).Inc()
}

// GuardCacheHit 记录一次守卫缓存查询结果。
func (m *Metrics) GuardCacheHit(hit bool) {
	if m == nil {
		return
	}
	m.guardCacheHits.WithLabelValues(fmt.Sprintf("%t", hit)).Inc()
}

// Transition 记录一次成功落库的状态迁移。
func (m *Metrics) Transition(from, to string) {
	if m == nil {
		return
	}
	m.transitions.WithLabelValues(from, to).Inc()
}

// AuditEmitFailed 记录一次审计事件发送失败。
func (m *Metrics) AuditEmitFailed() {
	if m == nil {
		return
	}
	m.auditEmitFailed.Inc()
}

// RegisterStatusGauge 注册 tenant_status（按状态分桶的租户数 Gauge）。
//
// counts 回调在抓取时调用，结果缓存 statusGaugeCacheTTL；查询失败沿用
// 上一次快照（指标停滞可观测，而不是跳变成 0 误导容量视图）。
// 重复注册是 no-op（幂等），方便多个 runtime 复用同一 Metrics。
func (m *Metrics) RegisterStatusGauge(counts func(context.Context) (map[domain.Status]int, error)) error {
	if m == nil {
		return fmt.Errorf("tenant observability: metrics is not initialized")
	}
	if counts == nil {
		return fmt.Errorf("tenant observability: status counts function is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registered["tenant_status"] {
		return nil
	}
	if m.registerer == nil {
		return fmt.Errorf("tenant observability: metrics is not initialized")
	}

	g := &statusGauge{
		desc: prometheus.NewDesc("tenant_status",
			"当前按生命周期状态统计的租户数（pending / active / suspended / terminated）。",
			[]string{"status"}, nil),
		counts: counts,
	}
	if err := m.registerer.Register(g); err != nil {
		return fmt.Errorf("tenant observability: register status gauge: %w", err)
	}
	m.registered["tenant_status"] = true
	return nil
}

// statusGauge 是带查询缓存的按状态 Gauge（自定义 Collector）。
//
// GaugeFunc 只支持无标签指标，状态分桶需要 ConstMetric；缓存避免每次抓取
// 都打一次聚合查询（多目标抓取时放大）。
type statusGauge struct {
	desc *prometheus.Desc
	// counts 状态计数查询；由 Metrics 注入带超时的闭包。
	counts func(context.Context) (map[domain.Status]int, error)

	mu        sync.Mutex
	cache     map[string]float64
	cacheOK   bool
	expiresAt time.Time
}

// Describe 实现 prometheus.Collector。
func (g *statusGauge) Describe(ch chan<- *prometheus.Desc) { ch <- g.desc }

// Collect 实现 prometheus.Collector：缓存有效直接回放，否则查询并刷新。
func (g *statusGauge) Collect(ch chan<- prometheus.Metric) {
	counts := g.snapshot()
	for status, count := range counts {
		ch <- prometheus.MustNewConstMetric(g.desc, prometheus.GaugeValue, count, status)
	}
}

// snapshot 返回缓存的状态计数；缓存过期时查询刷新，查询失败沿用旧值。
func (g *statusGauge) snapshot() map[string]float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if g.cacheOK && now.Before(g.expiresAt) {
		return g.cache
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	counts, err := g.counts(ctx)
	if err != nil {
		// 查询失败：沿用旧快照（可能为空 = 本周期不暴露），不跳变成 0。
		return g.cache
	}
	out := make(map[string]float64, len(counts))
	for status, count := range counts {
		out[string(status)] = float64(count)
	}
	g.cache = out
	g.cacheOK = true
	g.expiresAt = now.Add(statusGaugeCacheTTL)
	return out
}
