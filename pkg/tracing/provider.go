package tracing

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Provider 持有 TracerProvider，并在 Shutdown 时刷出未导出 span。
type Provider struct {
	tp         trace.TracerProvider
	sdk        *sdktrace.TracerProvider
	tracerName string
	mu         sync.Mutex
	shutdown   bool
}

// Init 初始化全局 TracerProvider 与 TextMapPropagator（W3C TraceContext + Baggage）。
// 返回的 Provider.Shutdown 须在进程退出前调用。
func Init(ctx context.Context, opts Options) (*Provider, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if opts.exporter() == ExporterNoop {
		tp := noop.NewTracerProvider()
		otel.SetTracerProvider(tp)
		return &Provider{tp: tp, tracerName: opts.tracerName()}, nil
	}

	res, err := newResource(opts)
	if err != nil {
		return nil, err
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(opts.sampleRatio()))
	sdkOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	}

	switch opts.exporter() {
	case ExporterNone:
		// 无 SpanProcessor：仍生成有效 TraceID，便于与日志 / X-Request-ID 关联。
	case ExporterStdout:
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, fmt.Errorf("tracing: stdout exporter: %w", err)
		}
		sdkOpts = append(sdkOpts, sdktrace.WithBatcher(exp))
	case ExporterOTLP:
		exp, err := newOTLPExporter(ctx, opts)
		if err != nil {
			return nil, err
		}
		sdkOpts = append(sdkOpts, sdktrace.WithBatcher(exp))
	}

	sdkTP := sdktrace.NewTracerProvider(sdkOpts...)
	otel.SetTracerProvider(sdkTP)
	return &Provider{tp: sdkTP, sdk: sdkTP, tracerName: opts.tracerName()}, nil
}

func newResource(opts Options) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(opts.Service),
	}
	if opts.Version != "" {
		attrs = append(attrs, semconv.ServiceVersion(opts.Version))
	}
	if opts.Env != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentNameKey.String(opts.Env))
	}
	if opts.Channel != "" {
		attrs = append(attrs, attribute.String("deployment.channel", opts.Channel))
	}
	return resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, attrs...),
	)
}

func newOTLPExporter(ctx context.Context, opts Options) (sdktrace.SpanExporter, error) {
	endpoint := strings.TrimSpace(opts.Endpoint)
	clientOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(stripOTLPScheme(endpoint)),
	}
	if opts.Insecure || strings.HasPrefix(endpoint, "http://") {
		clientOpts = append(clientOpts, otlptracehttp.WithInsecure())
	}
	exp, err := otlptracehttp.New(ctx, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("tracing: otlp exporter: %w", err)
	}
	return exp, nil
}

// stripOTLPScheme 去掉 WithEndpoint 不接受的 scheme 前缀。
func stripOTLPScheme(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(strings.ToLower(endpoint), p) {
			return endpoint[len(p):]
		}
	}
	return endpoint
}

// Shutdown 刷出并关闭 TracerProvider；可安全重复调用。
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shutdown {
		return nil
	}
	p.shutdown = true
	if p.sdk != nil {
		return p.sdk.Shutdown(ctx)
	}
	return nil
}

// Tracer 返回命名 Tracer（默认使用 Init 时的 TracerName）。
func (p *Provider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	if name == "" && p != nil {
		name = p.tracerName
	}
	if p != nil && p.tp != nil {
		return p.tp.Tracer(name, opts...)
	}
	return otel.Tracer(name, opts...)
}

// TracerProvider 返回底层 Provider（测试或高级装配用）。
func (p *Provider) TracerProvider() trace.TracerProvider {
	if p == nil || p.tp == nil {
		return otel.GetTracerProvider()
	}
	return p.tp
}
