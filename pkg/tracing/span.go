package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Tracer 返回全局 TracerProvider 上的命名 Tracer。
func Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return otel.Tracer(name, opts...)
}

// Start 在 ctx 上开启子 span，返回带 span 的 context。
// 调用方必须 End span（通常 defer span.End()）。
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return otel.Tracer("").Start(ctx, name, opts...)
}

// StartFrom 使用指定 Tracer 开启 span。
func StartFrom(ctx context.Context, tracer trace.Tracer, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if tracer == nil {
		tracer = otel.Tracer("")
	}
	return tracer.Start(ctx, name, opts...)
}

// TraceID 返回当前 span 的 TraceID 十六进制串；无效时返回空串。
func TraceID(ctx context.Context) string {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// SpanID 返回当前 span 的 SpanID 十六进制串；无效时返回空串。
func SpanID(ctx context.Context) string {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return ""
	}
	return sc.SpanID().String()
}

// RecordError 将 err 记录到当前 span，并标记 Error 状态。
// err 为 nil 时为 no-op。禁止传入含密钥 / PII 的错误文本（SEC）。
func RecordError(ctx context.Context, err error, opts ...trace.EventOption) {
	if err == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.RecordError(err, opts...)
	span.SetStatus(codes.Error, err.Error())
}

// SetAttributes 向当前 span 追加属性。
func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(attrs...)
}

// Attr 构造字符串属性的快捷方式。
func Attr(key, value string) attribute.KeyValue {
	return attribute.String(key, value)
}
