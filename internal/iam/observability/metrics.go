// Package observability 提供 IAM 鉴权链路的 Prometheus 指标适配。
//
// 本包只做「观测接口 → 指标」的翻译，不参与鉴权决策；由 cmd 在装配期
// 创建一次并同时注入 cache.SnapshotCache、app.AuthorizeService 与
// security.Authorizer，保证三类观测共享同一 Registry（docs/rbac-iam.md §9）。
package observability

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/metrics"
)

// outboxStatsCacheTTL 是 IAM Outbox 两个 Gauge 共用快照的有效期。
//
// 取值与平台 async Gauge 对齐：同一抓取周期内只查一次库，DB 慢时 /metrics 不会
// 因 Gauge 个数而叠加超时。
const outboxStatsCacheTTL = 2 * time.Second

// Metrics 是 IAM 鉴权指标集，实现缓存 / 加载 / 决策 / shadow 观测接口。
type Metrics struct {
	cacheTotal     *prometheus.CounterVec
	loadDuration   *prometheus.HistogramVec
	snapshotSize   *prometheus.HistogramVec
	decisions      *prometheus.CounterVec
	invalidateFail *prometheus.CounterVec
	shadowMismatch *prometheus.CounterVec
	loadErrors     *prometheus.CounterVec
	roleChanges    *prometheus.CounterVec
	bindingChanges *prometheus.CounterVec
	// constraintChecks RBAC2 约束检查结果；kind 为检查类别或违规类型，低基数。
	constraintChecks *prometheus.CounterVec
	outboxDelivery   *prometheus.CounterVec
	registerer       prometheus.Registerer
}

// NewMetrics 创建并注册指标；重复注册视为装配错误（fail-fast）。
func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	if registerer == nil {
		return nil, fmt.Errorf("iam observability: registerer is required")
	}
	m := &Metrics{
		registerer: registerer,
		cacheTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "iam_authz_cache_total",
			Help: "IAM 鉴权快照缓存读取次数，按层与结果分组。",
		}, []string{"layer", "result"}),
		loadDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "iam_authz_duration_seconds",
			Help:    "IAM 鉴权快照加载耗时，按端别与来源分组。",
			Buckets: []float64{0.0005, 0.001, 0.003, 0.005, 0.01, 0.03, 0.05, 0.1, 0.3, 1},
		}, []string{"channel", "source"}),
		snapshotSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "iam_authz_snapshot_permissions",
			Help:    "IAM 单主体快照权限点数量，用于发现角色膨胀。",
			Buckets: []float64{0, 5, 10, 20, 50, 100, 200, 500},
		}, []string{"channel"}),
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "iam_authz_decisions_total",
			Help: "IAM 动态鉴权决策次数，result=allow|deny|unavailable。",
		}, []string{"channel", "result"}),
		invalidateFail: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "iam_cache_invalidate_failed_total",
			Help: "IAM 缓存删除失败次数；依赖 TTL 过期兜底。",
		}, []string{"layer"}),
		shadowMismatch: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "iam_shadow_mismatch_total",
			Help: "IAM shadow 模式与静态角色表判定差异次数。",
		}, []string{"channel", "static_allow", "iam_allow"}),
		loadErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "iam_authz_load_errors_total",
			Help: "IAM 快照加载 / 缓存写失败次数，按阶段分组。",
		}, []string{"stage"}),
		roleChanges: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "iam_role_changes_total",
			Help: "IAM 角色变更次数，按端别、变更类型与结果分组。",
		}, []string{"channel", "operation", "outcome"}),
		bindingChanges: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "iam_binding_changes_total",
			Help: "IAM 主体绑定变更次数，按端别、变更类型与结果分组。",
		}, []string{"channel", "operation", "outcome"}),
		constraintChecks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "iam_constraint_checks_total",
			Help: "IAM RBAC2 约束检查次数；kind=检查类别或违规类型，result=pass|shadow|enforce|error。",
		}, []string{"kind", "result"}),
		outboxDelivery: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "iam_outbox_delivery_total",
			Help: "IAM 事务性 Outbox 投递结果，result=success|retry|dead|poison。",
		}, []string{"result"}),
	}
	for _, collector := range []prometheus.Collector{
		m.cacheTotal, m.loadDuration, m.snapshotSize, m.decisions,
		m.invalidateFail, m.shadowMismatch, m.loadErrors,
		m.roleChanges, m.bindingChanges, m.constraintChecks, m.outboxDelivery,
	} {
		if err := registerer.Register(collector); err != nil {
			return nil, fmt.Errorf("iam observability: register metrics: %w", err)
		}
	}
	// 预创建投递结果标签，保证 iam_outbox_delivery_total 在无投递时也能被采集，
	// 避免告警表达式因指标缺失而静默失效（尤其是 dead / poison）。
	for _, result := range []string{"success", "retry", "dead", "poison"} {
		m.outboxDelivery.WithLabelValues(result)
	}
	return m, nil
}

