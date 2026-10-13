package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// RegisterBuildInfo 注册 build_info gauge（值为 1），标签含 version/commit/build_time。
// service/channel/env 已由 Registry 常量标签注入。
// 重复注册返回错误（进程启动应只调用一次）。
func (m *Metrics) RegisterBuildInfo(version, commit, buildTime string) error {
	if m == nil {
		return nil
	}
	if version == "" {
		version = "unknown"
	}
	if commit == "" {
		commit = "unknown"
	}
	if buildTime == "" {
		buildTime = "unknown"
	}
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: m.namespace,
		Name:      "build_info",
		Help:      "Build identity for this process (always 1).",
	}, []string{"version", "commit", "build_time"})
	if err := m.registerer.Register(g); err != nil {
		return err
	}
	g.WithLabelValues(version, commit, buildTime).Set(1)
	return nil
}

// MustRegisterBuildInfo 同 RegisterBuildInfo，失败时 panic。
func (m *Metrics) MustRegisterBuildInfo(version, commit, buildTime string) {
	if err := m.RegisterBuildInfo(version, commit, buildTime); err != nil {
		panic(err)
	}
}
