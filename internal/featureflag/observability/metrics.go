// Package observability 提供 featureflag 域指标（ENT-11 FF-06）。
//
// 标签低基数：flag 是有限集合（门禁守护 key 登记），result / state 为
// 枚举值；不落主体标识（SubjectID 只进哈希不进标签）。
package observability

import (
	"fmt"

	"github.com/admin-frame/admin-frame/internal/featureflag/app"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics 是 featureflag 指标族，实现 app.EvaluationObserver。
type Metrics struct {
	evalTotal  *prometheus.CounterVec
	cacheTotal *prometheus.CounterVec
	expired    prometheus.Counter
}

// 编译期断言：Metrics 必须满足评估观测端口。
var _ app.EvaluationObserver = (*Metrics)(nil)

// NewMetrics 构造并注册指标族；registerer 为 nil 返回错误（装配期即失败，
// 观测不可静默缺失——对齐 notification 观测口径）。
func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		evalTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "featureflag_eval_total",
			Help: "特性开关评估次数（result：hit 命中 / miss 未命中 / killed 被总开关禁用 / unknown 未知键或故障）。",
		}, []string{"flag", "result"}),
		cacheTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "featureflag_cache_total",
			Help: "评估缓存命中状态（kind：flag；state：hit / miss / expired）——持续 miss 提示评估直查库热点。",
		}, []string{"kind", "state"}),
		expired: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "featureflag_expired_total",
			Help: "到期自动禁用的 flag 数（到期治理任务产出；持续增长属正常生命周期）。",
		}),
	}
	for _, c := range []prometheus.Collector{m.evalTotal, m.cacheTotal, m.expired} {
		if err := registerer.Register(c); err != nil {
			return nil, fmt.Errorf("featureflag observability: register collector: %w", err)
		}
	}
	return m, nil
}

// EvalObserved 记录一次评估。
func (m *Metrics) EvalObserved(flagKey, result string) {
	m.evalTotal.WithLabelValues(flagKey, result).Inc()
}

// CacheObserved 记录缓存命中状态。
func (m *Metrics) CacheObserved(kind, state string) {
	m.cacheTotal.WithLabelValues(kind, state).Inc()
}

// ExpireObserved 由到期任务调用（app 层当前未接——占位接口一致性）。
func (m *Metrics) ExpireObserved() {
	m.expired.Inc()
}
