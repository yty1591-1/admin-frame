// Package observability 提供配置中心 M1 指标（规划 §5.6 / 附录 C）。
//
// 标签低基数：管理面按路由与结果，客户端按 namespace；env / service 由
// pkg/metrics 作为常量标签注入，本包不重复声明。敏感项不进入指标，只记录
// 结果与计数。
package observability

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/pkg/configclient"

	"github.com/prometheus/client_golang/prometheus"
)

// 管理面路由标签；与 transport/admin 注册的路由一一对应。
const (
	RouteNamespaces       = "namespaces"
	RouteItemsList        = "items.list"
	RouteItemsCreate      = "items.create"
	RouteItemsGet         = "items.get"
	RouteItemsUpdate      = "items.update"
	RouteVersionsList     = "versions.list"
	RouteVersionsGet      = "versions.get"
	RouteReleasesCreate   = "releases.create"
	RouteReleasesList     = "releases.list"
	RouteReleasesGet      = "releases.get"
	RouteReleasesSubmit   = "releases.submit"
	RouteReleasesApprove  = "releases.approve"
	RouteReleasesPublish  = "releases.publish"
	RouteReleasesRollback = "releases.rollback"
)

// Metrics 是配置中心指标族，实现 transport/admin.RequestObserver。
type Metrics struct {
	adminRequests *prometheus.CounterVec
	adminDuration *prometheus.HistogramVec
	publishTotal  *prometheus.CounterVec
	rollbackTotal *prometheus.CounterVec
	pending       *prometheus.GaugeVec

	clientFetch       *prometheus.CounterVec
	clientSnapshotAge *prometheus.GaugeVec
	clientVersion     *prometheus.GaugeVec
	clientCorrupt     prometheus.Counter
	clientColdStart   prometheus.Counter
	clientWatchDrop   prometheus.Counter

	mu          sync.Mutex
	lastSuccess map[string]time.Time
}

// New 构造并注册指标族；registerer 为 nil 返回错误，避免观测静默缺失。
func New(registerer prometheus.Registerer) (*Metrics, error) {
	if registerer == nil {
		return nil, fmt.Errorf("configcenter observability: registerer is required")
	}
	m := &Metrics{
		adminRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "config_admin_request_total",
			Help: "配置中心管理面请求量（route：路由；result：success / conflict / denied / fail）。",
		}, []string{"route", "result"}),
		adminDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "config_admin_request_duration_seconds",
			Help:    "配置中心管理面请求耗时（按路由）。",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.5, 1, 2},
		}, []string{"route"}),
		publishTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "config_release_publish_total",
			Help: "配置发布结果（result：success / conflict / fail）。",
		}, []string{"result"}),
		rollbackTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "config_release_rollback_total",
			Help: "配置回滚结果（result：success / conflict / fail）。",
		}, []string{"result"}),
		pending: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "config_release_pending_approval",
			Help: "待审批发布数量（按 namespace）；持续大于 0 提示审批积压。",
		}, []string{"namespace"}),
		clientFetch: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "config_client_fetch_total",
			Help: "客户端快照拉取结果（result：success / fail）。",
		}, []string{"result"}),
		clientSnapshotAge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "config_client_snapshot_age_seconds",
			Help: "距最近一次成功刷新的秒数；持续增长表示中心不可用或刷新失败。",
		}, []string{"namespace"}),
		clientVersion: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "config_client_version",
			Help: "当前生效 Release 序号（release_no）。",
		}, []string{"namespace"}),
		clientCorrupt: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "config_client_snapshot_corrupt_total",
			Help: "快照验签或完整性校验失败次数（应为 0）。",
		}),
		clientColdStart: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "config_client_cold_start_degraded_total",
			Help: "冷启动无可信快照、回退代码默认值的进程次数。",
		}),
		clientWatchDrop: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "config_client_watch_dropped_total",
			Help: "Watch 订阅者积压导致的事件丢弃次数（消费过慢）。",
		}),
		lastSuccess: make(map[string]time.Time),
	}
	for _, c := range []prometheus.Collector{
		m.adminRequests, m.adminDuration, m.publishTotal, m.rollbackTotal, m.pending,
		m.clientFetch, m.clientSnapshotAge, m.clientVersion, m.clientCorrupt, m.clientColdStart,
		m.clientWatchDrop,
	} {
		if err := registerer.Register(c); err != nil {
			return nil, fmt.Errorf("configcenter observability: register collector: %w", err)
		}
	}
	return m, nil
}

