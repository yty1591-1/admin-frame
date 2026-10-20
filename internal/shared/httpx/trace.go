package httpx

import (
	"context"

	"github.com/admin-frame/admin-frame/pkg/contextx"
)

// TraceIDFromContext 读取请求关联 ID；缺失时返回空串。
//
// 该值同时用于响应信封 trace_id 与日志字段（OBS-05），实现委托 pkg/contextx，
// 便于 transport 在不依赖 internal/platform 的前提下取用。
func TraceIDFromContext(ctx context.Context) string {
	return contextx.RequestID(ctx)
}
