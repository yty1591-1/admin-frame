package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics 是进程级指标门面：私有（或注入）Registry、HTTP RED、构建信息与工厂方法。
type Metrics struct {
	namespace     string
	registerer    prometheus.Registerer
	rawRegisterer prometheus.Registerer
	service       string
	env           string
	gatherer      prometheus.Gatherer
	http          *HTTP
}

// New 创建 Metrics。Service 必填；默认注册 Go runtime 与 process 采集器。
func New(opts Options) (*Metrics, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}

	var (
		registerer prometheus.Registerer
		gatherer   prometheus.Gatherer
	)
	if opts.Registerer != nil {
		registerer = opts.Registerer
		gatherer = opts.Gatherer
	} else {
		reg := prometheus.NewRegistry()
		registerer = reg
		gatherer = reg
	}

	wrapped := prometheus.WrapRegistererWith(opts.constLabels(), registerer)
	ns := sanitizeNamespace(opts.Namespace)

	if opts.runtimeEnabled() {
		if err := wrapped.Register(collectors.NewGoCollector()); err != nil {
			return nil, err
		}
	}
	if opts.processEnabled() {
		if err := wrapped.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
			return nil, err
		}
	}

	m := &Metrics{
		namespace:     ns,
		registerer:    wrapped,
		rawRegisterer: registerer,
		service:       opts.Service,
		env:           opts.Env,
		gatherer:      gatherer,
	}
	httpMetrics, err := newHTTP(wrapped, ns)
	if err != nil {
		return nil, err
	}
	m.http = httpMetrics
	return m, nil
}

// Registerer 返回带常量标签的 Registerer，供业务自定义指标注册。
func (m *Metrics) Registerer() prometheus.Registerer {
	if m == nil {
		return nil
	}
	return m.registerer
}

// RegistererWithoutChannel 返回只注入 service / env 常量标签的 Registerer。
//
// 供自带 channel 变量标签的指标集（如 IAM）使用：Prometheus 禁止同一指标既有
// 常量标签 channel 又有变量标签 channel。使用本方法可保留端别维度而不启动失败。
func (m *Metrics) RegistererWithoutChannel() prometheus.Registerer {
	if m == nil {
		return nil
	}
	labels := prometheus.Labels{}
	if m.service != "" {
		labels["service"] = m.service
	}
	if m.env != "" {
		labels["env"] = m.env
	}
	return prometheus.WrapRegistererWith(labels, m.rawRegisterer)
}

// Gatherer 返回底层 Gatherer（供 Handler 与测试断言）。
func (m *Metrics) Gatherer() prometheus.Gatherer {
	if m == nil {
		return nil
	}
	return m.gatherer
}

// HTTP 返回内置 HTTP RED 指标集。
func (m *Metrics) HTTP() *HTTP {
	if m == nil {
		return nil
	}
	return m.http
}

// Handler 返回 Prometheus 文本拉取端点（通常挂 GET /metrics）。
// 默认不启用 OpenMetrics 协商以外的额外行为；超时与错误处理使用 promhttp 默认。
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
		})
	}
	return promhttp.HandlerFor(m.gatherer, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		// 抓取失败时返回 500，避免静默空页。
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
}

// MustRegister 向本 Registry 注册采集器；失败时 panic（仅用于进程启动确定性装配）。
func (m *Metrics) MustRegister(cs ...prometheus.Collector) {
	m.registerer.MustRegister(cs...)
}

// Register 注册采集器；已注册时返回 prometheus.AlreadyRegisteredError。
func (m *Metrics) Register(c prometheus.Collector) error {
	return m.registerer.Register(c)
}
