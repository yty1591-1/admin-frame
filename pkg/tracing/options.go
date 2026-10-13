package tracing

import (
	"fmt"
	"strings"
)

// 导出器名称（Options.Exporter）。
const (
	// ExporterNone 启用 SDK（生成有效 TraceID）但不导出，适合本地默认。
	ExporterNone = "none"
	// ExporterOTLP 通过 OTLP/HTTP 导出。
	ExporterOTLP = "otlp"
	// ExporterStdout 打印到 stdout（仅排障；生产禁止）。
	ExporterStdout = "stdout"
	// ExporterNoop 完全禁用采样与导出（无有效 TraceID）。
	ExporterNoop = "noop"
)

// Options 控制 TracerProvider 初始化。
type Options struct {
	// Service 写入 resource service.name；必填。
	Service string
	// Channel 写入 resource 属性 deployment.channel（本仓库 OBS 约定）。
	Channel string
	// Env 写入 deployment.environment。
	Env string
	// Version 写入 service.version。
	Version string

	// Exporter 见 Exporter* 常量；空等价于 ExporterNone。
	Exporter string
	// Endpoint OTLP/HTTP 主机或 URL（如 localhost:4318 或 https://otel:4318）。
	// Exporter=otlp 时必填。
	Endpoint string
	// Insecure 对 OTLP 使用明文 HTTP（无 TLS）；仅限内网/本地。
	Insecure bool

	// SampleRatio 头采样比例 [0,1]；nil 表示默认 1.0。
	// ParentBased：已采样父 span 仍跟随。
	SampleRatio *float64

	// TracerName HTTP 中间件等默认 Tracer 名；空则为 service 名。
	TracerName string
}

// Ratio 返回采样比例指针，供 Options.SampleRatio 使用。
func Ratio(v float64) *float64 { return &v }

func (o Options) validate() error {
	if strings.TrimSpace(o.Service) == "" {
		return fmt.Errorf("tracing: Service is required")
	}
	exp := o.exporter()
	switch exp {
	case ExporterNone, ExporterOTLP, ExporterStdout, ExporterNoop:
	default:
		return fmt.Errorf("tracing: unsupported Exporter %q", o.Exporter)
	}
	if exp == ExporterOTLP && strings.TrimSpace(o.Endpoint) == "" {
		return fmt.Errorf("tracing: Endpoint is required when Exporter=otlp")
	}
	if o.SampleRatio != nil {
		r := *o.SampleRatio
		if r < 0 || r > 1 {
			return fmt.Errorf("tracing: SampleRatio must be in [0,1]")
		}
	}
	return nil
}

func (o Options) exporter() string {
	e := strings.ToLower(strings.TrimSpace(o.Exporter))
	if e == "" {
		return ExporterNone
	}
	return e
}

func (o Options) sampleRatio() float64 {
	if o.SampleRatio == nil {
		return 1.0
	}
	return *o.SampleRatio
}

func (o Options) tracerName() string {
	if o.TracerName != "" {
		return o.TracerName
	}
	return o.Service
}
