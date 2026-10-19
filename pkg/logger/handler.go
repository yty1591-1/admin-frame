package logger

import (
	"context"
	"log/slog"

	"github.com/admin-frame/admin-frame/pkg/tracing"
)

// ContextHandler 在每条日志上附加 context 中的关联字段（OBS-01）。
//
// 优先级：
//   - trace_id：WithTraceID 关联键，否则 OTel TraceID
//   - span_id：OTel SpanID
//   - request_id：与 trace_id 相同时不重复；若未来分离可扩展
type ContextHandler struct {
	next slog.Handler
}

// NewContextHandler 包装底层 Handler。
func NewContextHandler(next slog.Handler) *ContextHandler {
	return &ContextHandler{next: next}
}

// Enabled 实现 slog.Handler。
func (h *ContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle 实现 slog.Handler。
func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, a := range contextAttrs(ctx) {
		if recordHasAttr(r, a.Key) {
			continue
		}
		r.AddAttrs(a)
	}
	return h.next.Handle(ctx, r)
}

// WithAttrs 实现 slog.Handler。
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{next: h.next.WithAttrs(attrs)}
}

// WithGroup 实现 slog.Handler。
func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{next: h.next.WithGroup(name)}
}

func contextAttrs(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	var attrs []slog.Attr
	corr := TraceIDFromContext(ctx)
	otelTrace := tracing.TraceID(ctx)
	traceID := corr
	if traceID == "" {
		traceID = otelTrace
	}
	if traceID != "" {
		attrs = append(attrs, slog.String(FieldTraceID, traceID))
	}
	if sid := tracing.SpanID(ctx); sid != "" {
		attrs = append(attrs, slog.String(FieldSpanID, sid))
	}
	// request_id：关联键与 envelope 对齐；与 trace_id 不同时才额外写出
	if corr != "" && corr != traceID {
		attrs = append(attrs, slog.String(FieldRequestID, corr))
	}
	return attrs
}

func recordHasAttr(r slog.Record, key string) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = true
			return false
		}
		return true
	})
	return found
}