// RegisterBreakglassGauge 注册 iam_breakglass_active（GaugeFunc）。
//
// 采集时实时统计有效破玻璃绑定数，避免常驻后台任务；查询失败按 0 暴露并
// 由 iam_authz_load_errors_total 之外的日志告警排查，绝不放行为「有权限」。
func (m *Metrics) RegisterBreakglassGauge(counter func() (int, error)) error {
	if m == nil || m.registerer == nil {
		return fmt.Errorf("iam observability: metrics is not initialized")
	}
	if counter == nil {
		return fmt.Errorf("iam observability: breakglass counter is required")
	}
	gauge := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "iam_breakglass_active",
		Help: "IAM 活跃破玻璃绑定数；>0 触发 P1 告警（docs/rbac-iam.md §8.5）。",
	}, func() float64 {
		value, err := counter()
		if err != nil || value < 0 {
			return 0
		}
		return float64(value)
	})
	if err := m.registerer.Register(gauge); err != nil {
		return fmt.Errorf("iam observability: register breakglass gauge: %w", err)
	}
	return nil
}

// OnOutboxDelivered 实现 app.AuditTopicObserver 依赖的指标出口。
func (m *Metrics) OnOutboxDelivered(_ context.Context, result string) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	m.outboxDelivery.WithLabelValues(result).Inc()
}

// RegisterOutboxGauges 注册 iam_outbox_pending 与 iam_outbox_lag_seconds。
//
// 两个 GaugeFunc 共用一次带 TTL 的统计调用：查询失败按「不可观测」暴露 0，但投递失败
// 会通过 iam_outbox_delivery_total{result=dead|poison} 与日志暴露，不依赖本指标。
//
// 共享快照同时避免「每个 Gauge 各查一次」把 /metrics 响应拖到超过 scrape_timeout：
// DB 慢时该进程的全部指标会一起丢失（ENT-06 T6 实测）。
func (m *Metrics) RegisterOutboxGauges(stats func() (int, float64, error)) error {
	if m == nil || m.registerer == nil {
		return fmt.Errorf("iam observability: metrics is not initialized")
	}
	if stats == nil {
		return fmt.Errorf("iam observability: outbox stats function is required")
	}
	type outboxSnapshot struct {
		pending int
		lag     float64
	}
	cache := metrics.NewSnapshotCache[outboxSnapshot](outboxStatsCacheTTL, func(context.Context) (outboxSnapshot, error) {
		count, lag, err := stats()
		if err != nil {
			return outboxSnapshot{}, err
		}
		return outboxSnapshot{pending: count, lag: lag}, nil
	})
	snapshot := func() outboxSnapshot {
		value, _, _ := cache.Get(context.Background())
		return value
	}
	pending := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "iam_outbox_pending",
		Help: "IAM Outbox 未投递事件数（含重试等待与死信）；持续 >0 说明投递受阻。",
	}, func() float64 {
		value := snapshot()
		if value.pending < 0 {
			return 0
		}
		return float64(value.pending)
	})
	lag := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "iam_outbox_lag_seconds",
		Help: "IAM Outbox 最老未投递事件的积压秒数；持续增长触发 P2 告警。",
	}, func() float64 {
		value := snapshot()
		if value.lag < 0 {
			return 0
		}
		return value.lag
	})
	for name, collector := range map[string]prometheus.Collector{
		"pending": pending,
		"lag":     lag,
	} {
		if err := m.registerer.Register(collector); err != nil {
			return fmt.Errorf("iam observability: register outbox %s gauge: %w", name, err)
		}
	}
	return nil
}

// OnCache 实现 cache.Observer。
func (m *Metrics) OnCache(layer iampcache.Layer, result string) {
	if m == nil {
		return
	}
	m.cacheTotal.WithLabelValues(string(layer), result).Inc()
}

