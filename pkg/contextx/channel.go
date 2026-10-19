package contextx

import "context"

// WithChannel 写入端标识（admin / biz / c / worker）。
func WithChannel(ctx context.Context, channel string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if channel == "" {
		return ctx
	}
	return context.WithValue(ctx, channelKey{}, channel)
}

// Channel 读取端标识；缺失返回空串。
func Channel(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(channelKey{}).(string); ok {
		return v
	}
	return ""
}