// ObserveAdminRequest 记录一次管理面请求；route 使用本包 Route* 常量。
func (m *Metrics) ObserveAdminRequest(route string, status int, elapsed time.Duration) {
	if m == nil {
		return
	}
	result := adminResult(status)
	m.adminRequests.WithLabelValues(route, result).Inc()
	m.adminDuration.WithLabelValues(route).Observe(elapsed.Seconds())
	switch route {
	case RouteReleasesPublish:
		m.publishTotal.WithLabelValues(result).Inc()
	case RouteReleasesRollback:
		m.rollbackTotal.WithLabelValues(result).Inc()
	}
}

// SetPendingApproval 更新某 namespace 的待审批数量。
func (m *Metrics) SetPendingApproval(namespace string, pending int) {
	if m == nil || namespace == "" {
		return
	}
	m.pending.WithLabelValues(namespace).Set(float64(pending))
}

// ClientColdStartDegraded 记录一次冷启动降级（进程内只应调用一次）。
func (m *Metrics) ClientColdStartDegraded() {
	if m == nil {
		return
	}
	m.clientColdStart.Inc()
}

// ClientWatchDropped 记录一次 Watch 订阅者事件丢弃。
func (m *Metrics) ClientWatchDropped() {
	if m == nil {
		return
	}
	m.clientWatchDrop.Inc()
}

// ObserveClientRefresh 记录一次快照刷新结果。
//
// 失败时保留上次成功时间计算快照年龄；验签 / 完整性错误单独计入 corrupt。
func (m *Metrics) ObserveClientRefresh(event configclient.RefreshEvent) {
	if m == nil {
		return
	}
	if event.Err != nil {
		m.clientFetch.WithLabelValues("fail").Inc()
		if isSnapshotCorrupt(event.Err) {
			m.clientCorrupt.Inc()
		}
	} else {
		m.clientFetch.WithLabelValues("success").Inc()
	}
	m.mu.Lock()
	namespace := event.Snapshot.Namespace
	if event.Err == nil && namespace != "" {
		m.lastSuccess[namespace] = event.At
	}
	last := m.lastSuccess[namespace]
	m.mu.Unlock()

	if namespace == "" {
		return
	}
	age := time.Duration(0)
	if event.Err == nil {
		m.clientVersion.WithLabelValues(namespace).Set(float64(event.Snapshot.ReleaseNo))
	} else if !last.IsZero() {
		age = event.At.Sub(last)
		if age < 0 {
			age = 0
		}
	} else {
		// 从未成功刷新：不写年龄，避免用进程启动时长污染 stale 告警。
		return
	}
	m.clientSnapshotAge.WithLabelValues(namespace).Set(age.Seconds())
}

// adminResult 把 HTTP 状态映射为低基数结果标签。
func adminResult(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "success"
	case status == 409:
		return "conflict"
	case status == 401 || status == 403:
		return "denied"
	default:
		return "fail"
	}
}

// isSnapshotCorrupt 判断错误是否属于快照本身不可信（而非中心不可用）。
func isSnapshotCorrupt(err error) bool {
	for _, target := range []error{
		configclient.ErrSnapshotUnsigned,
		configclient.ErrSnapshotUnknownKeyID,
		configclient.ErrSnapshotHashMismatch,
		configclient.ErrSnapshotSizeMismatch,
		configclient.ErrSnapshotValueMismatch,
		configclient.ErrSnapshotSignatureInvalid,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