// OnInvalidateFailure 实现 cache.Observer。
func (m *Metrics) OnInvalidateFailure(layer iampcache.Layer) {
	if m == nil {
		return
	}
	m.invalidateFail.WithLabelValues(string(layer)).Inc()
}

// OnSnapshotLoad 实现 app.Observer。
func (m *Metrics) OnSnapshotLoad(_ context.Context, channel string, source iampcache.Layer, elapsed time.Duration) {
	if m == nil {
		return
	}
	m.loadDuration.WithLabelValues(channel, string(source)).Observe(elapsed.Seconds())
}

// OnSnapshot 实现 app.Observer。
func (m *Metrics) OnSnapshot(_ context.Context, channel string, permissions int) {
	if m == nil {
		return
	}
	m.snapshotSize.WithLabelValues(channel).Observe(float64(permissions))
}

// OnSnapshotError 实现 app.Observer。
func (m *Metrics) OnSnapshotError(_ context.Context, stage string, _ error) {
	if m == nil {
		return
	}
	m.loadErrors.WithLabelValues(stage).Inc()
}

// OnDecision 实现 security.DecisionObserver。
func (m *Metrics) OnDecision(_ context.Context, channel, result string) {
	if m == nil {
		return
	}
	m.decisions.WithLabelValues(channel, result).Inc()
}

// OnMismatch 实现 security.ShadowObserver。
func (m *Metrics) OnMismatch(_ context.Context, mismatch security.Mismatch) {
	if m == nil {
		return
	}
	m.shadowMismatch.WithLabelValues(
		mismatch.Channel,
		strconv.FormatBool(mismatch.StaticAllow),
		strconv.FormatBool(mismatch.IAMAllow),
	).Inc()
}

// OnLoadError 实现 security.ShadowObserver；shadow 加载失败按 stage=shadow 计数。
func (m *Metrics) OnLoadError(context.Context, string, error) {
	if m == nil {
		return
	}
	m.loadErrors.WithLabelValues("shadow").Inc()
}

// OnConstraintCheck 实现 app.ConstraintObserver。
func (m *Metrics) OnConstraintCheck(_ context.Context, kind, result string) {
	if m == nil {
		return
	}
	if kind == "" {
		kind = "unknown"
	}
	if result == "" {
		result = "unknown"
	}
	m.constraintChecks.WithLabelValues(kind, result).Inc()
}

// OnChange 记录一次授权变更（由 ChangeRecorder 装饰器在审计落盘后调用）。
//
// 只登记角色与绑定两类 action：其它 action 不在此处理，避免指标语义漂移。
// labels 刻意不含 subject_id / role_id，遵守 §11.2 的高基数禁令。
func (m *Metrics) OnChange(_ context.Context, action, channel, operation, outcome string) {
	if m == nil {
		return
	}
	if operation == "" {
		operation = "unknown"
	}
	switch action {
	case audit.ActionRBACRoleChange:
		m.roleChanges.WithLabelValues(channel, operation, outcome).Inc()
	case audit.ActionRBACBindingChange:
		m.bindingChanges.WithLabelValues(channel, operation, outcome).Inc()
	}
}

// ChangeRecorder 是 app.ChangeRecorder 的观测装饰器。
//
// 顺序语义：先写审计，再计数。审计失败时业务会失败关闭，此时计数记为 failure，
// 因此 `iam_role_changes_total{outcome=failure}` 正是「审计/变更失败率」。
type ChangeRecorder struct {
	Inner   app.ChangeRecorder
	Metrics *Metrics
}

// Record 写入审计并统计变更结果；任何失败都按 failure 计数后原样返回。
func (r ChangeRecorder) Record(ctx context.Context, event audit.Event) error {
	var recordErr error
	if r.Inner == nil {
		recordErr = apperr.Internal("audit recorder is not configured", nil)
	} else {
		recordErr = r.Inner.Record(ctx, event)
	}
	outcome := string(audit.OutcomeSuccess)
	if recordErr != nil {
		outcome = string(audit.OutcomeFailure)
	}
	operation := event.Attrs[app.AttrOperation]
	if r.Metrics != nil {
		r.Metrics.OnChange(ctx, event.Action, event.Channel, operation, outcome)
	}
	return recordErr
}

// 编译期断言：装饰器必须满足用例侧端口。
var _ app.ChangeRecorder = ChangeRecorder{}
