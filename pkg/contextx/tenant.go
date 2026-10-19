package contextx

import "context"

// WithMerchantID 写入租户 / 商户 ID（B 端隔离键）。
func WithMerchantID(ctx context.Context, merchantID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if merchantID == "" {
		return ctx
	}
	return context.WithValue(ctx, merchantKey{}, merchantID)
}

// MerchantID 读取商户 ID；缺失返回空串。
func MerchantID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(merchantKey{}).(string); ok {
		return v
	}
	return ""
}
