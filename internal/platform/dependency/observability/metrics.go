// Package observability 把依赖注册表的信号翻译为 Prometheus 指标
// （ENT-13：依赖指标可见）。全部标签低基数：name 是装配期登记的
// 有限集合，result 为枚举。
package observability

import (
	"fmt"

	"github.com/admin-frame/admin-frame/internal/platform/dependency"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics 是依赖韧性指标族，实现 dependency.Observer。
type Metrics struct {
	callTotal  *prometheus.CounterVec
	retryTotal *prometheus.CounterVec
}

// 编译期断言：Metrics 必须满足依赖观测端口。
var _ dependency.Observer = (*Metrics)(nil)

// NewMetrics 构造并注册指标族；registerer 为 nil 返回错误（观测不可静默缺失）。
func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		callTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dependency_call_total",
			Help: "外部依赖调用次数（result：ok / error / breaker_open）——breaker_open 持续增长提示下游持续故障。",
		}, []string{"name", "result"}),
		retryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dependency_retry_total",
			Help: "外部依赖重试触发次数（name）——重试率异常提示下游不稳或幂等误配。",
		}, []string{"name"}),
	}
	for _, c := range []prometheus.Collector{m.callTotal, m.retryTotal} {
		if err := registerer.Register(c); err != nil {
			return nil, fmt.Errorf("dependency observability: register collector: %w", err)
		}
	}
	return m, nil
}

// CallObserved 记录一次依赖调用。
func (m *Metrics) CallObserved(name, result string) {
	m.callTotal.WithLabelValues(name, result).Inc()
}

// RetryObserved 记录一次重试触发。
func (m *Metrics) RetryObserved(name string) {
	m.retryTotal.WithLabelValues(name).Inc()
}
