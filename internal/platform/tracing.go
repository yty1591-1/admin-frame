package platform

import (
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

// TracingOptions 将本进程配置映射为 pkg/tracing.Options。
func (c *Config) TracingOptions(version string) tracing.Options {
	if c == nil {
		return tracing.Options{Exporter: tracing.ExporterNoop, Service: "unknown"}
	}
	exp := c.Tracing.Exporter
	if !c.Tracing.IsEnabled() {
		exp = tracing.ExporterNoop
	}
	return tracing.Options{
		Service:     c.Service,
		Channel:     c.Channel,
		Env:         c.Env,
		Version:     version,
		Exporter:    exp,
		Endpoint:    c.Tracing.Endpoint,
		Insecure:    c.Tracing.Insecure,
		SampleRatio: c.Tracing.SampleRatio,
	}
}
