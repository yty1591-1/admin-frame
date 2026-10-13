package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// NewCounter 创建并注册 CounterVec（自动加 Namespace 与常量标签）。
func (m *Metrics) NewCounter(name, help string, labelNames ...string) (*prometheus.CounterVec, error) {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: m.namespace,
		Name:      name,
		Help:      help,
	}, labelNames)
	if err := m.registerer.Register(c); err != nil {
		return nil, err
	}
	return c, nil
}

// NewHistogram 创建并注册 HistogramVec。
func (m *Metrics) NewHistogram(name, help string, buckets []float64, labelNames ...string) (*prometheus.HistogramVec, error) {
	if len(buckets) == 0 {
		buckets = prometheus.DefBuckets
	}
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: m.namespace,
		Name:      name,
		Help:      help,
		Buckets:   buckets,
	}, labelNames)
	if err := m.registerer.Register(h); err != nil {
		return nil, err
	}
	return h, nil
}

// NewGauge 创建并注册 GaugeVec。
func (m *Metrics) NewGauge(name, help string, labelNames ...string) (*prometheus.GaugeVec, error) {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: m.namespace,
		Name:      name,
		Help:      help,
	}, labelNames)
	if err := m.registerer.Register(g); err != nil {
		return nil, err
	}
	return g, nil
}
