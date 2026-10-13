// Package tracing 提供基于 OpenTelemetry 的追踪初始化、上下文传播与 span 工具。
//
// 设计边界：
//   - 不得依赖 internal/；service/channel/env 由调用方写入 Resource 属性
//   - 默认传播 W3C Trace Context（traceparent / tracestate）
//   - HTTP route 标签优先使用 ServeMux Pattern，禁止默认用原始 URL.Path
//   - 导出失败不得拖垮请求路径；Shutdown 须在进程退出前调用以刷出缓冲
//
// 典型用法：
//
//	tp, err := tracing.Init(ctx, tracing.Options{
//		Service: "admin-api", Channel: "admin", Env: "prod",
//		Exporter: tracing.ExporterOTLP, Endpoint: "localhost:4318",
//	})
//	defer tp.Shutdown(ctx)
//	handler = tracing.HTTPMiddleware()(handler)
package tracing
