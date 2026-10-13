package tracing

import (
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

// 默认排除的探针 / 运维路径，避免噪声 span。
var defaultExcludePaths = map[string]struct{}{
	"/healthz": {},
	"/readyz":  {},
	"/metrics": {},
	"/version": {},
}

// HTTPOption 配置 HTTP 服务端中间件。
type HTTPOption func(*httpConfig)

type httpConfig struct {
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	exclude    map[string]struct{}
	spanName   func(*http.Request) string
}

// WithTracer 指定 Tracer；默认 otel.Tracer("http.server")。
func WithTracer(t trace.Tracer) HTTPOption {
	return func(c *httpConfig) {
		if t != nil {
			c.tracer = t
		}
	}
}

// WithPropagator 覆盖全局传播器（测试用）。
func WithPropagator(p propagation.TextMapPropagator) HTTPOption {
	return func(c *httpConfig) {
		if p != nil {
			c.propagator = p
		}
	}
}

// WithExcludePaths 追加不建 span 的路径（精确匹配 URL.Path）。
func WithExcludePaths(paths ...string) HTTPOption {
	return func(c *httpConfig) {
		for _, p := range paths {
			c.exclude[p] = struct{}{}
		}
	}
}

// WithSpanName 自定义 span 名；默认 METHOD + route 模板。
func WithSpanName(fn func(*http.Request) string) HTTPOption {
	return func(c *httpConfig) {
		if fn != nil {
			c.spanName = fn
		}
	}
}

// HTTPMiddleware 返回服务端追踪中间件：提取 W3C 上下文、创建 SERVER span、写回传播头。
//
// 须置于 RequestID 之前，以便无 X-Request-ID 时可用 TraceID 作为关联 ID。
func HTTPMiddleware(opts ...HTTPOption) func(http.Handler) http.Handler {
	cfg := &httpConfig{
		tracer:     otel.Tracer("http.server"),
		propagator: otel.GetTextMapPropagator(),
		exclude:    copyExclude(defaultExcludePaths),
		spanName:   defaultSpanName,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, skip := cfg.exclude[r.URL.Path]; skip {
				next.ServeHTTP(w, r)
				return
			}

			ctx := cfg.propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			name := cfg.spanName(r)
			ctx, span := cfg.tracer.Start(ctx, name,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPRequestMethodKey.String(r.Method),
					semconv.URLScheme(requestScheme(r)),
					attribute.String("url.path", r.URL.Path),
				),
			)
			defer span.End()

			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			// 将当前上下文注入响应头，便于下游/客户端关联（可选；主要靠请求入站 Extract）。
			cfg.propagator.Inject(ctx, propagation.HeaderCarrier(rw.Header()))

			// WithContext 返回浅拷贝；ServeMux 会把 Pattern 写在该拷贝上。
			req := r.WithContext(ctx)
			next.ServeHTTP(rw, req)

			route := routeFromRequest(req)
			if route != "" {
				span.SetAttributes(attribute.String("http.route", route))
				span.SetName(req.Method + " " + route)
			}
			span.SetAttributes(semconv.HTTPResponseStatusCode(rw.status))
			if rw.status >= 500 {
				span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", rw.status))
			}
		})
	}
}

func defaultSpanName(r *http.Request) string {
	if r == nil {
		return "HTTP"
	}
	route := routeFromRequest(r)
	if route == "" {
		route = "unknown"
	}
	return r.Method + " " + route
}

func routeFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if p := r.Pattern; p != "" {
		return normalizePattern(p)
	}
	return ""
}

func normalizePattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "unknown"
	}
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		rest := strings.TrimSpace(pattern[i+1:])
		if rest != "" {
			return rest
		}
	}
	return pattern
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}
	return "http"
}

func copyExclude(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader 记录状态码并转发给底层 ResponseWriter。
func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap 支持 ResponseController / 可选接口透传。
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
