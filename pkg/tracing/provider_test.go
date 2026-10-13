package tracing_test

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/admin-frame/admin-frame/pkg/tracing"
)

func TestInitRequiresService(t *testing.T) {
	_, err := tracing.Init(context.Background(), tracing.Options{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInitNoneGeneratesTraceID(t *testing.T) {
	tp, err := tracing.Init(context.Background(), tracing.Options{
		Service: "admin-api",
		Channel: "admin",
		Env:     "test",
		Version: "dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tracing.Start(context.Background(), "unit")
	defer span.End()
	if id := tracing.TraceID(ctx); id == "" {
		t.Fatal("expected non-empty TraceID")
	}
	if id := tracing.SpanID(ctx); id == "" {
		t.Fatal("expected non-empty SpanID")
	}
}

func TestInitNoopHasNoValidTraceID(t *testing.T) {
	tp, err := tracing.Init(context.Background(), tracing.Options{
		Service:  "admin-api",
		Exporter: tracing.ExporterNoop,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tracing.Start(context.Background(), "noop")
	defer span.End()
	if id := tracing.TraceID(ctx); id != "" {
		t.Fatalf("noop should not yield TraceID, got %q", id)
	}
}

func TestInitOTLPRequiresEndpoint(t *testing.T) {
	_, err := tracing.Init(context.Background(), tracing.Options{
		Service:  "admin-api",
		Exporter: tracing.ExporterOTLP,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRecordError(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(context.Background(), "err-span")
	tracing.RecordError(ctx, context.Canceled)
	span.End()

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans=%d", len(spans))
	}
	if spans[0].Status().Code.String() != "Error" {
		t.Fatalf("status=%v", spans[0].Status())
	}
}
