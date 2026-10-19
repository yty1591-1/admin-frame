package httpclient

import (
	"fmt"
	"net/http"

	"github.com/admin-frame/admin-frame/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

type traceTransport struct {
	next            http.RoundTripper
	tracer          trace.Tracer
	propagator      propagation.TextMapPropagator
	injectRequestID bool
}

func (t *traceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || t.next == nil {
		return nil, ErrInvalidConfig
	}
	ctx := req.Context()
	name := req.Method + " " + hostOrUnknown(req)
	ctx, span := t.tracer.Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.HTTPRequestMethodKey.String(req.Method),
			attribute.String("url.full", redactURL(req)),
		),
	)
	defer span.End()

	req = req.WithContext(ctx)
	t.propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
	if t.injectRequestID && req.Header.Get("X-Request-ID") == "" {
		if tid := tracing.TraceID(ctx); tid != "" {
			req.Header.Set("X-Request-ID", tid)
		}
	}

	resp, err := t.next.RoundTrip(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))
	if resp.StatusCode >= 500 {
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
	return resp, nil
}

func hostOrUnknown(req *http.Request) string {
	if req == nil || req.URL == nil || req.URL.Host == "" {
		return "unknown"
	}
	return req.URL.Host
}

func redactURL(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	return req.URL.Redacted()
}
