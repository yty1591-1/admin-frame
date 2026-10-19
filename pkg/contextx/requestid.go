package contextx

import (
	"context"

	"github.com/admin-frame/admin-frame/pkg/logger"
)

// WithRequestID 写入请求关联 ID（与 X-Request-ID / 信封 trace_id 对齐）。
// 内部委托 pkg/logger，保证 ContextHandler 自动带上 trace_id。
func WithRequestID(ctx context.Context, id string) context.Context {
	return logger.WithTraceID(ctx, id)
}

// RequestID 读取请求关联 ID；缺失返回空串。
func RequestID(ctx context.Context) string {
	return logger.TraceIDFromContext(ctx)
}
