package metrics

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// Options 控制 Registry 与内置采集器。
type Options struct {
	// Namespace 指标名前缀（Prometheus namespace）；空表示不加前缀。
	// 仅允许 [a-zA-Z0-9_]，非法字符将被替换为下划线。
	Namespace string

	// Service / Channel / Env 写入所有指标的常量标签（OBS 基线）。
	// Service 必填；Channel、Env 建议填写，空则不加入该标签。
	Service string
	Channel string
	Env     string

	// RegisterRuntime 注册 Go runtime 采集器（goroutine、GC 等）；默认 true。
	RegisterRuntime *bool
	// RegisterProcess 注册 process 采集器；默认 true（非 Linux 上可能部分为空）。
	RegisterProcess *bool

	// Registerer / Gatherer 可选外部注册表；同时为空时创建私有 Registry。
	// 若只提供其一则返回错误。
	Registerer prometheus.Registerer
	Gatherer   prometheus.Gatherer

	// NativeHistogram 预留：当前客户端版本以经典 Histogram 为主；忽略该字段。
	// 保留以便后续升级不破坏 Options 形态。
	NativeHistogram bool
}

func (o Options) validate() error {
	if strings.TrimSpace(o.Service) == "" {
		return fmt.Errorf("metrics: Service is required")
	}
	if (o.Registerer == nil) != (o.Gatherer == nil) {
		return fmt.Errorf("metrics: Registerer and Gatherer must both be set or both nil")
	}
	return nil
}

func (o Options) constLabels() prometheus.Labels {
	labels := prometheus.Labels{"service": o.Service}
	if o.Channel != "" {
		labels["channel"] = o.Channel
	}
	if o.Env != "" {
		labels["env"] = o.Env
	}
	return labels
}

func (o Options) runtimeEnabled() bool {
	if o.RegisterRuntime == nil {
		return true
	}
	return *o.RegisterRuntime
}

func (o Options) processEnabled() bool {
	if o.RegisterProcess == nil {
		return true
	}
	return *o.RegisterProcess
}

// sanitizeNamespace 将 namespace 规范为 Prometheus 合法标识。
func sanitizeNamespace(ns string) string {
	if ns == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(ns))
	for _, r := range ns {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_':
			b.WriteRune(r)
		default:
			// '-', '.', '/' 及其他非法字符统一替换为下划线。
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out != "" && out[0] >= '0' && out[0] <= '9' {
		out = "_" + out
	}
	return out
}

// Bool 返回 bool 指针，便于覆盖 RegisterRuntime / RegisterProcess 默认值。
func Bool(v bool) *bool { return &v }
