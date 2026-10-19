package logger

import "context"

type ctxTraceKey struct{}

// WithTraceID 将请求关联 ID 写入 context（与 X-Request-ID / 信封 trace_id 对齐）。
func WithTraceID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxTraceKey{}, id)
}

// TraceIDFromContext 读取 WithTraceID 写入的关联 ID；缺失返回空串。
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(ctxTraceKey{}).(string); ok {
		return v
	}
	return ""
}
