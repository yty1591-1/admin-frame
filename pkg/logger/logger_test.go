package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/admin-frame/admin-frame/pkg/logger"
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

func TestNewBindsServiceChannelEnv(t *testing.T) {
	var buf bytes.Buffer
	log, err := logger.New(logger.Config{
		Level:   "info",
		Format:  "json",
		Output:  &buf,
		Service: "admin-api",
		Channel: "admin",
		Env:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	log.Info("hello")
	line := buf.String()
	for _, field := range []string{`"service":"admin-api"`, `"channel":"admin"`, `"env":"test"`} {
		if !strings.Contains(line, field) {
			t.Fatalf("missing %s in %s", field, line)
		}
	}
}

func TestContextHandlerAddsTraceID(t *testing.T) {
	var buf bytes.Buffer
	log, err := logger.New(logger.Config{
		Format: "json",
		Output: &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := logger.WithTraceID(context.Background(), "corr-123")
	log.InfoContext(ctx, "ping")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec[logger.FieldTraceID] != "corr-123" {
		t.Fatalf("trace_id=%v", rec[logger.FieldTraceID])
	}
}

func TestContextHandlerOTelFallback(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	defer span.End()
	tid := tracing.TraceID(ctx)
	if tid == "" {
		t.Fatal("empty otel trace")
	}

	var buf bytes.Buffer
	log, err := logger.New(logger.Config{Format: "json", Output: &buf})
	if err != nil {
		t.Fatal(err)
	}
	log.InfoContext(ctx, "otel")
	if !strings.Contains(buf.String(), tid) {
		t.Fatalf("want otel tid in %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"span_id"`) {
		t.Fatalf("want span_id in %s", buf.String())
	}
}

func TestValidateBadFormat(t *testing.T) {
	_, err := logger.New(logger.Config{Format: "xml"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log, err := logger.New(logger.Config{Level: "error", Format: "json", Output: &buf})
	if err != nil {
		t.Fatal(err)
	}
	log.Info("skip")
	log.Error("keep")
	if strings.Contains(buf.String(), "skip") {
		t.Fatal("info should be filtered")
	}
	if !strings.Contains(buf.String(), "keep") {
		t.Fatal("error missing")
	}
}

func TestHandlerEnabled(t *testing.T) {
	h := logger.NewContextHandler(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("info should be disabled")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("error should be enabled")
	}
}
