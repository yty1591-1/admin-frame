package idempotency

import "context"

type ctxKey struct{}

// WithKey 将已校验的客户端幂等键写入 context，供下游 Handler / app 读取。
func WithKey(ctx context.Context, key string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, key)
}

// KeyFrom 读取中间件注入的幂等键。
func KeyFrom(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.Value(ctxKey{}).(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}
