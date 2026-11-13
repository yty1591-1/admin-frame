package observability

import (
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// DualReadMetrics 是迁移期双读指标（CC-F-42）。
//
// 与 Metrics 分开注册：双读在所有进程启动期执行，而 Metrics 只在装配了配置
// 中心运行时的进程创建，避免同名重复注册。差异只打点 key 与哈希，不含取值。
type DualReadMetrics struct {
	mismatch *prometheus.GaugeVec
	degraded *prometheus.GaugeVec
}

// NewDualRead 构造并注册双读指标；registerer 为 nil 返回错误。
//
// 同一进程内可重复构造：同名已注册时复用既有采集器，使启动期一次性对比与
// 运行期周期对比写同一组指标，而不是因重复注册直接失败。
func NewDualRead(registerer prometheus.Registerer) (*DualReadMetrics, error) {
	if registerer == nil {
		return nil, fmt.Errorf("configcenter dual-read observability: registerer is required")
	}
	mismatch, err := registerGaugeVec(registerer, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "config_dual_read_mismatch",
		Help: "迁移期文件基线与配置中心取值差异（1 差异 / 0 一致），按 namespace + key。",
	}, []string{"namespace", "key"}))
	if err != nil {
		return nil, fmt.Errorf("configcenter dual-read observability: register mismatch: %w", err)
	}
	degraded, err := registerGaugeVec(registerer, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "config_dual_read_degraded",
		Help: "配置中心不可用、双读降级为基线取值（1 降级 / 0 正常）。",
	}, []string{"namespace"}))
	if err != nil {
		return nil, fmt.Errorf("configcenter dual-read observability: register degraded: %w", err)
	}
	return &DualReadMetrics{mismatch: mismatch, degraded: degraded}, nil
}

// registerGaugeVec 注册采集器；同名已注册时复用已有实例（同一 registry 内只应有一份）。
func registerGaugeVec(registerer prometheus.Registerer,
	collector *prometheus.GaugeVec) (*prometheus.GaugeVec, error) {
	if err := registerer.Register(collector); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			if existing, ok := already.ExistingCollector.(*prometheus.GaugeVec); ok {
				return existing, nil
			}
		}
		return nil, err
	}
	return collector, nil
}

// SetMismatch 更新单键差异状态。
func (m *DualReadMetrics) SetMismatch(namespace, key string, mismatched bool) {
	if m == nil || namespace == "" || key == "" {
		return
	}
	value := 0.0
	if mismatched {
		value = 1
	}
	m.mismatch.WithLabelValues(namespace, key).Set(value)
}

// SetDegraded 更新中心可用性状态。
func (m *DualReadMetrics) SetDegraded(namespace string, degraded bool) {
	if m == nil || namespace == "" {
		return
	}
	value := 0.0
	if degraded {
		value = 1
	}
	m.degraded.WithLabelValues(namespace).Set(value)
}
